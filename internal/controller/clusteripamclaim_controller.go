// Copyright 2024
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/utils"
)

// ClusterIPAMClaimReconciler reconciles a ClusterIPAMClaim object
type ClusterIPAMClaimReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	defaultRequeueTime time.Duration
}

// +kubebuilder:rbac:groups=k0rdent.mirantis.com.k0rdent.mirantis.com,resources=clusteripamclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=k0rdent.mirantis.com.k0rdent.mirantis.com,resources=clusteripamclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=k0rdent.mirantis.com.k0rdent.mirantis.com,resources=clusteripamclaims/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ClusterIPAMClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	ci := &kcm.ClusterIPAMClaim{}
	if err := r.Client.Get(ctx, req.NamespacedName, ci); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ClusterIPAMClaim not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		l.Error(err, "Failed to get ClusterIPAMClaim")
		return ctrl.Result{}, err
	}

	ci.TypeMeta = metav1.TypeMeta{
		APIVersion: kcm.GroupVersion.String(),
		Kind:       "ClusterIPAMClaim",
	}

	if !ci.DeletionTimestamp.IsZero() {
		l.Info("Deleting ClusterIpamClaim")
		return r.Delete(ctx, ci)
	}

	if controllerutil.AddFinalizer(ci, kcm.ClusterIPAMClaimFinalizer) {
		if err := r.Client.Update(ctx, ci); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update ClusterIPAMClaim %s/%s: %w", ci.Namespace, ci.Name, err)
		}
	}

	if err := r.CreateClusterIPAM(ctx, ci); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create ClusterIPAM %s/%s: %w", ci.Namespace, ci.Name, err)
	}
	return r.updateStatus(ctx, ci)
}

func (r *ClusterIPAMClaimReconciler) updateStatus(ctx context.Context, ci *kcm.ClusterIPAMClaim) (ctrl.Result, error) {
	requeue := false

	if ci.Status.ClusterIPAMRef.Name != "" {
		ipam := kcm.ClusterIPAM{}
		err := r.Client.Get(ctx, client.ObjectKey{
			Namespace: ci.Status.ClusterIPAMRef.Namespace,
			Name:      ci.Status.ClusterIPAMRef.Name,
		}, &ipam)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get ClusterIPAM %s/%s: %w", ci.Namespace, ci.Name, err)
		}

		ipam.Status.Phase = kcm.IpamPhasePending
		claims := ipam.Spec.NodeIPClaims
		claims = append(claims, ipam.Spec.ClusterIPClaims...)
		claims = append(claims, ipam.Spec.ExternalIPClaims...)

		allBound := true
		for _, claimRef := range claims {
			ipClaim := ipamv1.IPAddressClaim{}

			err := r.Client.Get(ctx, client.ObjectKey{Name: claimRef.Name, Namespace: claimRef.Namespace}, &ipClaim)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to get ip claim: %s : %w", claimRef.Name, err)
			}

			if ipClaim.Status.AddressRef.Name == "" {
				allBound = false
				requeue = true
			}
		}

		if allBound {
			ipam.Status.Phase = kcm.IpamPhaseBound
			requeue = false
		}

		ci.Status.Bound = allBound
		if err := r.Status().Update(ctx, ci); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update ClusterIPAMClaim status: %w", err)
		}

		if err := r.Status().Update(ctx, &ipam); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update ClusterIPAM status: %w", err)
		}
	}

	if requeue {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ClusterIPAMClaimReconciler) CreateClusterIPAM(ctx context.Context, ci *kcm.ClusterIPAMClaim) (returnErr error) {
	ipam := kcm.ClusterIPAM{
		ObjectMeta: metav1.ObjectMeta{Name: ci.Name, Namespace: ci.Namespace},
		Spec: kcm.ClusterIPAMSpec{
			Provider: ci.Spec.Provider,
		},
	}

	ci.Status.ClusterIPAMRef = corev1.ObjectReference{Name: ci.Name, Namespace: ci.Namespace}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: ci.Name, Namespace: ci.Namespace}, &ipam); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get ClusterIPAM %s/%s: %w", ci.Namespace, ci.Name, err)
	}

	clusterIPAMSpec := ipam.Spec
	defer func() {
		utils.AddOwnerReference(&ipam, ci)
		_, err := ctrl.CreateOrUpdate(ctx, r.Client, &ipam, func() error {
			ipam.Spec = clusterIPAMSpec
			return nil
		})
		if err != nil {
			returnErr = fmt.Errorf("failed to create or update ClusterIPAM %s/%s: %w", ci.Namespace, ci.Name, err)
		}
	}()

	claims, err := r.createOrUpdateIPClaims(ctx, ci, clusterIPAMSpec.NodeIPClaims, ci.Spec.NodeIPPool)
	if err != nil {
		return fmt.Errorf("failed to create or update node ip claims for pool %s: %w", ci.Spec.NodeIPPool.Name, err)
	}
	clusterIPAMSpec.NodeIPClaims = claims

	claims, err = r.createOrUpdateIPClaims(ctx, ci, clusterIPAMSpec.ClusterIPClaims, ci.Spec.ClusterIPPool)
	if err != nil {
		return fmt.Errorf("failed to create or update cluster ip claims for pool %s: %w", ci.Spec.ClusterIPPool.Name, err)
	}
	clusterIPAMSpec.ClusterIPClaims = claims

	claims, err = r.createOrUpdateIPClaims(ctx, ci, clusterIPAMSpec.ExternalIPClaims, ci.Spec.ExternalIPPool)
	if err != nil {
		return fmt.Errorf("failed to create or update cluster ip claims for pool %s: %w", ci.Spec.ExternalIPPool.Name, err)
	}
	clusterIPAMSpec.ExternalIPClaims = claims
	return nil
}

func (r *ClusterIPAMClaimReconciler) createOrUpdateIPClaims(ctx context.Context, ci *kcm.ClusterIPAMClaim, claims []corev1.ObjectReference, ipPool kcm.IPPoolSpec) ([]corev1.ObjectReference, error) {
	desiredCount := ipPool.Count

	if claims != nil {
		currentCount := len(claims)
		if currentCount > desiredCount {
			// remove from the end of the list
			removalList := claims[desiredCount:currentCount]
			claims = claims[0:desiredCount]

			for _, toRemove := range removalList {
				err := r.Client.Delete(ctx, &ipamv1.IPAddressClaim{ObjectMeta: metav1.ObjectMeta{
					Name:      toRemove.Name,
					Namespace: toRemove.Namespace,
				}})

				if client.IgnoreNotFound(err) != nil {
					return claims, fmt.Errorf("failed to delete IPAddressClaim: %s/%s", toRemove.Name, toRemove.Namespace)
				}
			}
			// we've reached the desired count
			desiredCount = 0
		} else if currentCount <= desiredCount {
			desiredCount -= currentCount
		}
	}

	if ipPool.Name != "" {
		for range desiredCount {
			claim, err := r.CreateIPAddressClaim(ctx, ci.Namespace, ipPool.TypedLocalObjectReference, ci)
			if err != nil {
				return claims, fmt.Errorf("failed to create node ip address claims for: %s pool : %w", ipPool.Name, err)
			}
			claims = append(claims, claim)
		}
	}
	return claims, nil
}

func (r *ClusterIPAMClaimReconciler) CreateIPAddressClaim(ctx context.Context, namespace string, pool corev1.TypedLocalObjectReference, ci *kcm.ClusterIPAMClaim) (corev1.ObjectReference, error) {
	claim := ipamv1.IPAddressClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: pool.Name + "-",
		},
		Spec: ipamv1.IPAddressClaimSpec{
			PoolRef: pool,
		},
	}

	utils.AddOwnerReference(&claim, ci)
	utils.AddLabel(&claim, kcm.OwnedByLabel, ci.Name)
	if err := r.Create(ctx, &claim); err != nil {
		return corev1.ObjectReference{Name: claim.Name, Namespace: namespace}, fmt.Errorf("failed to create IP address claim: %w", err)
	}
	return corev1.ObjectReference{Name: claim.Name, Namespace: namespace}, nil
}

func (r *ClusterIPAMClaimReconciler) Delete(ctx context.Context, ci *kcm.ClusterIPAMClaim) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	l.Info("Removing Finalizer", "finalizer", kcm.ClusterIPAMClaimFinalizer)
	if controllerutil.RemoveFinalizer(ci, kcm.ClusterIPAMClaimFinalizer) {
		if err := r.Client.Update(ctx, ci); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update ClusterIPAMClaim %s/%s: %w", ci.Namespace, ci.Name, err)
		}
	}
	l.Info("ClusterIPAMClaim deleted")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterIPAMClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.defaultRequeueTime = 10 * time.Second
	return ctrl.NewControllerManagedBy(mgr).
		For(&kcm.ClusterIPAMClaim{}).
		Complete(r)
}
