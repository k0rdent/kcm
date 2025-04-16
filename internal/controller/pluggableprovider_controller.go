// Copyright 2025
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
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
)

const defaultSyncPeriod = 3 * time.Minute

// PluggableProviderReconciler reconciles a PluggableProvider objects
type PluggableProviderReconciler struct {
	client.Client
	SystemNamespace string
	syncPeriod      time.Duration
}

func (r *PluggableProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("PluggableProvider reconcile start")

	pprov := &kcm.PluggableProvider{}
	if err := r.Get(ctx, req.NamespacedName, pprov); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if updated, err := utils.AddKCMComponentLabel(ctx, r.Client, pprov); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}

		return ctrl.Result{}, err
	}

	if updated, err := utils.AddPluggableProviderLabels(ctx, r.Client, pprov); updated || err != nil {
		if err != nil {
			l.Error(err, "adding provider labels")
		}

		return ctrl.Result{}, err
	}

	defer func() {
		err = errors.Join(err, r.updateStatus(ctx, pprov))
	}()

	return ctrl.Result{RequeueAfter: r.syncPeriod}, nil
}

func (r *PluggableProviderReconciler) updateStatus(ctx context.Context, pprov *kcm.PluggableProvider) error {
	annotations := pprov.Annotations
	if annotations == nil {
		annotations = make(map[string]string)
	}

	pprov.Status.Infrastructure = cmp.Or(
		annotations[kcm.InfrastructureProviderOverrideAnnotation],
		kcm.InfrastructureProviderPrefix+pprov.Name,
	)

	pprov.Status.CAPI = cmp.Or(
		annotations[kcm.ClusterAPIProviderOverrideAnnotation],
		kcm.ClusterAPIProviderPrefix+pprov.Name,
	)

	if err := r.Client.Status().Update(ctx, pprov); err != nil {
		return fmt.Errorf("failed to update PluggableProvider %s/%s status: %w", pprov.Namespace, pprov.Name, err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PluggableProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.syncPeriod = defaultSyncPeriod

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcm.PluggableProvider{}).
		Complete(r)
}
