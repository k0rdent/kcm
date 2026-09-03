// Copyright 2026
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
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
)

// NamespacedMultiClusterServiceReconciler reconciles a NamespacedMultiClusterServiceReconciler object.
type NamespacedMultiClusterServiceReconciler struct {
	MultiClusterServiceCommonReconciler
}

// Reconcile reconciles a NamespacedMultiClusterService object. It intentionally mirrors
// MultiClusterServiceReconciler.Reconcile: each controller needs its own thin, kubebuilder-idiomatic
// entrypoint that fetches its own concrete Kind and wires it into the shared
// MultiClusterServiceCommonReconciler logic below.
//
//nolint:dupl // see doc comment
func (r *NamespacedMultiClusterServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling NamespacedMultiClusterService")

	mcs := &kcmv1.NamespacedMultiClusterService{}
	err = r.Client.Get(ctx, req.NamespacedName, mcs)
	if apierrors.IsNotFound(err) {
		l.Info("NamespacedMultiClusterService not found, ignoring since object must be deleted")
		return ctrl.Result{}, nil
	}
	if err != nil {
		l.Error(err, "Failed to get NamespacedMultiClusterService")
		return ctrl.Result{}, err
	}

	clone := mcs.DeepCopy()
	defer func() {
		// we need to explicitly requeue NamespacedMultiClusterService object,
		// otherwise we'll miss if some ClusterDeployment will be updated
		// with matching labels.
		requeue, e := r.updateStatus(ctx, clone, mcs)
		if requeue {
			result = ctrl.Result{RequeueAfter: r.defaultRequeueTime}
		}
		err = errors.Join(err, e)
	}()

	if !mcs.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, mcs)
	}

	management := &kcmv1.Management{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, management); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Management: %w", err)
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping NamespacedMultiClusterService reconciliation")
		return ctrl.Result{}, nil
	}

	return r.reconcileUpdate(ctx, mcs)
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespacedMultiClusterServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	if r.timeFunc == nil {
		r.timeFunc = time.Now
	}
	r.defaultRequeueTime = 10 * time.Second

	managedController := ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.NamespacedMultiClusterService{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&kcmv1.ServiceSet{},
			kubeutil.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) ([]ctrl.Request, error) {
				serviceSet, ok := o.(*kcmv1.ServiceSet)
				if !ok {
					return nil, nil
				}
				if serviceSet.Spec.NamespacedMultiClusterService == "" {
					return nil, nil
				}
				// .spec.namespacedMultiClusterService is namespace/name - see
				// NamespacedMultiClusterService.GetFullname, which is what writes it.
				namespace, name, ok := strings.Cut(serviceSet.Spec.NamespacedMultiClusterService, "/")
				if !ok {
					return nil, nil
				}
				nmcsKey := client.ObjectKey{Namespace: namespace, Name: name}
				nmcs := new(kcmv1.NamespacedMultiClusterService)
				if err := r.Client.Get(ctx, nmcsKey, nmcs); err != nil {
					if apierrors.IsNotFound(err) {
						return nil, nil
					}
					return nil, fmt.Errorf("failed to get NamespacedMultiClusterService %s: %w", nmcsKey, err)
				}
				return []ctrl.Request{{NamespacedName: client.ObjectKeyFromObject(nmcs)}}, nil
			}),
		)

	if r.IsDisabledValidationWH {
		// A NamespacedMultiClusterService only ever references ServiceTemplates in its own
		// namespace - see validateSpec in the MultiClusterService controller - so scoping this
		// List to the ServiceTemplate's own namespace is required for correctness here, unlike
		// the equivalent MultiClusterService watch where the object being listed is cluster-scoped.
		watchServiceTemplates(mgr, managedController, "namespacedmulticlusterservice_ctrl_setup",
			func(ctx context.Context, namespace, templateName string) ([]ctrl.Request, error) {
				nmcss := new(kcmv1.NamespacedMultiClusterServiceList)
				if err := mgr.GetClient().List(ctx, nmcss, client.InNamespace(namespace), client.MatchingFields{kcmv1.NamespacedMultiClusterServiceTemplatesIndexKey: templateName}); err != nil {
					return nil, fmt.Errorf("failed to list NamespacedMultiClusterServices by ServiceTemplate %s: %w", templateName, err)
				}

				resp := make([]ctrl.Request, 0, len(nmcss.Items))
				for _, v := range nmcss.Items {
					resp = append(resp, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&v)})
				}

				return resp, nil
			})
	}

	return managedController.Complete(r)
}
