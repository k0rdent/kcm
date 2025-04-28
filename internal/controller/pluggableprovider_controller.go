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
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
)

// PluggableProviderReconciler reconciles a PluggableProvider objects
type PluggableProviderReconciler struct {
	client.Client
	syncPeriod time.Duration
}

func (r *PluggableProviderReconciler) getExposedProviders(ctx context.Context, pprov *kcm.PluggableProvider) (string, error) {
	management := &kcm.Management{}
	if err := r.Get(ctx, client.ObjectKey{Name: kcm.ManagementName}, management); err != nil {
		return "", err
	}
	return strings.Join(management.Status.Components[pprov.Name].ExposedProviders, ","), nil
}

func (r *PluggableProviderReconciler) addLabels(ctx context.Context, pprov *kcm.PluggableProvider) error {
	_, err := utils.AddKCMComponentLabel(ctx, r.Client, pprov)
	return err
}

func (r *PluggableProviderReconciler) updateStatus(ctx context.Context, pprov *kcm.PluggableProvider) error {
	exposedProviders, err := r.getExposedProviders(ctx, pprov)
	if err != nil {
		return err
	}

	if exposedProviders == "" {
		return nil
	}

	pprov.Status.ExposedProviders = exposedProviders
	if err := r.Client.Status().Update(ctx, pprov); err != nil {
		return fmt.Errorf("failed to update PluggableProvider %q status: %w", pprov.Name, err)
	}

	return nil
}

func (r *PluggableProviderReconciler) update(ctx context.Context, pprov *kcm.PluggableProvider) (_ ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("PluggableProvider object event")

	if err := r.addLabels(ctx, pprov); err != nil {
		l.Error(err, "PluggableProvider adding labels error")
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, pprov); err != nil {
		l.Error(err, "PluggableProvider update status error")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *PluggableProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("PluggableProvider reconcile start")

	var pprov kcm.PluggableProvider

	if err := r.Get(ctx, req.NamespacedName, &pprov); err != nil {
		l.Error(err, "PluggableProvider reconcile error")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.update(ctx, &pprov)
}

// SetupWithManager sets up the controller with the Manager.
func (r *PluggableProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	const defaultSyncPeriod = 5 * time.Minute

	r.syncPeriod = defaultSyncPeriod

	respFunc := func(ctx context.Context, _ client.Object) []ctrl.Request {
		objList := new(kcm.PluggableProviderList)

		if err := mgr.GetClient().List(ctx, objList,
			client.MatchingLabels{kcm.GenericComponentNameLabel: kcm.GenericComponentLabelValueKCM},
		); err != nil {
			return nil
		}

		resp := make([]ctrl.Request, 0, len(objList.Items))
		for _, el := range objList.Items {
			resp = append(resp, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&el)})
		}

		return resp
	}

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcm.PluggableProvider{}).
		Watches(&kcm.Management{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
			return respFunc(ctx, obj)
		}), builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return utils.HasLabel(e.Object, kcm.GenericComponentNameLabel)
			},
			DeleteFunc: func(event.DeleteEvent) bool {
				return true
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				if !utils.HasLabel(e.ObjectNew, kcm.GenericComponentNameLabel) {
					return false
				}

				oldObj, ok := e.ObjectOld.(*kcm.Management)
				if !ok {
					return false
				}

				newObj, ok := e.ObjectNew.(*kcm.Management)
				if !ok {
					return false
				}

				if len(oldObj.Status.Components) != len(newObj.Status.Components) {
					return true
				}

				return !equality.Semantic.DeepEqual(oldObj.Status.Components, newObj.Status.Components)
			},
			GenericFunc: func(event.GenericEvent) bool {
				return false
			},
		})).
		Complete(r)
}
