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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
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

	// due to API change we'll need to update existing objects
	if len(mcs.Spec.ServiceSpec.Services) != 0 && mcs.Spec.ServiceSpec.Provider.Name == "" {
		mcs.Spec.ServiceSpec, err = updateServiceSpecWithProvider(mcs.Spec.ServiceSpec)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update ServiceSpec: %w", err)
		}
		return ctrl.Result{}, r.Client.Update(ctx, mcs)
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
	upgradePaths, servicesErr = serviceset.ServicesUpgradePaths(ctx, r.Client, mcs.Spec.ServiceSpec.Services, r.SystemNamespace)
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

// createOrUpdateServiceSet creates or updates the ServiceSet for the given ClusterDeployment.
func (r *MultiClusterServiceReconciler) createOrUpdateServiceSet(
	ctx context.Context,
	mcs *kcmv1.MultiClusterService,
	cd *kcmv1.ClusterDeployment,
) error {
	l := ctrl.LoggerFrom(ctx).WithName("handle-service-set")
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

	serviceSet, op, err := serviceset.GetServiceSetWithOperation(ctx, r.Client, serviceSetObjectKey, mcs.Spec.ServiceSpec.Services, mcs.Spec.ServiceSpec.Provider)
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
		ctx, r.Client, serviceset.ServicesWithDesiredChains(mcs.Spec.ServiceSpec.Services, serviceSet.Spec.Services), cd.Namespace)
	if err != nil {
		return fmt.Errorf("failed to determine upgrade paths for services: %w", err)
	}
	l.V(1).Info("Determined upgrade paths for services", "upgradePaths", upgradePaths)
	resultingServices := serviceset.ServicesToDeploy(upgradePaths, mcs.Spec.ServiceSpec.Services, serviceSet.Spec.Services)
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

// updateServiceSpecWithProvider moves sveltos-specific configuration
// from .spec.serviceSpec to .spec.serviceSpec.provider.config
func updateServiceSpecWithProvider(serviceSpec kcmv1.ServiceSpec) (kcmv1.ServiceSpec, error) {
	type config struct {
		SyncMode             string                                       `json:"syncMode,omitempty"`
		TemplateResourceRefs []addoncontrollerv1beta1.TemplateResourceRef `json:"templateResourceRefs,omitempty"`
		PolicyRefs           []addoncontrollerv1beta1.PolicyRef           `json:"policyRefs,omitempty"`
		DriftIgnore          []libsveltosv1beta1.PatchSelector            `json:"driftIgnore,omitempty"`
		DriftExclusions      []addoncontrollerv1beta1.DriftExclusion      `json:"driftExclusions,omitempty"`
		Priority             int32                                        `json:"priority,omitempty"`
		StopOnConflict       bool                                         `json:"stopOnConflict,omitempty"`
		Reload               bool                                         `json:"reloader,omitempty"`
		ContinueOnError      bool                                         `json:"continueOnError,omitempty"`
	}

	cfg := config{
		SyncMode:             serviceSpec.SyncMode,
		TemplateResourceRefs: serviceSpec.TemplateResourceRefs,
		PolicyRefs:           serviceSpec.PolicyRefs,
		DriftIgnore:          serviceSpec.DriftIgnore,
		DriftExclusions:      serviceSpec.DriftExclusions,
		Priority:             serviceSpec.Priority,
		StopOnConflict:       serviceSpec.StopOnConflict,
		Reload:               serviceSpec.Reload,
		ContinueOnError:      serviceSpec.ContinueOnError,
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return kcmv1.ServiceSpec{}, err
	}

	providerConfig := kcmv1.StateManagementProviderConfig{
		Config: &apiextv1.JSON{Raw: raw},
		Name:   utils.DefaultStateManagementProvider,
	}

	services := make([]kcmv1.Service, len(serviceSpec.Services))
	copy(services, serviceSpec.Services)

	return kcmv1.ServiceSpec{
		Provider: providerConfig,
		Services: services,
	}, nil
}
