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
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
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
)

// MultiClusterServiceReconciler reconciles a MultiClusterService object
type MultiClusterServiceReconciler struct {
	Client                 client.Client
	SystemNamespace        string
	IsDisabledValidationWH bool // is webhook disabled set via the controller flags

	defaultRequeueTime time.Duration
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

	clone := mcs.DeepCopy()

	defer func() {
		// we need to explicitly requeue MultiClusterService object,
		// otherwise we'll miss if some ClusterDeployment will be updated
		// with matching labels.
		if equality.Semantic.DeepEqual(clone.Status, mcs.Status) {
			result.RequeueAfter = r.defaultRequeueTime
			return
		}
		err = r.updateStatus(ctx, mcs)
	}()

	l.V(1).Info("Ensuring ServiceSets for matching ClusterDeployments")
	selector, err := metav1.LabelSelectorAsSelector(&mcs.Spec.ClusterSelector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to convert ClusterSelector to selector: %w", err)
	}

	var errs error
	clusters := new(kcmv1.ClusterDeploymentList)
	if err := r.Client.List(ctx, clusters, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list ClusterDeployments: %w", err)
	}

	l.V(1).Info("Matching ClusterDeployments listed", "count", len(clusters.Items))
	for _, cluster := range clusters.Items {
		if !cluster.DeletionTimestamp.IsZero() {
			continue
		}

		err = r.createOrUpdateServiceSet(ctx, mcs, &cluster)
		if err != nil {
			errs = errors.Join(errs, err)
		}
	}

	if errs != nil {
		return result, errs
	}

	var (
		upgradePaths []kcmv1.ServiceUpgradePaths
		servicesErr  error
	)
	upgradePaths, servicesErr = servicesUpgradePaths(ctx, r.Client, mcs.Spec.ServiceSpec.Services, r.SystemNamespace)
	mcs.Status.ServicesUpgradePaths = upgradePaths
	return result, servicesErr
}

// updateStatus updates the status for the MultiClusterService object.
func (r *MultiClusterServiceReconciler) updateStatus(ctx context.Context, mcs *kcmv1.MultiClusterService) error {
	mcs.Status.ObservedGeneration = mcs.Generation
	mcs.Status.Conditions = updateStatusConditions(mcs.Status.Conditions)

	if err := r.Client.Status().Update(ctx, mcs); err != nil {
		return fmt.Errorf("failed to update status for MultiClusterService %s/%s: %w", mcs.Namespace, mcs.Name, err)
	}

	return nil
}

func getServicesReadinessCondition(serviceStatuses []kcmv1.ServiceState, desiredServices int) metav1.Condition {
	ready := 0
	for _, svcstatus := range serviceStatuses {
		if svcstatus.State == kcmv1.ServiceStateDeployed {
			ready++
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

func servicesUpgradePaths(
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
	l := ctrl.LoggerFrom(ctx)
	l.Info("Deleting MultiClusterService")

	defer func() {
		if err == nil {
			for _, svc := range mcs.Spec.ServiceSpec.Services {
				metrics.TrackMetricTemplateUsage(ctx, kcmv1.ServiceTemplateKind, svc.Template, kcmv1.MultiClusterServiceKind, mcs.ObjectMeta, false)
			}
		}
	}()

	serviceSets := new(kcmv1.ServiceSetList)
	selector := fields.OneTermEqualSelector(kcmv1.ServiceSetMultiClusterServiceIndexKey, mcs.Name)
	if err := r.Client.List(ctx, serviceSets, &client.ListOptions{FieldSelector: selector}); err != nil {
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

	if controllerutil.RemoveFinalizer(mcs, kcmv1.MultiClusterServiceFinalizer) {
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

// serviceSetWithOperation returns the ServiceSetOperation to perform and the ServiceSet object,
// depending on the existence of the ServiceSet object and the services to deploy.
func serviceSetWithOperation(
	ctx context.Context,
	c client.Client,
	serviceSetObjectKey client.ObjectKey,
	services []kcmv1.Service,
	providerSpec kcmv1.ProviderSpec,
) (*kcmv1.ServiceSet, kcmv1.ServiceSetOperation, error) {
	l := ctrl.LoggerFrom(ctx)
	serviceSet := new(kcmv1.ServiceSet)
	err := c.Get(ctx, serviceSetObjectKey, serviceSet)
	if client.IgnoreNotFound(err) != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to get ServiceSet %s: %w", serviceSetObjectKey, err)
	}

	switch {
	case err != nil && len(services) == 0:
		l.V(1).Info("No services to deploy, ServiceSet does not exist", "operation", kcmv1.ServiceSetOperationNone)
		return nil, kcmv1.ServiceSetOperationNone, nil
	case err != nil && len(services) > 0:
		l.V(1).Info("Pending services to deploy, ServiceSet does not exist", "operation", kcmv1.ServiceSetOperationCreate)
		serviceSet.SetName(serviceSetObjectKey.Name)
		serviceSet.SetNamespace(serviceSetObjectKey.Namespace)
		return serviceSet, kcmv1.ServiceSetOperationCreate, nil
	case len(services) == 0:
		l.V(1).Info("No services to deploy, ServiceSet exists", "operation", kcmv1.ServiceSetOperationDelete)
		return serviceSet, kcmv1.ServiceSetOperationDelete, nil
	case serviceSetNeedsUpdate(serviceSet, providerSpec, services):
		l.V(1).Info("Pending services to deploy, ServiceSet exists", "operation", kcmv1.ServiceSetOperationUpdate)
		return serviceSet, kcmv1.ServiceSetOperationUpdate, nil
	default:
		l.V(1).Info("No actions required, ServiceSet exists", "operation", kcmv1.ServiceSetOperationNone)
		return serviceSet, kcmv1.ServiceSetOperationNone, nil
	}
}

// serviceSetNeedsUpdate checks if the ServiceSet needs to be updated based on the ClusterDeployment spec.
// It first compares the ServiceSet's provider configuration with the ClusterDeployment's service provider configuration.
// Then it compares the ServiceSet's observed services' state with its desired state, and after that it compares
// the ServiceSet's observed services' state with ClusterDeployment's desired services state.
func serviceSetNeedsUpdate(serviceSet *kcmv1.ServiceSet, providerSpec kcmv1.ProviderSpec, services []kcmv1.Service) bool {
	// we'll need to update provider configuration if it was changed.
	if !equality.Semantic.DeepEqual(providerSpec, serviceSet.Spec.Provider) {
		return true
	}

	// we'll need to compare observed services' state with desired state to ensure
	// ServiceSet was already reconciled and services are properly deployed.
	// we won't update ServiceSet until that.
	observedServiceStateMap := make(map[types.NamespacedName]kcmv1.ServiceState)
	for _, s := range serviceSet.Status.Services {
		observedServiceStateMap[types.NamespacedName{Name: s.Name, Namespace: s.Namespace}] = kcmv1.ServiceState{
			Name:      s.Name,
			Namespace: s.Namespace,
			Template:  s.Template,
			State:     s.State,
		}
	}
	desiredServiceStateMap := make(map[types.NamespacedName]kcmv1.ServiceState)
	desiredServicesMap := make(map[types.NamespacedName]kcmv1.ServiceWithValues)
	for _, s := range serviceSet.Spec.Services {
		desiredServiceStateMap[types.NamespacedName{Name: s.Name, Namespace: s.Namespace}] = kcmv1.ServiceState{
			Name:      s.Name,
			Namespace: s.Namespace,
			Template:  s.Template,
			State:     kcmv1.ServiceStateDeployed,
		}
		desiredServicesMap[types.NamespacedName{Name: s.Name, Namespace: s.Namespace}] = kcmv1.ServiceWithValues{
			Name:       s.Name,
			Namespace:  s.Namespace,
			Template:   s.Template,
			Values:     s.Values,
			ValuesFrom: s.ValuesFrom,
		}
	}
	// difference between observed and desired services state means that ServiceSet was not fully
	// deployed yet. Therefore we won't update ServiceSet until that.
	if !equality.Semantic.DeepEqual(observedServiceStateMap, desiredServiceStateMap) {
		return false
	}

	// now, since ServiceSet is fully deployed, we can compare it with ClusterDeployment's desired services state.
	clusterDeploymentServicesMap := make(map[types.NamespacedName]kcmv1.ServiceWithValues)
	for _, s := range services {
		clusterDeploymentServicesMap[types.NamespacedName{Name: s.Name, Namespace: s.Namespace}] = kcmv1.ServiceWithValues{
			Name:       s.Name,
			Namespace:  s.Namespace,
			Template:   s.Template,
			Values:     s.Values,
			ValuesFrom: s.ValuesFrom,
		}
	}
	// difference between services defined in ClusterDeployment and ServiceSet means that ServiceSet needs to be updated.
	return !equality.Semantic.DeepEqual(desiredServicesMap, clusterDeploymentServicesMap)
}

// servicesToDeploy returns the services to deploy based on the ClusterDeployment spec,
// taking into account already deployed services, dependencies, and versioning.
func servicesToDeploy(
	ctx context.Context,
	c client.Client,
	namespace string,
	desiredServices []kcmv1.Service,
	_ []kcmv1.ServiceState,
) ([]kcmv1.ServiceWithValues, error) {
	// todo: implement dependencies resolution, taking into account observed services state
	// todo: implement sequential version updates, taking into account observed services state
	_, err := servicesUpgradePaths(ctx, c, desiredServices, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get upgrade paths for services: %w", err)
	}

	services := make([]kcmv1.ServiceWithValues, 0)
	for _, s := range desiredServices {
		if s.Disable {
			continue
		}
		services = append(services, kcmv1.ServiceWithValues{
			Name:       s.Name,
			Namespace:  s.Namespace,
			Template:   s.Template,
			Values:     s.Values,
			ValuesFrom: s.ValuesFrom,
		})
	}
	return services, nil
}

// createOrUpdateServiceSet creates or updates the ServiceSet for the given ClusterDeployment.
func (r *MultiClusterServiceReconciler) createOrUpdateServiceSet(
	ctx context.Context,
	mcs *kcmv1.MultiClusterService,
	cd *kcmv1.ClusterDeployment,
) error {
	provider := new(kcmv1.StateManagementProvider)
	key := client.ObjectKey{
		Name: mcs.Spec.ServiceSpec.Provider.Name,
	}
	if err := r.Client.Get(ctx, key, provider); err != nil {
		return fmt.Errorf("failed to get StateManagementProvider %s: %w", key.String(), err)
	}

	// we'll use the following pattern to build ServiceSet name:
	// <ClusterDeploymentName>-<MultiClusterServiceNameHash>
	// this will guarantee that the ServiceSet produced by MultiClusterService
	// has name unique for each ClusterDeployment.
	mcsNameHash := sha256.Sum256([]byte(mcs.Name))
	serviceSetObjectKey := client.ObjectKey{
		Namespace: cd.Namespace,
		Name:      fmt.Sprintf("%s-%x", cd.Name, mcsNameHash[:4]),
	}

	serviceSet, op, err := serviceSetWithOperation(ctx, r.Client, serviceSetObjectKey, mcs.Spec.ServiceSpec.Services, mcs.Spec.ServiceSpec.Provider)
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

	resultingServices, err := servicesToDeploy(ctx, r.Client, cd.Namespace, mcs.Spec.ServiceSpec.Services, serviceSet.Status.Services)
	if err != nil {
		return fmt.Errorf("failed to get services to deploy: %w", err)
	}
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
