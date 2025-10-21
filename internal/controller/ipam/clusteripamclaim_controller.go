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

package ipam

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/metrics"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
)

type ClusterIPAMClaimReconciler struct {
	client.Client
}

func (r *ClusterIPAMClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ClusterIPAMClaim")

	ci := &kcmv1.ClusterIPAMClaim{}
	if err := r.Get(ctx, req.NamespacedName, ci); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ClusterIPAMClaim not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		l.Error(err, "Failed to get ClusterIPAMClaim")
		return ctrl.Result{}, err
	}

	if !ci.DeletionTimestamp.IsZero() {
		l.Info("Deleting ClusterIPAMClaim")
		metrics.TrackMetricIPAMClaimsBound(ctx, kcmv1.ClusterIPAMKind, ci.Name, ci.Namespace, false)
		metrics.TrackMetricIPAMUsage(ctx, kcmv1.ClusterIPAMKind, ci.Name, ci.Namespace, false)
		return ctrl.Result{}, nil
	}

	if err := ci.Validate(); err != nil {
		l.Error(err, "Failed to validate ClusterIPAMClaim")
		return ctrl.Result{}, nil
	}
	if err := r.createOrUpdateClusterIPAM(ctx, ci); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create ClusterIPAM %s/%s: %w", ci.Namespace, ci.Name, err)
	}

	return ctrl.Result{}, r.updateStatus(ctx, ci)
}

func (r *ClusterIPAMClaimReconciler) createOrUpdateClusterIPAM(ctx context.Context, clusterIPAMClaim *kcmv1.ClusterIPAMClaim) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Creating or updating ClusterIPAM")

	clusterIPAM := kcmv1.ClusterIPAM{
		ObjectMeta: metav1.ObjectMeta{Name: clusterIPAMClaim.Name, Namespace: clusterIPAMClaim.Namespace},
		Spec: kcmv1.ClusterIPAMSpec{
			Provider:            clusterIPAMClaim.Spec.Provider,
			ClusterIPAMClaimRef: clusterIPAMClaim.Name,
		},
	}

	clusterIPAMSpec := clusterIPAM.Spec

	if err := controllerutil.SetControllerReference(clusterIPAMClaim, &clusterIPAM, r.Scheme()); err != nil {
		return fmt.Errorf("failed to set controller reference: %w", err)
	}

	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, &clusterIPAM, func() error {
		clusterIPAM.Spec = clusterIPAMSpec
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create or update ClusterIPAM %s/%s: %w", clusterIPAMClaim.Namespace, clusterIPAMClaim.Name, err)
	}

	metrics.TrackMetricIPAMUsage(ctx, kcmv1.ClusterIPAMKind, clusterIPAM.Name, clusterIPAM.Namespace, true)

	if clusterIPAMClaim.Spec.ClusterIPAMRef != clusterIPAM.Name {
		clusterIPAMClaim.Spec.ClusterIPAMRef = clusterIPAM.Name
		return r.Update(ctx, clusterIPAMClaim)
	}

	return nil
}

func (r *ClusterIPAMClaimReconciler) updateStatus(ctx context.Context, clusterIPAMClaim *kcmv1.ClusterIPAMClaim) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Update ClusterIPAMClaim status")

	clusterIPAM := kcmv1.ClusterIPAM{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(clusterIPAMClaim), &clusterIPAM); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get ClusterIPAM %s: %w", client.ObjectKeyFromObject(clusterIPAMClaim), err)
	}

	clusterIPAMClaim.Status.Bound = clusterIPAM.Status.Phase == kcmv1.ClusterIPAMPhaseBound

	metrics.TrackMetricIPAMClaimsBound(ctx, kcmv1.ClusterIPAMKind, clusterIPAM.Name, clusterIPAM.Namespace, clusterIPAMClaim.Status.Bound)

	apimeta.RemoveStatusCondition(&clusterIPAMClaim.Status.Conditions, kcmv1.InvalidClaimConditionType)
	if err := clusterIPAMClaim.Validate(); err != nil {
		apimeta.SetStatusCondition(&clusterIPAMClaim.Status.Conditions,
			metav1.Condition{
				Type:               kcmv1.InvalidClaimConditionType,
				Status:             metav1.ConditionUnknown,
				Reason:             kcmv1.FailedReason,
				Message:            fmt.Sprintf("ClusterIPAMClaim contains invalid IP or CIDR notation: %v", err),
				ObservedGeneration: clusterIPAMClaim.Generation,
			})
	}

	if err := r.Status().Update(ctx, clusterIPAMClaim); err != nil {
		return fmt.Errorf("failed to update ClusterIPAMClaim status: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterIPAMClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.ClusterIPAMClaim{}).
		Owns(&kcmv1.ClusterIPAM{}).
		Complete(r)
}
