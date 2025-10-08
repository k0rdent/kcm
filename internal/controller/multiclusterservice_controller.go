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
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
	"github.com/K0rdent/kcm/internal/serviceset"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
	"github.com/K0rdent/kcm/internal/utils/validation"
)

// MultiClusterServiceReconciler reconciles a MultiClusterService object
type MultiClusterServiceReconciler struct {
	Client                 client.Client
	SystemNamespace        string
	IsDisabledValidationWH bool // is webhook disabled set via the controller flags

	defaultRequeueTime time.Duration
}

// Reconcile reconciles a MultiClusterService object.
func (r *MultiClusterServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling MultiClusterService")

	mcs := &kcmv1.MultiClusterService{}
	err = r.Client.Get(ctx, req.NamespacedName, mcs)
	if apierrors.IsNotFound(err) {
		l.Info("MultiClusterService not found, ignoring since object must be deleted")
		return ctrl.Result{}, nil
	}
	if err != nil {
		l.Error(err, "Failed to get MultiClusterService")
		return ctrl.Result{}, err
	}

	clone := mcs.DeepCopy()
	defer func() {
		// we need to explicitly requeue MultiClusterService object,
		// otherwise we'll miss if some ClusterDeployment will be updated
		// with matching labels.
		var requeue bool
		if requeue, err = r.updateStatus(ctx, clone, mcs); requeue {
			result = ctrl.Result{RequeueAfter: r.defaultRequeueTime}
		}
	}()

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

func (r *MultiClusterServiceReconciler) reconcileUpdate(ctx context.Context, mcs *kcmv1.MultiClusterService) (result ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	if controllerutil.AddFinalizer(mcs, kcmv1.MultiClusterServiceFinalizer) {
		if err = r.Client.Update(ctx, mcs); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update MultiClusterService %s with finalizer %s: %w", mcs.Name, kcmv1.MultiClusterServiceFinalizer, err)
		}
		// Requeuing to make sure that ClusterProfile is reconciled in subsequent runs.
		// Without the requeue, we would be depending on an external re-trigger after
		// the 1st run for the ClusterProfile object to be reconciled.
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	if updated, err := utils.AddKCMComponentLabel(ctx, r.Client, mcs); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, err // generation has not changed, need explicit requeue
	}

	if r.IsDisabledValidationWH {
		l.Info("Validating service dependencies")
		if err := validation.ValidateServiceDependencyOverall(mcs.Spec.ServiceSpec.Services); err != nil {
			if r.setCondition(mcs, kcmv1.ServicesDependencyValidationCondition, err) {
				record.Warnf(mcs, mcs.Generation, kcmv1.ServicesDependencyValidationCondition, err.Error())
			}
			l.Error(err, "failed to validate service dependencies, will not retrigger this error")
			return ctrl.Result{}, nil
		}
		r.setCondition(mcs, kcmv1.ServicesDependencyValidationCondition, nil)

		l.Info("Validating MultiClusterService dependencies")
		if err := validation.ValidateMCSDependencyOverall(ctx, r.Client, mcs); err != nil {
			if r.setCondition(mcs, kcmv1.MultiClusterServiceDependencyValidationCondition, err) {
				record.Warnf(mcs, mcs.Generation, kcmv1.MultiClusterServiceDependencyValidationCondition, err.Error())
			}
			// Consider a scenario where mcs2 depending on mcs1 is created before mcs1 with
			// the validation webhooks disabled. In this case the user may create mcs1 sometime later.
			// Anytime after that point, we would want this validation to pass for mcs2, so we retrigger here.
			l.Error(err, "failed to validate MultiClusterService dependencies, will retrigger this error")
			return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, err
		}
		r.setCondition(mcs, kcmv1.MultiClusterServiceDependencyValidationCondition, nil)
	}

	l.V(1).Info("Cleaning up ServiceSets for ClusterDeployments that are no longer match")
	if err = r.cleanup(ctx, mcs); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile cleanup: %w", err)
	}

	l.V(1).Info("Ensuring ServiceSets for matching ClusterDeployments")
	selector, err := metav1.LabelSelectorAsSelector(&mcs.Spec.ClusterSelector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to convert ClusterSelector to selector: %w", err)
	}

	var errs error
	// if selfManagement flag is set, then we'll need to create serviceSet which does not refer
	// any clusterDeployment, but also has selfManagement flag set to true.
	if mcs.Spec.ServiceSpec.Provider.SelfManagement {
		errs = errors.Join(r.createOrUpdateServiceSet(ctx, mcs, nil))
	}

	clusters := new(kcmv1.ClusterDeploymentList)
	if err := r.Client.List(ctx, clusters, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list ClusterDeployments: %w", err)
	}

	l.V(1).Info("Matching ClusterDeployments found", "count", len(clusters.Items))
	for _, cluster := range clusters.Items {
		if !cluster.DeletionTimestamp.IsZero() {
			continue
		}
		errs = errors.Join(errs, r.createOrUpdateServiceSet(ctx, mcs, &cluster))
	}

	errs = errors.Join(errs, r.setClustersCondition(ctx, mcs))
	if errs != nil {
		return result, errs
	}

	var (
		upgradePaths []kcmv1.ServiceUpgradePaths
		servicesErr  error
	)
	upgradePaths, servicesErr = serviceset.ServicesUpgradePaths(ctx, r.Client, mcs.Spec.ServiceSpec.Services, r.SystemNamespace)
	mcs.Status.ServicesUpgradePaths = upgradePaths
	return result, servicesErr
}

// setClustersCondition updates MultiClusterService's condition which shows number of clusters where services were
// successfully deployed out of total number of matching clusters.
func (r *MultiClusterServiceReconciler) setClustersCondition(ctx context.Context, mcs *kcmv1.MultiClusterService) error {
	serviceSetList := new(kcmv1.ServiceSetList)
	if err := r.Client.List(ctx, serviceSetList, client.MatchingFields{kcmv1.ServiceSetMultiClusterServiceIndexKey: mcs.Name}); err != nil {
		return fmt.Errorf("failed to list ServiceSets for MultiClusterService %s: %w", client.ObjectKeyFromObject(mcs), err)
	}

	var totalDeployments, readyDeployments int

	c := metav1.Condition{
		Type:   kcmv1.ClusterInReadyStateCondition,
		Status: metav1.ConditionTrue,
		Reason: kcmv1.SucceededReason,
	}

	for _, serviceSet := range serviceSetList.Items {
		// We won't count serviceSets being deleted neither in total deployments count
		// nor in successful deployments count. If the serviceSet is being deleted, this
		// means that either corresponding cluster is being deleted or corresponding cluster
		// has labels which don't match selector anymore. Hence all services defined in
		// the service set will be removed from cluster and there is no reason to count
		// them anyhow.
		if !serviceSet.DeletionTimestamp.IsZero() {
			continue
		}
		totalDeployments++
		if serviceSet.Status.Deployed {
			readyDeployments++
		}
	}

	if readyDeployments < totalDeployments {
		c.Status = metav1.ConditionFalse
		c.Reason = kcmv1.FailedReason
	}

	c.Message = fmt.Sprintf("%d/%d", readyDeployments, totalDeployments)
	apimeta.SetStatusCondition(&mcs.Status.Conditions, c)
	return nil
}

// updateStatus check whether status needs to be updated, if so updates the status for the MultiClusterService object
// and returns a flag whether requeue should happen and an error.
func (r *MultiClusterServiceReconciler) updateStatus(ctx context.Context, oldObj, newObj *kcmv1.MultiClusterService) (bool, error) {
	// we'll requeue if no changes were applied to keep tracking ClusterDeployments
	// which were created or updated.
	if equality.Semantic.DeepEqual(oldObj.Status, newObj.Status) {
		return true, nil
	}

	newObj.Status.ObservedGeneration = newObj.Generation
	newObj.Status.Conditions = updateStatusConditions(newObj.Status.Conditions)

	// we'll requeue in case of successful status update due to existing GenerationChangePredicate.
	// Otherwise we'll return an error.
	if err := r.Client.Status().Update(ctx, newObj); err != nil {
		return false, fmt.Errorf("failed to update status for MultiClusterService %s/%s: %w", newObj.Namespace, newObj.Name, err)
	}
	return true, nil
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

func (r *MultiClusterServiceReconciler) reconcileDelete(ctx context.Context, mcs *kcmv1.MultiClusterService) (result ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Deleting MultiClusterService")

	defer func() {
		if err == nil {
			for _, svc := range mcs.Spec.ServiceSpec.Services {
				metrics.TrackMetricTemplateUsage(ctx, kcmv1.ServiceTemplateKind, svc.Template, kcmv1.MultiClusterServiceKind, mcs.ObjectMeta, false)
			}
		}
	}()

	if r.IsDisabledValidationWH {
		l.Info("Validating MultiClusterService dependencies")
		if err := validation.ValidateMCSDelete(ctx, r.Client, mcs); err != nil {
			if r.setCondition(mcs, kcmv1.MultiClusterServiceDependencyValidationCondition, err) {
				record.Warnf(mcs, mcs.Generation, kcmv1.MultiClusterServiceDependencyValidationCondition, err.Error())
			}
			// We want to retrigger in case the MCS which depends on this one
			// is deleted sometime later after the delete on this MCS was called.
			l.Error(err, "failed validation for MultiClusterService deletion, will retrigger")
			return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, err
		}
		r.setCondition(mcs, kcmv1.MultiClusterServiceDependencyValidationCondition, nil)
	}

	serviceSets := new(kcmv1.ServiceSetList)
	if err := r.Client.List(ctx, serviceSets, client.MatchingFields{kcmv1.ServiceSetMultiClusterServiceIndexKey: mcs.Name}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list ServiceSets for MultiClusterService %s: %w", mcs.Name, err)
	}
	l.V(1).Info("Found ServiceSets", "count", len(serviceSets.Items))
	for _, serviceSet := range serviceSets.Items {
		if !serviceSet.DeletionTimestamp.IsZero() {
			continue
		}
		if err := r.Client.Delete(ctx, &serviceSet); err != nil {
			l.Error(err, "failed to delete ServiceSet", "ServiceSet.Name", serviceSet.Name)
		}
		l.V(1).Info("Deleting ServiceSet", "namespaced_name", client.ObjectKeyFromObject(&serviceSet))
	}
	if len(serviceSets.Items) > 0 {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	if ok := controllerutil.RemoveFinalizer(mcs, kcmv1.MultiClusterServiceFinalizer); ok {
		if err := r.Client.Update(ctx, mcs); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer %s from MultiClusterService %s: %w", kcmv1.MultiClusterServiceFinalizer, mcs.Name, err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MultiClusterServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.defaultRequeueTime = 10 * time.Second

	managedController := ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcmv1.MultiClusterService{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&kcmv1.ServiceSet{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
				serviceSet, ok := o.(*kcmv1.ServiceSet)
				if !ok {
					return nil
				}
				if serviceSet.Spec.MultiClusterService == "" {
					return nil
				}
				mcs := new(kcmv1.MultiClusterService)
				if err := r.Client.Get(ctx, client.ObjectKey{Name: serviceSet.Spec.MultiClusterService}, mcs); err != nil {
					return nil
				}
				return []ctrl.Request{{NamespacedName: client.ObjectKeyFromObject(mcs)}}
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

// createOrUpdateServiceSet creates or updates the ServiceSet for the given ClusterDeployment.
func (r *MultiClusterServiceReconciler) createOrUpdateServiceSet(
	ctx context.Context,
	mcs *kcmv1.MultiClusterService,
	cd *kcmv1.ClusterDeployment,
) error {
	var err error
	l := ctrl.LoggerFrom(ctx).WithName("handle-service-set")

	// We won't create or update the ServiceSet until all MultiClusterServices
	// which this one depends on successfully deploy all of their services to
	// the cluster represented by the provided ClusterDeployment.
	if err := r.okToReconcileServiceSet(ctx, mcs, cd); err != nil {
		return fmt.Errorf("skipping create/update of ServiceSet for cluster %s: %w", client.ObjectKeyFromObject(cd), err)
	}

	providerSpec := mcs.Spec.ServiceSpec.Provider
	if providerSpec.Name == "" {
		providerSpec, err = serviceset.ConvertServiceSpecToProviderConfig(mcs.Spec.ServiceSpec)
		if err != nil {
			return fmt.Errorf("failed to convert ServiceSpec to provider config: %w", err)
		}
	}

	key := client.ObjectKey{
		Name: providerSpec.Name,
	}
	provider := new(kcmv1.StateManagementProvider)
	if err := r.Client.Get(ctx, key, provider); err != nil {
		return fmt.Errorf("failed to get StateManagementProvider %s: %w", key.String(), err)
	}

	serviceSetObjectKey := serviceset.ServiceSetObjectKey(r.SystemNamespace, cd, mcs)

	opRequisites := serviceset.OperationRequisites{
		ObjectKey:    serviceSetObjectKey,
		Services:     mcs.Spec.ServiceSpec.Services,
		ProviderSpec: providerSpec,
	}
	serviceSet, op, err := serviceset.GetServiceSetWithOperation(ctx, r.Client, opRequisites)
	if err != nil {
		return fmt.Errorf("failed to get ServiceSet %s: %w", serviceSetObjectKey.String(), err)
	}

	if op == kcmv1.ServiceSetOperationNone {
		return nil
	}
	if op == kcmv1.ServiceSetOperationDelete {
		// no-op if the ServiceSet is already being deleted.
		if !serviceSet.DeletionTimestamp.IsZero() {
			return nil
		}
		if err := r.Client.Delete(ctx, serviceSet); err != nil {
			return fmt.Errorf("failed to delete ServiceSet %s: %w", serviceSetObjectKey.String(), err)
		}
		record.Eventf(mcs, mcs.Generation, kcmv1.ServiceSetIsBeingDeletedEvent,
			"ServiceSet %s is being deleted", serviceSetObjectKey.String())
		return nil
	}

	upgradePaths, err := serviceset.ServicesUpgradePaths(
		ctx, r.Client, serviceset.ServicesWithDesiredChains(mcs.Spec.ServiceSpec.Services, serviceSet.Spec.Services), serviceSetObjectKey.Namespace)
	if err != nil {
		return fmt.Errorf("failed to determine upgrade paths for services: %w", err)
	}
	l.V(1).Info("Determined upgrade paths for services", "upgradePaths", upgradePaths)

	filteredServices, err := serviceset.FilterServiceDependencies(ctx, r.Client, cd.GetNamespace(), cd.GetName(), mcs.Spec.ServiceSpec.Services)
	if err != nil {
		return fmt.Errorf("failed to filter for services that are not dependent on any other service: %w", err)
	}
	l.V(1).Info("Services to deploy after filtering services that are not dependent on any other service", "services", filteredServices)

	resultingServices := serviceset.ServicesToDeploy(upgradePaths, filteredServices, serviceSet.Spec.Services)
	l.V(1).Info("Services to deploy", "services", resultingServices)

	serviceSet, err = serviceset.NewBuilder(cd, serviceSet, provider.Spec.Selector).
		WithMultiClusterService(mcs).
		WithServicesToDeploy(resultingServices).Build()
	if err != nil {
		return fmt.Errorf("failed to build ServiceSet %s: %w", serviceSetObjectKey.String(), err)
	}

	serviceSetProcessor := serviceset.NewProcessor(r.Client)
	err = serviceSetProcessor.CreateOrUpdateServiceSet(ctx, op, serviceSet)
	if err != nil {
		return fmt.Errorf("failed to process ServiceSet %s: %w", serviceSetObjectKey.String(), err)
	}
	return nil
}

func (r *MultiClusterServiceReconciler) cleanup(ctx context.Context, mcs *kcmv1.MultiClusterService) error {
	serviceSets := new(kcmv1.ServiceSetList)
	// we'll list all ServiceSets which have .spec.multiClusterService defined and match
	// current MultiClusterService object being reconciled
	if err := r.Client.List(ctx, serviceSets, client.MatchingFields{kcmv1.ServiceSetMultiClusterServiceIndexKey: mcs.Name}); err != nil {
		return fmt.Errorf("failed to list ServiceSets for MultiClusterService %s: %w", mcs.Name, err)
	}

	selector, err := metav1.LabelSelectorAsSelector(&mcs.Spec.ClusterSelector)
	if err != nil {
		return fmt.Errorf("failed to convert ClusterSelector to label selector: %w", err)
	}

	var errs error
	for _, serviceSet := range serviceSets.Items {
		// this will happen in case the corresponding ClusterDeployment was deleted,
		// which triggered ServiceSet deletion as
		if !serviceSet.DeletionTimestamp.IsZero() {
			continue
		}

		// this is a self-management ServiceSet, skipping
		if serviceSet.Spec.Cluster == "" {
			continue
		}

		cd := new(kcmv1.ClusterDeployment)
		key := client.ObjectKey{Namespace: serviceSet.Namespace, Name: serviceSet.Spec.Cluster}
		if err := r.Client.Get(ctx, key, cd); err != nil {
			return fmt.Errorf("failed to get ClusterDeployment %s: %w", key.String(), err)
		}

		// ClusterDeployment labels match selector, skipping
		if selector.Matches(labels.Set(cd.Labels)) {
			continue
		}

		// we want to delete serviceSet since clusterDeployment does not match selector anymore
		if err := r.Client.Delete(ctx, &serviceSet); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to delete ServiceSet %s: %w", key.String(), err))
		}
	}
	return errs
}

func (*MultiClusterServiceReconciler) setCondition(mcs *kcmv1.MultiClusterService, typ string, err error) bool {
	reason, cstatus, msg := kcmv1.SucceededReason, metav1.ConditionTrue, ""
	if err != nil {
		reason, cstatus, msg = kcmv1.FailedReason, metav1.ConditionFalse, err.Error()
	}

	return apimeta.SetStatusCondition(&mcs.Status.Conditions, metav1.Condition{
		Type:               typ,
		Status:             cstatus,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: mcs.Generation,
	})
}

// okToReconcileServiceSet verifies if it is ok to reconcile a serviceset for the provided
// mcs and cd by verifying if all of the services defined in the multiclusterservices that
// mcs depends on have been successfully deployed on the cluster represented by cd.
func (r *MultiClusterServiceReconciler) okToReconcileServiceSet(ctx context.Context, mcs *kcmv1.MultiClusterService, cd *kcmv1.ClusterDeployment) error {
	var errs error

	for _, dep := range mcs.Spec.DependsOn {
		// Get the MCS this one depends on.
		depMCSKey := client.ObjectKey{Name: dep}
		depMCS := new(kcmv1.MultiClusterService)
		if err := r.Client.Get(ctx, depMCSKey, depMCS); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to get MultiClusterService %s which this depends on: %w", depMCSKey, err))
			continue
		}

		// Get the ServiceSet associated with provided CD and depMCS.
		sset := new(kcmv1.ServiceSet)
		ssetKey := serviceset.ServiceSetObjectKey(r.SystemNamespace, cd, depMCS)
		err := r.Client.Get(ctx, ssetKey, sset)
		if apierrors.IsNotFound(err) {
			// If the ServiceSet for depMCS is not yet created, we will
			// consider that an error so that the reconcile loop is retriggered.
			errs = errors.Join(errs, fmt.Errorf("serviceSet %s owned by MultiClusterService %s which this depends on not yet created: %w", ssetKey, depMCSKey, err))
			continue
		}
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to get serviceSet %s owned by MultiClusterService %s which this depends on: %w", ssetKey, depMCSKey, err))
			continue
		}

		// To check if all services for depMCS have been deployed, we have
		// to use depMCS's spec because the ServiceSet may not have the full
		// list of services in it's spec or status due to inter-service dependencies.
		svcToCheck := make(map[client.ObjectKey]struct{}, len(depMCS.Spec.ServiceSpec.Services))
		for _, svc := range depMCS.Spec.ServiceSpec.Services {
			svcToCheck[serviceset.ServiceKey(svc.Namespace, svc.Name)] = struct{}{}
		}

		for _, svc := range sset.Status.Services {
			deployed := 0
			if _, ok := svcToCheck[serviceset.ServiceKey(svc.Namespace, svc.Name)]; ok {
				if svc.State == kcmv1.ServiceStateDeployed {
					deployed++
				}
			}

			if deployed != len(depMCS.Spec.ServiceSpec.Services) {
				errs = errors.Join(errs, fmt.Errorf("all services managed by ServiceSet %s owned by MultiClusterService %s not yet deployed", ssetKey, client.ObjectKeyFromObject(mcs)))
				continue
			}
		}
	}

	return errs
}
