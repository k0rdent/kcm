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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/controller/ipam/adapter"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
)

type ClusterIPAMReconciler struct {
	client.Client

	defaultRequeueTime time.Duration
}

func (r *ClusterIPAMReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ClusterIPAM")

	clusterIPAM := &kcmv1.ClusterIPAM{}
	if err := r.Get(ctx, req.NamespacedName, clusterIPAM); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ClusterIPAM not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		l.Error(err, "Failed to get ClusterIPAM")
		return ctrl.Result{}, err
	}

	clusterIPAMClaim := &kcmv1.ClusterIPAMClaim{}
	if err := r.Get(ctx, client.ObjectKey{Name: clusterIPAM.Spec.ClusterIPAMClaimRef, Namespace: clusterIPAM.Namespace}, clusterIPAMClaim); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ClusterIPAMClaim: %w", err)
	}

	l.Info("Processing provider specific data")
	adapterData, err := r.processProvider(ctx, clusterIPAMClaim)
	if err != nil {
		apimeta.SetStatusCondition(&clusterIPAMClaim.Status.Conditions,
			metav1.Condition{
				Type:               kcmv1.IPAMProviderConditionError,
				Status:             metav1.ConditionUnknown,
				Reason:             kcmv1.FailedReason,
				Message:            fmt.Sprintf("ClusterIPAMClaim provider processing failed: %v", err),
				ObservedGeneration: clusterIPAMClaim.Generation,
			})

		return ctrl.Result{}, fmt.Errorf("failed to create provider specific data for ClusterIPAM %s/%s: %w", clusterIPAM.Namespace, clusterIPAM.Name, err)
	}

	apimeta.RemoveStatusCondition(&clusterIPAMClaim.Status.Conditions, kcmv1.IPAMProviderConditionError)
	clusterIPAM.Status.Phase = kcmv1.ClusterIPAMPhasePending
	if adapterData.Ready {
		clusterIPAM.Status.Phase = kcmv1.ClusterIPAMPhaseBound
	}
	clusterIPAM.Status.ProviderData = []kcmv1.ClusterIPAMProviderData{adapterData}

	if err := r.Status().Update(ctx, clusterIPAM); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ClusterIPAM status: %w", err)
	}

	if !adapterData.Ready {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}
	return ctrl.Result{}, nil
}

func (r *ClusterIPAMReconciler) processProvider(ctx context.Context, clusterIPAMClaim *kcmv1.ClusterIPAMClaim) (kcmv1.ClusterIPAMProviderData, error) {
	ipamAdapter, err := adapter.Builder(clusterIPAMClaim.Spec.Provider)
	if err != nil {
		return kcmv1.ClusterIPAMProviderData{}, fmt.Errorf("failed to build IPAM adapter for provider '%s': %w", clusterIPAMClaim.Spec.Provider, err)
	}

	return ipamAdapter.BindAddress(ctx, adapter.IPAMConfig{
		ClusterIPAMClaim: clusterIPAMClaim,
	}, r.Client)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterIPAMReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.defaultRequeueTime = 10 * time.Second
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.ClusterIPAM{}).
		Complete(r)
}
