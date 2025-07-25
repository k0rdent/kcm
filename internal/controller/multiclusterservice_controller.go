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
	"strings"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	sveltoscontrollers "github.com/projectsveltos/addon-controller/controllers"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/metrics"
	"github.com/K0rdent/kcm/internal/record"
	"github.com/K0rdent/kcm/internal/sveltos"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
	"github.com/K0rdent/kcm/internal/utils/validation"
)

// MultiClusterServiceReconciler reconciles a MultiClusterService object
type MultiClusterServiceReconciler struct {
	Client                 client.Client
	SystemNamespace        string
	IsDisabledValidationWH bool // is webhook disabled set via the controller flags
}

// Reconcile reconciles a MultiClusterService object.
func (r *MultiClusterServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling MultiClusterService")

	mcs := &kcmv1.MultiClusterService{}
	err := r.Client.Get(ctx, req.NamespacedName, mcs)
	if apierrors.IsNotFound(err) {
		l.Info("MultiClusterService not found, ignoring since object must be deleted")
		return ctrl.Result{}, nil
	}
	if err != nil {
		l.Error(err, "Failed to get MultiClusterService")
		return ctrl.Result{}, err
	}

	if !mcs.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, mcs)
	}

	management := &kcmv1.Management{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, management); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Management: %w", err)
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping MultiClusterService reconciliation")
		return ctrl.Result{}, nil
	}

	return r.reconcileUpdate(ctx, mcs)
}

func (*MultiClusterServiceReconciler) initServicesConditions(mcs *kcmv1.MultiClusterService) (changed bool) {
	for _, typ := range [3]string{kcmv1.SveltosClusterProfileReadyCondition, kcmv1.FetchServicesStatusSuccessCondition, kcmv1.ServicesReferencesValidationCondition} {
		// Skip initialization if the condition already exists.
		// This ensures we don't overwrite an existing condition and can accurately detect actual
		// conditions changes later.
		if apimeta.FindStatusCondition(mcs.Status.Conditions, typ) != nil {
			continue
		}
		if apimeta.SetStatusCondition(&mcs.Status.Conditions, metav1.Condition{
			Type:               typ,
			Status:             metav1.ConditionUnknown,
			Reason:             kcmv1.ProgressingReason,
			ObservedGeneration: mcs.Generation,
		}) {
			changed = true
		}
	}

	return changed
}

func (*MultiClusterServiceReconciler) setCondition(mcs *kcmv1.MultiClusterService, typ string, err error) (changed bool) {
	reason, cstatus, msg := kcmv1.SucceededReason, metav1.ConditionTrue, ""
	if err != nil {
		reason, cstatus, msg = kcmv1.FailedReason, metav1.ConditionFalse, err.Error()
	}

	return apimeta.SetStatusCondition(&mcs.Status.Conditions, metav1.Condition{
		Type:    typ,
		Status:  cstatus,
		Reason:  reason,
		Message: msg,
	})
}

func (r *MultiClusterServiceReconciler) reconcileUpdate(ctx context.Context, mcs *kcmv1.MultiClusterService) (_ ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)

	if controllerutil.AddFinalizer(mcs, kcmv1.MultiClusterServiceFinalizer) {
		if err = r.Client.Update(ctx, mcs); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update MultiClusterService %s with finalizer %s: %w", mcs.Name, kcmv1.MultiClusterServiceFinalizer, err)
		}
		// Requeuing to make sure that ClusterProfile is reconciled in subsequent runs.
		// Without the requeue, we would be depending on an external re-trigger after
		// the 1st run for the ClusterProfile object to be reconciled.
		return ctrl.Result{Requeue: true}, nil
	}

	if updated, err := utils.AddKCMComponentLabel(ctx, r.Client, mcs); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{Requeue: true}, err // generation has not changed, need explicit requeue
	}

	r.initServicesConditions(mcs)

	if r.IsDisabledValidationWH {
		if err := validation.ServicesHaveValidTemplates(ctx, r.Client, mcs.Spec.ServiceSpec.Services, r.SystemNamespace); err != nil {
			if r.setCondition(mcs, kcmv1.ServicesReferencesValidationCondition, err) {
				r.warnf(mcs, kcmv1.ServicesReferencesValidationFailedReason, err.Error())
			}

			l.Error(err, "failed to validate services reference valid ServiceTemplates, will not retrigger this error")
			return ctrl.Result{}, r.updateStatus(ctx, mcs) // no reason to reconcile further
		}
	}

	if r.setCondition(mcs, kcmv1.ServicesReferencesValidationCondition, nil) { // if wh is enabled just set it ok, otherwise it succeeded
		r.eventf(mcs, kcmv1.ServicesReferencesValidationSucceededReason, "Successfully validated services references")
	}

	// servicesErr is handled separately from err because we do not want
	// to set the condition of SveltosClusterProfileReady type to "False"
	// if there is an error while retrieving status for the services.
	var servicesErr error

	// TODO: should be refactored, unmaintainable; requires to refactor the whole conditions-approach (e.g. init on each event); requires to refactor controllers to be moved to dedicated pkgs
	defer func() {
		if r.setCondition(mcs, kcmv1.SveltosClusterProfileReadyCondition, err) {
			if err != nil {
				r.warnf(mcs, kcmv1.SveltosClusterProfileNotReadyReason, err.Error())
			} else {
				r.eventf(mcs, kcmv1.SveltosClusterProfileReadyCondition, "Successfully reconciled %s ClusterProfile", mcs.Name)
			}
		}

		if r.setCondition(mcs, kcmv1.FetchServicesStatusSuccessCondition, servicesErr) {
			if servicesErr != nil {
				r.warnf(mcs, kcmv1.FetchServicesStatusFailedReason, servicesErr.Error())
			} else {
				r.eventf(mcs, kcmv1.FetchServicesStatusSuccessCondition, "Successfully fetched status of services from Sveltos ClusterSummaries")
			}
		}

		if r.IsDisabledValidationWH && apierrors.IsNotFound(err) {
			// non-services NotFound errors relate only to ServiceTemplate
			// if they are gone then nothing to do
			err = nil
		}
		err = errors.Join(err, servicesErr, r.updateStatus(ctx, mcs))
	}()

	// we need to validate desired services state against the observed state and available upgrade paths
	if err = validation.ValidateUpgradePaths(mcs.Spec.ServiceSpec.Services, mcs.Status.ServicesUpgradePaths); err != nil {
		return ctrl.Result{}, err
	}

	// We are enforcing that MultiClusterService may only use
	// ServiceTemplates that are present in the system namespace.
	helmCharts, err := sveltos.GetHelmCharts(ctx, r.Client, r.SystemNamespace, mcs.Spec.ServiceSpec.Services)
	if err != nil {
		return ctrl.Result{}, err
	}
	kustomizationRefs, err := sveltos.GetKustomizationRefs(ctx, r.Client, r.SystemNamespace, mcs.Spec.ServiceSpec.Services)
	if err != nil {
		return ctrl.Result{}, err
	}
	policyRefs, err := sveltos.GetPolicyRefs(ctx, r.Client, r.SystemNamespace, mcs.Spec.ServiceSpec.Services)
	if err != nil {
		return ctrl.Result{}, err
	}

	if _, err = sveltos.ReconcileClusterProfile(ctx, r.Client, mcs.Name,
		sveltos.ReconcileProfileOpts{
			OwnerReference: &metav1.OwnerReference{
				APIVersion: kcmv1.GroupVersion.String(),
				Kind:       kcmv1.MultiClusterServiceKind,
				Name:       mcs.Name,
				UID:        mcs.UID,
			},
			LabelSelector:        mcs.Spec.ClusterSelector,
			HelmCharts:           helmCharts,
			KustomizationRefs:    kustomizationRefs,
			PolicyRefs:           policyRefs,
			Priority:             mcs.Spec.ServiceSpec.Priority,
			StopOnConflict:       mcs.Spec.ServiceSpec.StopOnConflict,
			Reload:               mcs.Spec.ServiceSpec.Reload,
			TemplateResourceRefs: mcs.Spec.ServiceSpec.TemplateResourceRefs,
			SyncMode:             mcs.Spec.ServiceSpec.SyncMode,
			DriftIgnore:          mcs.Spec.ServiceSpec.DriftIgnore,
			DriftExclusions:      mcs.Spec.ServiceSpec.DriftExclusions,
			ContinueOnError:      mcs.Spec.ServiceSpec.ContinueOnError,
		}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile ClusterProfile: %w", err)
	}

	for _, svc := range mcs.Spec.ServiceSpec.Services {
		metrics.TrackMetricTemplateUsage(ctx, kcmv1.ServiceTemplateKind, svc.Template, kcmv1.MultiClusterServiceKind, mcs.ObjectMeta, true)
	}

	// NOTE:
	// We are returning nil in the return statements whenever servicesErr != nil
	// because we don't want the error content in servicesErr to be assigned to err.
	// The servicesErr var is joined with err in the defer func() so this function
	// will ultimately return the error in servicesErr instead of nil.
	profile := addoncontrollerv1beta1.ClusterProfile{}
	profileRef := client.ObjectKey{Name: mcs.Name}
	if servicesErr = r.Client.Get(ctx, profileRef, &profile); servicesErr != nil {
		servicesErr = fmt.Errorf("failed to get ClusterProfile %s to fetch status from its associated ClusterSummary: %w", profileRef.String(), servicesErr)
		return ctrl.Result{}, nil
	}

	if len(mcs.Spec.ServiceSpec.Services) == 0 {
		mcs.Status.Services = nil
		return ctrl.Result{}, nil
	}

	servicesStatus, servicesErr := getServicesStatus(ctx, r.Client, profileRef, profile.Status.MatchingClusterRefs)
	if servicesErr != nil {
		return ctrl.Result{}, nil
	}

	// Running this loop for the sole purpose of creating
	// a kubernetes event for each change in conditions.
	for _, svc := range servicesStatus {
		idx := slices.IndexFunc(mcs.Status.Services, func(o kcmv1.ServiceStatus) bool {
			return svc.ClusterNamespace == o.ClusterNamespace && svc.ClusterName == o.ClusterName
		})

		for _, cond := range svc.Conditions {
			if idx > -1 && apimeta.SetStatusCondition(&mcs.Status.Services[idx].Conditions, cond) {
				sveltos.CreateEventFromCondition(mcs, mcs.Generation, client.ObjectKey{Namespace: svc.ClusterNamespace, Name: svc.ClusterName}, &cond)
			}
			// If idx == -1, then a new service was added to the mcs spec so we should create an event in this case.
			sveltos.CreateEventFromCondition(mcs, mcs.Generation, client.ObjectKey{Namespace: svc.ClusterNamespace, Name: svc.ClusterName}, &cond)
		}
	}

	// We are overwriting conditions so as to be in-sync with the custom status
	// implemented by Sveltos ClusterSummary object. E.g. If a service has been
	// removed, the ClusterSummary status will not show that service, therefore
	// we also want the entry for that service to be removed from conditions.
	mcs.Status.Services = servicesStatus
	l.Info("Successfully updated status of services")
	var servicesUpgradePaths []kcmv1.ServiceUpgradePaths
	servicesUpgradePaths, servicesErr = updateServicesUpgradePaths(ctx, r.Client, mcs.Spec.ServiceSpec.Services, r.SystemNamespace)
	mcs.Status.ServicesUpgradePaths = servicesUpgradePaths
	return ctrl.Result{}, nil
}

// updateStatus updates the status for the MultiClusterService object.
func (r *MultiClusterServiceReconciler) updateStatus(ctx context.Context, mcs *kcmv1.MultiClusterService) error {
	if err := r.setClustersServicesReadinessConditions(ctx, mcs); err != nil {
		return fmt.Errorf("failed to set clusters and services readiness conditions: %w", err)
	}

	mcs.Status.ObservedGeneration = mcs.Generation
	mcs.Status.Conditions = updateStatusConditions(mcs.Status.Conditions)

	if err := r.Client.Status().Update(ctx, mcs); err != nil {
		return fmt.Errorf("failed to update status for MultiClusterService %s/%s: %w", mcs.Namespace, mcs.Name, err)
	}

	return nil
}

// setClustersServicesReadinessConditions calculates and sets
// [github.com/K0rdent/kcm/api/v1beta1.ServicesInReadyStateCondition] and
// [github.com/K0rdent/kcm/api/v1beta1.ClusterInReadyStateCondition]
// informational conditions with the number of ready services and clusters.
func (r *MultiClusterServiceReconciler) setClustersServicesReadinessConditions(ctx context.Context, mcs *kcmv1.MultiClusterService) error {
	sel, err := metav1.LabelSelectorAsSelector(&mcs.Spec.ClusterSelector)
	if err != nil {
		return fmt.Errorf("failed to construct selector from MultiClusterService %s selector: %w", client.ObjectKeyFromObject(mcs), err)
	}

	capiClusters := &kcmv1.ClusterDeploymentList{}
	if err := r.Client.List(ctx, capiClusters, client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return fmt.Errorf("failed to list CAPI Clusters: %w", err)
	}

	ready := 0
	for _, cluster := range capiClusters.Items {
		rc := apimeta.FindStatusCondition(cluster.Status.Conditions, kcmv1.ReadyCondition)
		if rc != nil && rc.Status == metav1.ConditionTrue {
			ready++
		}
	}

	sveltosClusters := &libsveltosv1beta1.SveltosClusterList{}
	if err := r.Client.List(ctx, sveltosClusters, client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return fmt.Errorf("failed to list SveltosClusters: %w", err)
	}

	for _, cluster := range sveltosClusters.Items {
		if cluster.Status.Ready {
			ready++
		}
	}

	desiredClusters := len(capiClusters.Items) + len(sveltosClusters.Items)
	desiredServices := desiredClusters * len(mcs.Spec.ServiceSpec.Services)
	c := metav1.Condition{
		Type:    kcmv1.ClusterInReadyStateCondition,
		Status:  metav1.ConditionTrue,
		Reason:  kcmv1.SucceededReason,
		Message: fmt.Sprintf("%d/%d", ready, desiredClusters),
	}
	if ready != desiredClusters {
		c.Reason = kcmv1.ProgressingReason
		c.Status = metav1.ConditionFalse
	}

	apimeta.SetStatusCondition(&mcs.Status.Conditions, c)
	apimeta.SetStatusCondition(&mcs.Status.Conditions, getServicesReadinessCondition(mcs.Status.Services, desiredServices))

	return nil
}

func getServicesReadinessCondition(serviceStatuses []kcmv1.ServiceStatus, desiredServices int) metav1.Condition {
	ready := 0
	for _, svcstatus := range serviceStatuses {
		for _, c := range svcstatus.Conditions {
			if strings.HasSuffix(c.Type, kcmv1.SveltosHelmReleaseReadyCondition) && c.Status == metav1.ConditionTrue {
				ready++
			}
		}
	}

	// NOTE: if desired < ready we still want to show this, because some of services might be in removal process
	// WARN: at the moment complete service removal is not being handled at all
	c := metav1.Condition{
		Type:    kcmv1.ServicesInReadyStateCondition,
		Status:  metav1.ConditionTrue,
		Reason:  kcmv1.SucceededReason,
		Message: fmt.Sprintf("%d/%d", ready, desiredServices),
	}
	if ready != desiredServices {
		c.Reason = kcmv1.ProgressingReason
		c.Status = metav1.ConditionFalse
		// FIXME: remove the kludge after handling of services removal is done
		if desiredServices < ready {
			c.Reason = kcmv1.SucceededReason
			c.Status = metav1.ConditionTrue
			c.Message = fmt.Sprintf("%d/%d", ready, ready)
		}
	}

	return c
}

// updateStatusConditions evaluates all provided conditions and returns them
// after setting a new condition based on the status of the provided ones.
func updateStatusConditions(conditions []metav1.Condition) []metav1.Condition {
	var warnings, errs strings.Builder

	condition := metav1.Condition{
		Type:    kcmv1.ReadyCondition,
		Status:  metav1.ConditionTrue,
		Reason:  kcmv1.SucceededReason,
		Message: "Object is ready",
	}

	defer func() {
		apimeta.SetStatusCondition(&conditions, condition)
	}()

	idx := slices.IndexFunc(conditions, func(c metav1.Condition) bool {
		return c.Type == kcmv1.DeletingCondition
	})
	if idx >= 0 {
		condition.Status = conditions[idx].Status
		condition.Reason = conditions[idx].Reason
		condition.Message = conditions[idx].Message
		return conditions
	}

	for _, cond := range conditions {
		if cond.Type == kcmv1.ReadyCondition {
			continue
		}
		if cond.Status == metav1.ConditionUnknown {
			_, _ = warnings.WriteString(cond.Message + ". ")
		}
		if cond.Status == metav1.ConditionFalse {
			switch cond.Type {
			case kcmv1.ClusterInReadyStateCondition:
				_, _ = errs.WriteString(cond.Message + " Clusters are ready. ")
			case kcmv1.ServicesInReadyStateCondition:
				_, _ = errs.WriteString(cond.Message + " Services are ready. ")
			default:
				_, _ = errs.WriteString(cond.Message + ". ")
			}
		}
	}

	if warnings.Len() > 0 {
		condition.Status = metav1.ConditionUnknown
		condition.Reason = kcmv1.ProgressingReason
		condition.Message = strings.TrimSuffix(warnings.String(), ". ")
	}
	if errs.Len() > 0 {
		condition.Status = metav1.ConditionFalse
		condition.Reason = kcmv1.FailedReason
		condition.Message = strings.TrimSuffix(errs.String(), ". ")
	}

	return conditions
}

// getServicesStatus gets the services deployment status.
func getServicesStatus(
	ctx context.Context,
	c client.Client,
	profileRef client.ObjectKey,
	profileStatusMatchingClusterRefs []corev1.ObjectReference,
) ([]kcmv1.ServiceStatus, error) {
	profileKind := addoncontrollerv1beta1.ProfileKind
	if profileRef.Namespace == "" {
		profileKind = addoncontrollerv1beta1.ClusterProfileKind
	}

	servicesStatus := make([]kcmv1.ServiceStatus, len(profileStatusMatchingClusterRefs))

	for i, obj := range profileStatusMatchingClusterRefs {
		isSveltosCluster := obj.APIVersion == libsveltosv1beta1.GroupVersion.String()
		summaryName := sveltoscontrollers.GetClusterSummaryName(profileKind, profileRef.Name, obj.Name, isSveltosCluster)

		summary := addoncontrollerv1beta1.ClusterSummary{}
		summaryRef := client.ObjectKey{Name: summaryName, Namespace: obj.Namespace}
		if err := c.Get(ctx, summaryRef, &summary); err != nil {
			return nil, fmt.Errorf("failed to get ClusterSummary %s to fetch status: %w", summaryRef.String(), err)
		}

		status := kcmv1.ServiceStatus{
			ClusterName:      obj.Name,
			ClusterNamespace: obj.Namespace,
		}

		conditions, err := sveltos.GetStatusConditions(&summary)
		if err != nil {
			return nil, err
		}

		status.Conditions = conditions
		servicesStatus[i] = status
	}

	return servicesStatus, nil
}

func updateServicesUpgradePaths(
	ctx context.Context,
	c client.Client,
	services []kcmv1.Service,
	namespace string,
) ([]kcmv1.ServiceUpgradePaths, error) {
	var errs error
	servicesUpgradePaths := make([]kcmv1.ServiceUpgradePaths, 0, len(services))
	for _, svc := range services {
		serviceNamespace := svc.Namespace
		if serviceNamespace == "" {
			serviceNamespace = metav1.NamespaceDefault
		}
		serviceUpgradePaths := kcmv1.ServiceUpgradePaths{
			Name:      svc.Name,
			Namespace: serviceNamespace,
			Template:  svc.Template,
		}
		if svc.TemplateChain == "" {
			servicesUpgradePaths = append(servicesUpgradePaths, serviceUpgradePaths)
			continue
		}
		serviceTemplateChain := new(kcmv1.ServiceTemplateChain)
		key := client.ObjectKey{Name: svc.TemplateChain, Namespace: namespace}
		if err := c.Get(ctx, key, serviceTemplateChain); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to get ServiceTemplateChain %s to fetch upgrade paths: %w", key.String(), err))
			continue
		}
		upgradePaths, err := serviceTemplateChain.Spec.UpgradePaths(svc.Template)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to get upgrade paths for ServiceTemplate %s: %w", svc.Template, err))
			continue
		}
		serviceUpgradePaths.AvailableUpgrades = upgradePaths
		servicesUpgradePaths = append(servicesUpgradePaths, serviceUpgradePaths)
	}
	return servicesUpgradePaths, errs
}

func (r *MultiClusterServiceReconciler) reconcileDelete(ctx context.Context, mcs *kcmv1.MultiClusterService) (result ctrl.Result, err error) {
	ctrl.LoggerFrom(ctx).Info("Deleting MultiClusterService")

	defer func() {
		if err == nil {
			for _, svc := range mcs.Spec.ServiceSpec.Services {
				metrics.TrackMetricTemplateUsage(ctx, kcmv1.ServiceTemplateKind, svc.Template, kcmv1.MultiClusterServiceKind, mcs.ObjectMeta, false)
			}
		}
	}()

	if err := sveltos.DeleteClusterProfile(ctx, r.Client, mcs.Name); err != nil {
		return ctrl.Result{}, err
	}

	if controllerutil.RemoveFinalizer(mcs, kcmv1.MultiClusterServiceFinalizer) {
		if err := r.Client.Update(ctx, mcs); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer %s from MultiClusterService %s: %w", kcmv1.MultiClusterServiceFinalizer, mcs.Name, err)
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

	cs, ok := obj.(*addoncontrollerv1beta1.ClusterSummary)
	if !ok {
		l.Error(errors.New("request is not for a ClusterSummary object"), msg, "Requested.Name", obj.GetName(), "Requested.Namespace", obj.GetNamespace())
		return []ctrl.Request{}
	}

	ownerRef, err := addoncontrollerv1beta1.GetProfileOwnerReference(cs)
	if err != nil {
		l.Error(err, msg, "ClusterSummary.Name", obj.GetName(), "ClusterSummary.Namespace", obj.GetNamespace())
		return []ctrl.Request{}
	}

	// The Profile/ClusterProfile object has the same name as its
	// owner object which is either ClusterDeployment or MultiClusterService.
	req := client.ObjectKey{Name: ownerRef.Name}
	if ownerRef.Kind == addoncontrollerv1beta1.ProfileKind {
		req.Namespace = obj.GetNamespace()
	}

	return []ctrl.Request{{NamespacedName: req}}
}

// SetupWithManager sets up the controller with the Manager.
func (r *MultiClusterServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()

	managedController := ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcmv1.MultiClusterService{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&addoncontrollerv1beta1.ClusterSummary{},
			handler.EnqueueRequestsFromMapFunc(requeueSveltosProfileForClusterSummary),
			builder.WithPredicates(predicate.Funcs{
				DeleteFunc:  func(event.DeleteEvent) bool { return false },
				GenericFunc: func(event.GenericEvent) bool { return false },
			}),
		)

	if r.IsDisabledValidationWH {
		managedController.Watches(&kcmv1.ServiceTemplate{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
			mcss := new(kcmv1.MultiClusterServiceList)
			if err := mgr.GetClient().List(ctx, mcss, client.InNamespace(o.GetNamespace()), client.MatchingFields{kcmv1.MultiClusterServiceTemplatesIndexKey: o.GetName()}); err != nil {
				return nil
			}

			resp := make([]ctrl.Request, 0, len(mcss.Items))
			for _, v := range mcss.Items {
				resp = append(resp, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&v)})
			}

			return resp
		}), builder.WithPredicates(predicate.Funcs{
			GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
			DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
			UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
				sto, ok := tue.ObjectOld.(*kcmv1.ServiceTemplate)
				if !ok {
					return false
				}
				stn, ok := tue.ObjectNew.(*kcmv1.ServiceTemplate)
				if !ok {
					return false
				}
				return stn.Status.Valid && !sto.Status.Valid
			},
		}))
		mgr.GetLogger().WithName("multiclusterservice_ctrl_setup").Info("Validations are disabled, watcher for ServiceTemplate objects is set")
	}

	return managedController.Complete(r)
}

func (*MultiClusterServiceReconciler) eventf(mcs *kcmv1.MultiClusterService, reason, message string, args ...any) {
	record.Eventf(mcs, mcs.Generation, reason, message, args...)
}

func (*MultiClusterServiceReconciler) warnf(mcs *kcmv1.MultiClusterService, reason, message string, args ...any) {
	record.Warnf(mcs, mcs.Generation, reason, message, args...)
}
