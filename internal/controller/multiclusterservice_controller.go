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
	"errors"
	"fmt"
	"slices"

	sveltosv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	sveltoscontrollers "github.com/projectsveltos/addon-controller/controllers"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	hmc "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/sveltos"
	"github.com/K0rdent/kcm/internal/utils"
)

// MultiClusterServiceReconciler reconciles a MultiClusterService object
type MultiClusterServiceReconciler struct {
	client.Client
	SystemNamespace string
}

// Reconcile reconciles a MultiClusterService object.
func (r *MultiClusterServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling MultiClusterService")

	mcs := &hmc.MultiClusterService{}
	err := r.Get(ctx, req.NamespacedName, mcs)
	if apierrors.IsNotFound(err) {
		l.Info("MultiClusterService not found, ignoring since object must be deleted")
		return ctrl.Result{}, nil
	}
	if err != nil {
		l.Error(err, "Failed to get MultiClusterService")
		return ctrl.Result{}, err
	}

	if !mcs.DeletionTimestamp.IsZero() {
		l.Info("Deleting MultiClusterService")
		return r.reconcileDelete(ctx, mcs)
	}

	return r.reconcileUpdate(ctx, mcs)
}

func (r *MultiClusterServiceReconciler) reconcileUpdate(ctx context.Context, mcs *hmc.MultiClusterService) (_ ctrl.Result, err error) {
	if utils.AddLabel(mcs, hmc.GenericComponentLabelName, hmc.GenericComponentLabelValueHMC) {
		if err := r.Update(ctx, mcs); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update labels: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil // generation has not changed, need explicit requeue
	}

	// servicesErr is handled separately from err because we do not want
	// to set the condition of SveltosClusterProfileReady type to "False"
	// if there is an error while retrieving status for the services.
	var servicesErr error

	defer func() {
		condition := metav1.Condition{
			Reason: hmc.SucceededReason,
			Status: metav1.ConditionTrue,
			Type:   hmc.SveltosClusterProfileReadyCondition,
		}
		if err != nil {
			condition.Message = err.Error()
			condition.Reason = hmc.FailedReason
			condition.Status = metav1.ConditionFalse
		}
		apimeta.SetStatusCondition(&mcs.Status.Conditions, condition)

		servicesCondition := metav1.Condition{
			Reason: hmc.SucceededReason,
			Status: metav1.ConditionTrue,
			Type:   hmc.FetchServicesStatusSuccessCondition,
		}
		if servicesErr != nil {
			servicesCondition.Message = servicesErr.Error()
			servicesCondition.Reason = hmc.FailedReason
			servicesCondition.Status = metav1.ConditionFalse
		}
		apimeta.SetStatusCondition(&mcs.Status.Conditions, servicesCondition)

		err = errors.Join(err, servicesErr, r.updateStatus(ctx, mcs))
	}()

	if controllerutil.AddFinalizer(mcs, hmc.MultiClusterServiceFinalizer) {
		if err = r.Client.Update(ctx, mcs); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update MultiClusterService %s with finalizer %s: %w", mcs.Name, hmc.MultiClusterServiceFinalizer, err)
		}
		// Requeuing to make sure that ClusterProfile is reconciled in subsequent runs.
		// Without the requeue, we would be depending on an external re-trigger after
		// the 1st run for the ClusterProfile object to be reconciled.
		return ctrl.Result{Requeue: true}, nil
	}

	// We are enforcing that MultiClusterService may only use
	// ServiceTemplates that are present in the system namespace.
	opts, err := sveltos.GetHelmChartOpts(ctx, r.Client, r.SystemNamespace, mcs.Spec.Services)
	if err != nil {
		return ctrl.Result{}, err
	}

	if _, err = sveltos.ReconcileClusterProfile(ctx, r.Client, mcs.Name,
		sveltos.ReconcileProfileOpts{
			OwnerReference: &metav1.OwnerReference{
				APIVersion: hmc.GroupVersion.String(),
				Kind:       hmc.MultiClusterServiceKind,
				Name:       mcs.Name,
				UID:        mcs.UID,
			},
			LabelSelector:  mcs.Spec.ClusterSelector,
			HelmChartOpts:  opts,
			Priority:       mcs.Spec.ServicesPriority,
			StopOnConflict: mcs.Spec.StopOnConflict,
		}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile ClusterProfile: %w", err)
	}

	// NOTE:
	// We are returning nil in the return statements whenever servicesErr != nil
	// because we don't want the error content in servicesErr to be assigned to err.
	// The servicesErr var is joined with err in the defer func() so this function
	// will ultimately return the error in servicesErr instead of nil.
	profile := sveltosv1beta1.ClusterProfile{}
	profileRef := client.ObjectKey{Name: mcs.Name}
	if servicesErr = r.Get(ctx, profileRef, &profile); servicesErr != nil {
		servicesErr = fmt.Errorf("failed to get ClusterProfile %s to fetch status from its associated ClusterSummary: %w", profileRef.String(), servicesErr)
		return ctrl.Result{}, nil
	}

	var servicesStatus []hmc.ServiceStatus
	servicesStatus, servicesErr = updateServicesStatus(ctx, r.Client, profileRef, profile.Status.MatchingClusterRefs, mcs.Status.Services)
	if servicesErr != nil {
		return ctrl.Result{}, nil
	}
	mcs.Status.Services = servicesStatus

	return ctrl.Result{}, nil
}

// updateStatus updates the status for the MultiClusterService object.
func (r *MultiClusterServiceReconciler) updateStatus(ctx context.Context, mcs *hmc.MultiClusterService) error {
	mcs.Status.ObservedGeneration = mcs.Generation
	mcs.Status.Conditions = updateStatusConditions(mcs.Status.Conditions, "MultiClusterService is ready")

	if err := r.Status().Update(ctx, mcs); err != nil {
		return fmt.Errorf("failed to update status for MultiClusterService %s/%s: %w", mcs.Namespace, mcs.Name, err)
	}

	return nil
}

// updateStatusConditions evaluates all provided conditions and returns them
// after setting a new condition based on the status of the provided ones.
func updateStatusConditions(conditions []metav1.Condition, readyMsg string) []metav1.Condition {
	warnings := ""
	errs := ""

	for _, condition := range conditions {
		if condition.Type == hmc.ReadyCondition {
			continue
		}
		if condition.Status == metav1.ConditionUnknown {
			warnings += condition.Message + ". "
		}
		if condition.Status == metav1.ConditionFalse {
			errs += condition.Message + ". "
		}
	}

	condition := metav1.Condition{
		Type:    hmc.ReadyCondition,
		Status:  metav1.ConditionTrue,
		Reason:  hmc.SucceededReason,
		Message: readyMsg,
	}

	if warnings != "" {
		condition.Status = metav1.ConditionUnknown
		condition.Reason = hmc.ProgressingReason
		condition.Message = warnings
	}
	if errs != "" {
		condition.Status = metav1.ConditionFalse
		condition.Reason = hmc.FailedReason
		condition.Message = errs
	}

	apimeta.SetStatusCondition(&conditions, condition)
	return conditions
}

// updateServicesStatus updates the services deployment status.
func updateServicesStatus(ctx context.Context, c client.Client, profileRef client.ObjectKey, profileStatusMatchingClusterRefs []corev1.ObjectReference, servicesStatus []hmc.ServiceStatus) ([]hmc.ServiceStatus, error) {
	profileKind := sveltosv1beta1.ProfileKind
	if profileRef.Namespace == "" {
		profileKind = sveltosv1beta1.ClusterProfileKind
	}

	for _, obj := range profileStatusMatchingClusterRefs {
		isSveltosCluster := obj.APIVersion == libsveltosv1beta1.GroupVersion.String()
		summaryName := sveltoscontrollers.GetClusterSummaryName(profileKind, profileRef.Name, obj.Name, isSveltosCluster)

		summary := sveltosv1beta1.ClusterSummary{}
		summaryRef := client.ObjectKey{Name: summaryName, Namespace: obj.Namespace}
		if err := c.Get(ctx, summaryRef, &summary); err != nil {
			return nil, fmt.Errorf("failed to get ClusterSummary %s to fetch status: %w", summaryRef.String(), err)
		}

		idx := slices.IndexFunc(servicesStatus, func(o hmc.ServiceStatus) bool {
			return obj.Name == o.ClusterName && obj.Namespace == o.ClusterNamespace
		})

		if idx < 0 {
			servicesStatus = append(servicesStatus, hmc.ServiceStatus{
				ClusterName:      obj.Name,
				ClusterNamespace: obj.Namespace,
			})
			idx = len(servicesStatus) - 1
		}

		conditions, err := sveltos.GetStatusConditions(&summary)
		if err != nil {
			return nil, err
		}

		// We are overwriting conditions so as to be in-sync with the custom status
		// implemented by Sveltos ClusterSummary object. E.g. If a service has been
		// removed, the ClusterSummary status will not show that service, therefore
		// we also want the entry for that service to be removed from conditions.
		servicesStatus[idx].Conditions = conditions
	}

	return servicesStatus, nil
}

func (r *MultiClusterServiceReconciler) reconcileDelete(ctx context.Context, mcsvc *hmc.MultiClusterService) (ctrl.Result, error) {
	if err := sveltos.DeleteClusterProfile(ctx, r.Client, mcsvc.Name); err != nil {
		return ctrl.Result{}, err
	}

	if controllerutil.RemoveFinalizer(mcsvc, hmc.MultiClusterServiceFinalizer) {
		if err := r.Client.Update(ctx, mcsvc); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer %s from MultiClusterService %s: %w", hmc.MultiClusterServiceFinalizer, mcsvc.Name, err)
		}
	}

	return ctrl.Result{}, nil
}

// requeueSveltosProfileForClusterSummary asserts that the requested object has Sveltos ClusterSummary
// type, fetches its owner (a Sveltos Profile or ClusterProfile object), and requeues its reference.
// When used with ClusterDeploymentReconciler or MultiClusterServiceReconciler, this effectively
// requeues a ClusterDeployment or MultiClusterService object as these are referenced by the same
// namespace/name as the Sveltos Profile or ClusterProfile object that they create respectively.
func requeueSveltosProfileForClusterSummary(ctx context.Context, obj client.Object) []ctrl.Request {
	l := ctrl.LoggerFrom(ctx)
	msg := "cannot queue request"

	cs, ok := obj.(*sveltosv1beta1.ClusterSummary)
	if !ok {
		l.Error(errors.New("request is not for a ClusterSummary object"), msg, "Requested.Name", obj.GetName(), "Requested.Namespace", obj.GetNamespace())
		return []ctrl.Request{}
	}

	ownerRef, err := sveltosv1beta1.GetProfileOwnerReference(cs)
	if err != nil {
		l.Error(err, msg, "ClusterSummary.Name", obj.GetName(), "ClusterSummary.Namespace", obj.GetNamespace())
		return []ctrl.Request{}
	}

	// The Profile/ClusterProfile object has the same name as its
	// owner object which is either ClusterDeployment or MultiClusterService.
	req := client.ObjectKey{Name: ownerRef.Name}
	if ownerRef.Kind == sveltosv1beta1.ProfileKind {
		req.Namespace = obj.GetNamespace()
	}

	return []ctrl.Request{{NamespacedName: req}}
}

// SetupWithManager sets up the controller with the Manager.
func (r *MultiClusterServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hmc.MultiClusterService{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&sveltosv1beta1.ClusterSummary{},
			handler.EnqueueRequestsFromMapFunc(requeueSveltosProfileForClusterSummary),
			builder.WithPredicates(predicate.Funcs{
				DeleteFunc:  func(event.DeleteEvent) bool { return false },
				GenericFunc: func(event.GenericEvent) bool { return false },
			}),
		).
		Complete(r)
}
