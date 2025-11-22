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
	labelsutil "github.com/K0rdent/kcm/internal/util/labels"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
)

// MultiClusterServiceReconciler reconciles a MultiClusterService object
type MultiClusterServiceReconciler struct {
	Client client.Client

	timeFunc func() time.Time

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

	if updated, err := labelsutil.AddKCMComponentLabel(ctx, r.Client, mcs); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, err // generation has not changed, need explicit requeue
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

	l.Info("Validating service templates")
	if err := validationutil.ServicesHaveValidTemplates(ctx, r.Client, mcs.Spec.ServiceSpec.Services, r.SystemNamespace); err != nil {
		if r.setCondition(mcs, kcmv1.ServicesReferencesValidationCondition, err) {
			record.Warnf(mcs, mcs.Generation, kcmv1.ServicesReferencesValidationCondition, err.Error())
		}
		l.Error(err, "failed to validate service template references")
		// Will not retrigger this error because the MCS controller is
		// already configured to watch for changes in ServiceTemplates.
		return ctrl.Result{}, nil
	}
	r.setCondition(mcs, kcmv1.ServicesReferencesValidationCondition, nil)

	l.Info("Validating service dependencies")
	if err := validationutil.ValidateServiceDependencyOverall(mcs.Spec.ServiceSpec.Services); err != nil {
		if r.setCondition(mcs, kcmv1.ServicesDependencyValidationCondition, err) {
			record.Warnf(mcs, mcs.Generation, kcmv1.ServicesDependencyValidationCondition, err.Error())
		}
		l.Error(err, "failed to validate service dependencies of services defined in spec, will not retrigger")
		// Will not retrigger this error because nothing to do until spec is changed.
		return ctrl.Result{}, nil
	}
	r.setCondition(mcs, kcmv1.ServicesDependencyValidationCondition, nil)

	l.Info("Validating MultiClusterService dependencies")
	if err := validationutil.ValidateMCSDependencyOverall(ctx, r.Client, mcs); err != nil {
		if r.setCondition(mcs, kcmv1.MultiClusterServiceDependencyValidationCondition, err) {
			record.Warnf(mcs, mcs.Generation, kcmv1.MultiClusterServiceDependencyValidationCondition, err.Error())
		}
		l.Error(err, "failed to validate MultiClusterService dependencies, will not retrigger")
		// Will not retrigger this error because nothing to do until spec is changed.
		return ctrl.Result{}, nil
	}
	r.setCondition(mcs, kcmv1.MultiClusterServiceDependencyValidationCondition, nil)

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

	clustersErr := r.setMatchingClusters(ctx, mcs)

	return result, errors.Join(servicesErr, clustersErr)
}

// setClustersCondition updates MultiClusterService's condition which shows number of clusters where services were
// successfully deployed out of total number of matching clusters.
func (r *MultiClusterServiceReconciler) setClustersCondition(ctx context.Context, mcs *kcmv1.MultiClusterService) error {
	l := ctrl.LoggerFrom(ctx)
	l.V(1).Info("Reconciling MultiClusterService conditions")

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

// setMatchingClusters collects service deployments status on matching clusters from ServiceSet objects and
// updates MultiClusterService object's status.
func (r *MultiClusterServiceReconciler) setMatchingClusters(ctx context.Context, mcs *kcmv1.MultiClusterService) error {
	l := ctrl.LoggerFrom(ctx)
	l.V(1).Info("Reconciling MultiClusterService matching clusters")

	serviceSetList := new(kcmv1.ServiceSetList)
	if err := r.Client.List(ctx, serviceSetList, client.MatchingFields{kcmv1.ServiceSetMultiClusterServiceIndexKey: mcs.Name}); err != nil {
		return fmt.Errorf("failed to list ServiceSets for MultiClusterService %s: %w", client.ObjectKeyFromObject(mcs), err)
	}

	now := metav1.NewTime(r.timeFunc())
	matchingClusters := make([]kcmv1.MatchingCluster, 0, len(serviceSetList.Items))

	var errs error
	for _, serviceSet := range serviceSetList.Items {
		// we'll skip service sets being deleted
		if !serviceSet.DeletionTimestamp.IsZero() {
			continue
		}
		// we'll skip service sets which does not have cluster reference set yet
		if serviceSet.Status.Cluster == nil {
			continue
		}

		cluster := kcmv1.MatchingCluster{
			ObjectReference:    serviceSet.Status.Cluster.DeepCopy(),
			LastTransitionTime: &now,
			Regional:           false,
			Deployed:           serviceSet.Status.Deployed,
		}
		if cluster.Kind == kcmv1.ClusterDeploymentKind {
			cd := new(kcmv1.ClusterDeployment)
			key := client.ObjectKey{Name: cluster.Name, Namespace: cluster.Namespace}
			if err := r.Client.Get(ctx, key, cd); err != nil {
				errs = errors.Join(errs, fmt.Errorf("failed to get ClusterDeployment %s: %w", key, err))
				continue
			}
			cred := new(kcmv1.Credential)
			key = client.ObjectKey{
				Namespace: cd.Namespace,
				Name:      cd.Spec.Credential,
			}
			if err := r.Client.Get(ctx, key, cred); err != nil {
				errs = errors.Join(errs, fmt.Errorf("failed to get Credential %s: %w", key, err))
				continue
			}
			cluster.Regional = cred.Spec.Region != ""
		}
		matchingClusters = append(matchingClusters, cluster)
	}

	observedClustersMap := make(map[client.ObjectKey]kcmv1.MatchingCluster)
	for _, cluster := range mcs.Status.MatchingClusters {
		observedClustersMap[client.ObjectKey{Name: cluster.Name, Namespace: cluster.Namespace}] = cluster
	}

	resultingClusters := make([]kcmv1.MatchingCluster, 0)
	for _, cluster := range matchingClusters {
		observedCluster, ok := observedClustersMap[client.ObjectKey{Name: cluster.Name, Namespace: cluster.Namespace}]
		if !ok {
			resultingClusters = append(resultingClusters, cluster)
			continue
		}
		if observedCluster.Deployed != cluster.Deployed {
			observedCluster.Deployed = cluster.Deployed
			observedCluster.LastTransitionTime = cluster.LastTransitionTime.DeepCopy()
		}
		resultingClusters = append(resultingClusters, observedCluster)
	}
	mcs.Status.MatchingClusters = resultingClusters
	return errs
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
	// Check if the object is being deleted first
	deletingIdx := slices.IndexFunc(conditions, func(c metav1.Condition) bool {
		return c.Type == kcmv1.DeletingCondition
	})
	if deletingIdx >= 0 {
		apimeta.SetStatusCondition(&conditions, metav1.Condition{
			Type:    kcmv1.ReadyCondition,
			Status:  conditions[deletingIdx].Status,
			Reason:  conditions[deletingIdx].Reason,
			Message: conditions[deletingIdx].Message,
		})
		return conditions
	}

	var warnings, errs []string
	for _, cond := range conditions {
		if cond.Type == kcmv1.ReadyCondition {
			continue
		}

		if cond.Type == kcmv1.PausedCondition {
			// If True and Paused, the cluster is paused and thus is not ready
			if cond.Status == metav1.ConditionTrue && cond.Reason == kcmv1.PausedReason {
				errs = append(errs, cond.Message)
			}
			// If False and NotPaused, that's normal operation - no need to include in status
			continue
		}

		switch cond.Status {
		case metav1.ConditionTrue:
			// Do nothing
		case metav1.ConditionUnknown:
			warnings = append(warnings, cond.Message)
		case metav1.ConditionFalse:
			// Preserve the special case handling for specific condition types
			switch cond.Type {
			case kcmv1.ClusterInReadyStateCondition:
				errs = append(errs, cond.Message+" Clusters are ready.")
			case kcmv1.ServicesInReadyStateCondition:
				errs = append(errs, cond.Message+" Services are ready.")
			default:
				errs = append(errs, cond.Message)
			}
		}
	}

	// Create ready condition
	readyCondition := metav1.Condition{
		Type:    kcmv1.ReadyCondition,
		Status:  metav1.ConditionTrue,
		Reason:  kcmv1.SucceededReason,
		Message: "Object is ready",
	}

	if len(errs) > 0 {
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = kcmv1.FailedReason
		readyCondition.Message = strings.Join(errs, ". ")
	} else if len(warnings) > 0 {
		readyCondition.Status = metav1.ConditionUnknown
		readyCondition.Reason = kcmv1.ProgressingReason
		readyCondition.Message = strings.Join(warnings, ". ")
	}

	apimeta.SetStatusCondition(&conditions, readyCondition)
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

	l.Info("Validating MultiClusterService dependencies for delete")
	if err := validationutil.ValidateMCSDelete(ctx, r.Client, mcs); err != nil {
		if r.setCondition(mcs, kcmv1.MultiClusterServiceDependencyValidationCondition, err) {
			record.Warnf(mcs, mcs.Generation, kcmv1.MultiClusterServiceDependencyValidationCondition, err.Error())
		}
		l.Error(err, "failed validation for MultiClusterService deletion, will retrigger")
		// Will retrigger this error because we want this MCS to be deleted once:
		// 1. Either the MCS this one depends on is deleted.
		// 2. Or the dependency is removed.
		return ctrl.Result{}, err
	}
	r.setCondition(mcs, kcmv1.MultiClusterServiceDependencyValidationCondition, nil)

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
	if r.timeFunc == nil {
		r.timeFunc = time.Now
	}
	r.defaultRequeueTime = 10 * time.Second

	managedController := ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
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
		return err
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

	serviceSetObjectKey := serviceset.ObjectKey(r.SystemNamespace, cd, mcs)

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

	upgradePaths, err := serviceset.ServicesUpgradePaths(
		ctx, r.Client, serviceset.ServicesWithDesiredChains(mcs.Spec.ServiceSpec.Services, serviceSet.Spec.Services), serviceSetObjectKey.Namespace)
	if err != nil {
		return fmt.Errorf("failed to determine upgrade paths for services: %w", err)
	}
	l.V(1).Info("Determined upgrade paths for services", "upgradePaths", upgradePaths)

	filteredServices, err := serviceset.FilterServiceDependencies(ctx, r.Client, r.SystemNamespace, mcs, cd, mcs.Spec.ServiceSpec.Services)
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
func (r *MultiClusterServiceReconciler) okToReconcileServiceSet(ctx context.Context, mcs *kcmv1.MultiClusterService, cd *kcmv1.ClusterDeployment) (errs error) {
	clusterRef := client.ObjectKey{Namespace: "mgmt", Name: "mgmt"}
	clusterLabels := map[string]string{
		kcmv1.K0rdentManagementClusterLabelKey: kcmv1.K0rdentManagementClusterLabelValue,
		// TODO(https://github.com/k0rdent/kcm/issues/2170):
		// Now that we have the ability to use providers other than sveltos,
		// perhaps we should not use the "sveltos-agent:present" label for
		// matching to the management cluster anymore as described in docs:
		// https://github.com/k0rdent/docs/blob/18d23d6/docs/admin/ksm/ksm-self-management.md?plain=1#L24-L27
		// because we want to keep this code as provider agnostic as possible.
		"sveltos-agent": "present",
	}
	if !mcs.Spec.ServiceSpec.Provider.SelfManagement {
		// cd should never be nil here because selfManagement=false.
		clusterRef = client.ObjectKeyFromObject(cd)
		clusterLabels = cd.Labels
	}

	defer func() {
		if errs != nil {
			errs = errors.Join(errs, fmt.Errorf("skipping create/update of ServiceSet for matching cluster %s", clusterRef))
		}
	}()

	for _, dep := range mcs.Spec.DependsOn {
		// Get the MCS this one depends on.
		depMCSKey := client.ObjectKey{Name: dep}
		depMCS := new(kcmv1.MultiClusterService)
		if err := r.Client.Get(ctx, depMCSKey, depMCS); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to get MultiClusterService %s which this depends on: %w", depMCSKey, err))
			continue
		}

		// Check if depMCS matches either the
		// provided CD or the mgmt cluster if cd=nil.
		sel, err := metav1.LabelSelectorAsSelector(&depMCS.Spec.ClusterSelector)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to determine if MultiClusterService %s which this depends on matches cluster %s: %w", depMCSKey, clusterRef, err))
			continue
		}

		if !sel.Matches(labels.Set(clusterLabels)) {
			// depMCS does not match the provided CD or mgmt cluster so continue.
			continue
		}

		// Get the ServiceSet associated with provided CD and depMCS.
		sset := new(kcmv1.ServiceSet)
		ssetKey := serviceset.ObjectKey(r.SystemNamespace, cd, depMCS)
		err = r.Client.Get(ctx, ssetKey, sset)
		if apierrors.IsNotFound(err) {
			// If the ServiceSet for depMCS is not yet created, we will
			// consider that an error so that the reconcile loop is retriggered.
			//
			// NOTE: We can safely retrigger here by adding error to return value because
			// we already return earlier if depMCS does not match either the cluster
			// represented by CD or the mgmt cluster. If that check is removed then a
			// bug may be introduced where the ServiceSet for this MCS and cluster is
			// never created if any one of the depMCS has a set of selector labels that
			// don't match either the cluster represented by CD or the mgmt cluster.
			// In such a scenario, the execution will always add error and continue because
			// it is trying to fetch the ServiceSet for depMCS and cluster which will never exist.
			errs = errors.Join(errs, fmt.Errorf("serviceSet %s (owned by MultiClusterService %s) which this depends on not yet created: %w", ssetKey, depMCSKey, err))
			continue
		}
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to get serviceSet %s (owned by MultiClusterService %s) which this depends on: %w", ssetKey, depMCSKey, err))
			continue
		}

		// To check if all services for depMCS have been deployed, we have
		// to use depMCS's spec because the ServiceSet may not have the full
		// list of services in it's spec or status due to inter-service dependencies.
		svcToCheck := make(map[client.ObjectKey]struct{}, len(depMCS.Spec.ServiceSpec.Services))
		for _, svc := range depMCS.Spec.ServiceSpec.Services {
			svcToCheck[serviceset.ServiceKey(svc.Namespace, svc.Name)] = struct{}{}
		}

		deployed := 0
		for _, svc := range sset.Status.Services {
			if _, ok := svcToCheck[serviceset.ServiceKey(svc.Namespace, svc.Name)]; ok {
				if svc.State == kcmv1.ServiceStateDeployed {
					deployed++
				}
			}
		}

		if deployed != len(depMCS.Spec.ServiceSpec.Services) {
			errs = errors.Join(errs, fmt.Errorf("not all services in ServiceSet %s (owned by MultiClusterService %s) are deployed (%d/%d deployed)", ssetKey, client.ObjectKeyFromObject(depMCS), deployed, len(depMCS.Spec.ServiceSpec.Services)))
			continue
		}
	}

	return errs
}
