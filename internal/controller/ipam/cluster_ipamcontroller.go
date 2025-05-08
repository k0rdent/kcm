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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	kcm "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/controller/ipam/adapter"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
)

type ClusterIPAMReconciler struct {
	Client             client.Client
	Scheme             *runtime.Scheme
	defaultRequeueTime time.Duration
}

func (r *ClusterIPAMReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ClusterIPAM")

	clusterIPAM := &kcm.ClusterIPAM{}
	if err := r.Client.Get(ctx, req.NamespacedName, clusterIPAM); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ClusterIPAM not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		l.Error(err, "Failed to get ClusterIPAM")
		return ctrl.Result{}, err
	}

	clusterIPAMClaim := &kcm.ClusterIPAMClaim{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: clusterIPAM.Spec.ClusterIPAMClaimRef, Namespace: clusterIPAM.Namespace}, clusterIPAMClaim); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ClusterIPAMClaim: %w", err)
	}

	l.Info("Processing provider specific data")
	adapterData, err := r.processProvider(ctx, clusterIPAMClaim)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create provider specific data for ClusterIPAM %s/%s: %w", clusterIPAM.Namespace, clusterIPAM.Name, err)
	}

	clusterIPAM.Status.Phase = kcm.ClusterIPAMPhasePending
	if adapterData.Ready {
		clusterIPAM.Status.Phase = kcm.ClusterIPAMPhaseBound
	}
	clusterIPAM.Status.ProviderData = []kcm.ClusterIPAMProviderData{adapterData}

	if err := r.Client.Status().Update(ctx, clusterIPAM); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ClusterIPAM status: %w", err)
	}

	if !adapterData.Ready {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}
	return ctrl.Result{}, nil
}

func (r *ClusterIPAMReconciler) processProvider(ctx context.Context, clusterIPAMClaim *kcm.ClusterIPAMClaim) (kcm.ClusterIPAMProviderData, error) {
	clusterDeployment := &kcm.ClusterDeployment{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: clusterIPAMClaim.Spec.ClusterDeploymentRef, Namespace: clusterIPAMClaim.Namespace}, clusterDeployment); err != nil {
		return kcm.ClusterIPAMProviderData{}, fmt.Errorf("failed to get ClusterDeployment %s: %w", clusterIPAMClaim.Spec.ClusterDeploymentRef, err)
	}

	return adapter.Builder(clusterIPAMClaim.Spec.Provider).
		BindAddress(ctx, adapter.IPAMConfig{
			ClusterDeployment: clusterDeployment,
			ClusterIPAMClaim:  clusterIPAMClaim,
		}, r.Client)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterIPAMReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.defaultRequeueTime = 10 * time.Second
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcm.ClusterIPAM{}).
		Complete(r)
}
