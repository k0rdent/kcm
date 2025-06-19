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

package statemanagementprovider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/decls"
	"github.com/google/cel-go/common/types"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/record"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
)

const (
	serviceAccountSuffix     = "-sa"
	clusterRoleSuffix        = "-cr"
	clusterRoleBindingSuffix = "-crb"

	apiExtensionsGroup    = "apiextensions.k8s.io"
	apiExtensionsVersion  = "v1"
	apiExtensionsResource = "customresourcedefinitions"

	emptyConditionMessage = ""
)

type (
	DiscoveryClientFunc func(*rest.Config) (discovery.DiscoveryInterface, error)
	DynamicClientFunc   func(*rest.Config) (dynamic.Interface, error)
)

// Reconciler reconciles a StateManagementProvider object
type Reconciler struct {
	client.Client

	discoveryClientFunc DiscoveryClientFunc
	dynamicClientFunc   DynamicClientFunc

	config          *rest.Config
	timeFunc        func() time.Time
	SystemNamespace string
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling StateManagementProvider")

	smp := new(kcmv1.StateManagementProvider)
	err = r.Get(ctx, req.NamespacedName, smp)
	if apierrors.IsNotFound(err) {
		l.Info("StateManagementProvider not found, skipping")
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	if !smp.DeletionTimestamp.IsZero() {
		l.Info("StateManagementProvider is being deleted, skipping")
		return ctrl.Result{}, nil
	}

	if smp.Spec.Suspend {
		l.Info("StateManagementProvider is suspended, skipping")
		return ctrl.Result{}, nil
	}

	defer func() {
		smp.Status.Ready = !slices.ContainsFunc(smp.Status.Conditions, func(c metav1.Condition) bool {
			return c.Status == metav1.ConditionFalse || c.Status == metav1.ConditionUnknown
		})
		err = errors.Join(err, r.Status().Update(ctx, smp))
		l.Info("StateManagementProvider reconciled", "duration", time.Since(start))
	}()

	fillConditions(smp, r.timeFunc())

	// We'll ensure RBAC resources first and return in case of an error. Without RBAC
	// resources, we'll not be able to reconcile other resources.
	if reconcileErr := r.ensureRBAC(ctx, smp); reconcileErr != nil {
		record.Warnf(smp, smp.Generation, kcmv1.StateManagementProviderFailedRBACEvent,
			"Failed to ensure RBAC for %s %s: %v", kcmv1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), reconcileErr)
		return ctrl.Result{}, reconcileErr
	}

	// When RBAC resources are ready, we can reconcile other resources without failing,
	// due to adapter, provisioner and provisioner CRDs are independent of each other.
	// Errors will be joined and returned at the end of the reconciliation.
	config := impersonationConfigForServiceAccount(r.config, smp.Name, r.SystemNamespace)
	if reconcileErr := r.ensureAdapter(ctx, config, smp); reconcileErr != nil {
		record.Warnf(smp, smp.Generation, kcmv1.StateManagementProviderFailedAdapterEvent,
			"Failed to ensure adapter for %s %s: %v", kcmv1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), reconcileErr)
		err = errors.Join(err, reconcileErr)
	}
	if reconcileErr := r.ensureProvisioner(ctx, config, smp); reconcileErr != nil {
		record.Warnf(smp, smp.Generation, kcmv1.StateManagementProviderFailedProvisionerEvent,
			"Failed to ensure provisioner for %s %s: %v", kcmv1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), reconcileErr)
		err = errors.Join(err, reconcileErr)
	}
	if reconcileErr := r.ensureProvisionerCRDs(ctx, config, smp); reconcileErr != nil {
		record.Warnf(smp, smp.Generation, kcmv1.StateManagementProviderFailedProvisionerCRDsEvent,
			"Failed to ensure provisioner CRDs for %s %s: %v", kcmv1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), reconcileErr)
		err = errors.Join(err, reconcileErr)
	}
	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.timeFunc == nil {
		r.timeFunc = time.Now
	}
	r.config = mgr.GetConfig()

	r.discoveryClientFunc = func(config *rest.Config) (discovery.DiscoveryInterface, error) {
		return discovery.NewDiscoveryClientForConfig(config)
	}
	r.dynamicClientFunc = func(config *rest.Config) (dynamic.Interface, error) {
		return dynamic.NewForConfig(config)
	}

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			MaxConcurrentReconciles: 10,
			RateLimiter:             ratelimit.DefaultFastSlow(),
		}).
		For(&kcmv1.StateManagementProvider{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.ClusterRole{}).
		Owns(&rbacv1.ClusterRoleBinding{}).
		Complete(r)
}

// ensureRBAC ensures that ClusterRole and ServiceAccount exist for the StateManagementProvider.
func (r *Reconciler) ensureRBAC(ctx context.Context, smp *kcmv1.StateManagementProvider) error {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring RBAC exists")
	rbacCondition, _ := findCondition(smp, kcmv1.StateManagementProviderRBACCondition)

	status := metav1.ConditionFalse
	reason := kcmv1.StateManagementProviderRBACNotReadyReason
	message := kcmv1.StateManagementProviderRBACNotReadyMessage

	defer func() {
		if updateCondition(smp, rbacCondition, status, reason, message, r.timeFunc()) && status == metav1.ConditionTrue {
			l.Info("Successfully ensured RBAC")
			record.Eventf(smp, smp.Generation, kcmv1.StateManagementProviderSuccessRBACEvent,
				"Successfully ensured RBAC for %s %s", kcmv1.StateManagementProviderKind, client.ObjectKeyFromObject(smp))
		}
		l.V(1).Info("Finished ensuring RBAC", "duration", time.Since(start))
	}()

	adapterGVR, err := r.gvrFromResourceReference(ctx, r.config, smp.Spec.Adapter)
	if err != nil {
		reason = kcmv1.StateManagementProviderRBACFailedToGetGVKForAdapterReason
		message = fmt.Sprintf("Failed to ensure RBAC: %v", err)
		return fmt.Errorf("failed to ensure RBAC: %w", err)
	}
	gvrList := []schema.GroupVersionResource{adapterGVR}

	for _, provisioner := range smp.Spec.Provisioner {
		provisionerGVR, err := r.gvrFromResourceReference(ctx, r.config, provisioner)
		if err != nil {
			reason = kcmv1.StateManagementProviderRBACFailedToGetGVKForProvisionerReason
			message = fmt.Sprintf("Failed to ensure RBAC: %v", err)
			return fmt.Errorf("failed to ensure RBAC: %w", err)
		}
		gvrList = append(gvrList, provisionerGVR)
	}

	for _, gvr := range smp.Spec.ProvisionerCRDs {
		for _, res := range gvr.Resources {
			gvrList = append(gvrList, schema.GroupVersionResource{
				Group:    gvr.Group,
				Version:  gvr.Version,
				Resource: res,
			})
		}
	}
	rbacRules := buildRBACRules(gvrList)
	l.V(1).Info("Resulting rule set", "rules", rbacRules)

	if err = r.ensureClusterRole(ctx, smp, rbacRules); err != nil {
		reason = kcmv1.StateManagementProviderRBACFailedToEnsureClusterRoleReason
		message = fmt.Sprintf("Failed to ensure RBAC: %v", err)
		return fmt.Errorf("failed to ensure RBAC: %w", err)
	}
	if err = r.ensureServiceAccount(ctx, smp); err != nil {
		reason = kcmv1.StateManagementProviderRBACFailedToEnsureServiceAccountReason
		message = fmt.Sprintf("Failed to ensure RBAC: %v", err)
		return fmt.Errorf("failed to ensure RBAC: %w", err)
	}
	if err = r.ensureClusterRoleBinding(ctx, smp); err != nil {
		reason = kcmv1.StateManagementProviderRBACFailedToEnsureClusterRoleBindingReason
		message = fmt.Sprintf("Failed to ensure RBAC: %v", err)
		return fmt.Errorf("failed to ensure RBAC: %w", err)
	}

	status = metav1.ConditionTrue
	reason = kcmv1.StateManagementProviderRBACReadyReason
	message = kcmv1.StateManagementProviderRBACReadyMessage
	return nil
}

// ensureClusterRole ensures that the ClusterRole exists for the StateManagementProvider.
func (r *Reconciler) ensureClusterRole(ctx context.Context, smp *kcmv1.StateManagementProvider, rules []rbacv1.PolicyRule) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring ClusterRole exists and up-to-date")

	clusterRole := new(rbacv1.ClusterRole)
	key := client.ObjectKey{Name: smp.Name + clusterRoleSuffix}
	err := r.Get(ctx, key, clusterRole)
	// IgnoreNotFound returns nil in either case when error is nil
	// or when the error occurred due to object was not found.
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to ensure ClusterRole for %s %s: %w", kcmv1.StateManagementProviderKind, key, err)
	}

	switch {
	// non-nil err means that the error occurred due to object was not found,
	// therefore the clusterRole object is empty on this step.
	case err != nil:
		clusterRole = &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: smp.Name + clusterRoleSuffix,
			},
			Rules: rules,
		}
		err = r.Create(ctx, clusterRole)
	// if the existing rules are not equal to desired rules,
	// then we need to update the ClusterRole
	case !equality.Semantic.DeepEqual(clusterRole.Rules, rules):
		clusterRole.Rules = rules
		err = r.Update(ctx, clusterRole)
	}

	if err != nil {
		return fmt.Errorf("failed to ensure ClusterRole for %s %s: %w", kcmv1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
	}
	l.V(1).Info("Ensured ClusterRole", "cluster_role", client.ObjectKeyFromObject(clusterRole))
	return nil
}

// ensureServiceAccount ensures that the ServiceAccount exists for the StateManagementProvider.
func (r *Reconciler) ensureServiceAccount(ctx context.Context, smp *kcmv1.StateManagementProvider) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring ServiceAccount exists")

	sa := new(corev1.ServiceAccount)
	key := client.ObjectKey{Namespace: r.SystemNamespace, Name: smp.Name + serviceAccountSuffix}
	err := r.Get(ctx, key, sa)
	// IgnoreNotFound returns nil in either case when error is nil
	// or when the error occurred due to object was not found.
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to ensure ServieAccount for %s %s: %w", kcmv1.StateManagementProviderKind, key, err)
	}

	desiredSA := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      smp.Name + serviceAccountSuffix,
			Namespace: r.SystemNamespace,
		},
	}

	// we do not care about discrepancy in metadata as user may annotate or label produced objects,
	// hence we need only to ensure that SA configuration was not changed.
	saIsUpToDate := sa.AutomountServiceAccountToken == desiredSA.AutomountServiceAccountToken &&
		equality.Semantic.DeepEqualWithNilDifferentFromEmpty(sa.Secrets, desiredSA.Secrets) &&
		equality.Semantic.DeepEqualWithNilDifferentFromEmpty(sa.ImagePullSecrets, desiredSA.ImagePullSecrets)

	switch {
	// non-nil err means that the error occurred due to object was not found,
	// therefore the clusterRole object is empty on this step.
	case err != nil:
		err = r.Create(ctx, desiredSA)
	// if the existing SA configuration is not equal to desired configuration,
	// then we need to update the ServiceAccount
	case !saIsUpToDate:
		sa.AutomountServiceAccountToken = nil
		sa.Secrets = nil
		sa.ImagePullSecrets = nil
		err = r.Update(ctx, sa)
	}

	if err != nil {
		return fmt.Errorf("failed to ensure ServiceAccount for %s %s: %w", kcmv1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
	}
	l.V(1).Info("Ensured ServiceAccount", "service_account", key)
	return nil
}

// ensureClusterRoleBinding ensures that the ClusterRoleBinding exists for the StateManagementProvider.
func (r *Reconciler) ensureClusterRoleBinding(ctx context.Context, smp *kcmv1.StateManagementProvider) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring ClusterRoleBinding exists")

	binding := new(rbacv1.ClusterRoleBinding)
	key := client.ObjectKey{Name: smp.Name + clusterRoleBindingSuffix}
	err := r.Get(ctx, key, binding)
	// IgnoreNotFound returns nil in either case when error is nil
	// or when the error occurred due to object was not found.
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to ensure ClusterRoleBinding for %s %s: %w", kcmv1.StateManagementProviderKind, key, err)
	}

	desiredBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: smp.Name + clusterRoleBindingSuffix,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     smp.Name + clusterRoleSuffix,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      smp.Name + serviceAccountSuffix,
				Namespace: r.SystemNamespace,
			},
		},
	}

	switch {
	// non-nil err means that the error occurred due to object was not found,
	// therefore the clusterRole object is empty on this step.
	case err != nil:
		err = r.Create(ctx, desiredBinding)
	// if the ClusterRoleBinding with expected name contains wrong RoleRef
	// we'll just return an error and it should be handled manually by ClusterRoleBinding deletion
	// due to RoleRef immutability
	case !equality.Semantic.DeepEqual(binding.RoleRef, desiredBinding.RoleRef):
		return fmt.Errorf("existing ClusterRoleBinding %s defines unexpected immutable RoleRef", key)
	case !equality.Semantic.DeepEqual(binding.Subjects, desiredBinding.Subjects):
		binding.Subjects = desiredBinding.Subjects
		err = r.Update(ctx, binding)
	}

	if err != nil {
		return fmt.Errorf("failed to ensure ClusterRoleBinding for %s %s: %w", kcmv1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
	}
	l.V(1).Info("Ensured ClusterRoleBinding", "cluster_role_binding", client.ObjectKeyFromObject(binding))
	return nil
}

// ensureAdapter ensures that the adapter exists and ready.
func (r *Reconciler) ensureAdapter(ctx context.Context, config *rest.Config, smp *kcmv1.StateManagementProvider) error {
	start := r.timeFunc()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring adapter exists and ready")
	adapterCondition, _ := findCondition(smp, kcmv1.StateManagementProviderAdapterCondition)

	status := metav1.ConditionFalse
	reason := kcmv1.StateManagementProviderAdapterNotReadyReason
	message := kcmv1.StateManagementProviderAdapterNotReadyMessage

	defer func() {
		if updateCondition(smp, adapterCondition, status, reason, message, r.timeFunc()) && status == metav1.ConditionTrue {
			l.Info("Successfully ensured adapter")
			record.Eventf(smp, smp.Generation, kcmv1.StateManagementProviderSuccessAdapterEvent,
				"Successfully ensured adapter for %s %s", kcmv1.StateManagementProviderKind, client.ObjectKeyFromObject(smp))
		}
		l.V(1).Info("Finished ensuring adapter", "duration", time.Since(start))
	}()

	adapter, err := r.getReferencedObject(ctx, config, smp.Spec.Adapter)
	if err != nil {
		reason = kcmv1.StateManagementProviderFailedToGetResourceReason
		message = fmt.Sprintf("Failed to get adapter object: %v", err)
		return fmt.Errorf("failed to get adapter object: %w", err)
	}
	l.V(1).Info("Evaluating readiness of the adapter", "rule", smp.Spec.Adapter.ReadinessRule)
	ready, err := evaluateReadiness(adapter, smp.Spec.Adapter.ReadinessRule)
	if err != nil {
		reason = kcmv1.StateManagementProviderFailedToEvaluateReadinessReason
		message = fmt.Sprintf("Failed to evaluate adapter readiness: %v", err)
		return fmt.Errorf("failed to evaluate adapter readiness: %w", err)
	}
	if ready {
		status = metav1.ConditionTrue
		reason = kcmv1.StateManagementProviderAdapterReadyReason
		message = kcmv1.StateManagementProviderAdapterReadyMessage
	}
	return nil
}

// ensureProvisioner ensures that the provisioner-related resources exist and ready.
func (r *Reconciler) ensureProvisioner(ctx context.Context, config *rest.Config, smp *kcmv1.StateManagementProvider) error {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring provisioners exist and ready")
	provisionerCondition, _ := findCondition(smp, kcmv1.StateManagementProviderProvisionerCondition)

	status := metav1.ConditionFalse
	reason := kcmv1.StateManagementProviderProvisionerNotReadyReason
	message := kcmv1.StateManagementProviderProvisionerFailedMessage

	defer func() {
		if updateCondition(smp, provisionerCondition, status, reason, message, r.timeFunc()) && status == metav1.ConditionTrue {
			l.Info("Successfully ensured provisioner")
			record.Eventf(smp, smp.Generation, kcmv1.StateManagementProviderSuccessProvisionerEvent,
				"Successfully ensured provisioner for %s %s", kcmv1.StateManagementProviderKind, client.ObjectKeyFromObject(smp))
		}
		l.V(1).Info("Finished ensuring provisioner", "duration", time.Since(start))
	}()

	var (
		provisioner *unstructured.Unstructured
		ready       bool
		err         error
	)
	provisionersReady := true
	for _, item := range smp.Spec.Provisioner {
		provisioner, err = r.getReferencedObject(ctx, config, item)
		if err != nil {
			reason = kcmv1.StateManagementProviderFailedToGetResourceReason
			message = fmt.Sprintf("Failed to get provisioner object: %v", err)
			return fmt.Errorf("failed to get provisioner object: %w", err)
		}
		l.V(1).Info("Evaluating readiness of the provisioner", "rule", item.ReadinessRule)
		ready, err = evaluateReadiness(provisioner, item.ReadinessRule)
		if err != nil {
			reason = kcmv1.StateManagementProviderFailedToEvaluateReadinessReason
			message = fmt.Sprintf("Failed to evaluate provisioner readiness: %v", err)
			return fmt.Errorf("failed to evaluate provisioner readiness: %w", err)
		}
		provisionersReady = provisionersReady && ready
	}
	if provisionersReady {
		status = metav1.ConditionTrue
		reason = kcmv1.StateManagementProviderProvisionerReadyReason
		message = kcmv1.StateManagementProviderProvisionerReadyMessage
	}
	return err
}

// ensureProvisionerCRDs ensures that the desired provisioner-specific CRDs exist.
func (r *Reconciler) ensureProvisionerCRDs(ctx context.Context, config *rest.Config, smp *kcmv1.StateManagementProvider) error {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring desired provisioner-specific CRDs exist")
	gvrCondition, _ := findCondition(smp, kcmv1.StateManagementProviderProvisionerCRDsCondition)

	status := metav1.ConditionFalse
	reason := kcmv1.StateManagementProviderProvisionerCRDsNotReadyReason
	message := kcmv1.StateManagementProviderProvisionerCRDsNotReadyMessage

	defer func() {
		if updateCondition(smp, gvrCondition, status, reason, message, r.timeFunc()) && status == metav1.ConditionTrue {
			l.Info("Successfully ensured provisioner CRDs")
			record.Eventf(smp, smp.Generation, kcmv1.StateManagementProviderSuccessProvisionerCRDsEvent,
				"Successfully ensured provisioner CRDs for %s %s", kcmv1.StateManagementProviderKind, client.ObjectKeyFromObject(smp))
		}
		l.V(1).Info("Finished ensuring provisioner CRDs", "duration", time.Since(start))
	}()

	var err error
	for _, gvr := range smp.Spec.ProvisionerCRDs {
		if err = validateProvisionerCRDs(ctx, config, gvr.Group, gvr.Version, gvr.Resources); err != nil {
			reason = kcmv1.StateManagementProviderProvisionerCRDsNotReadyReason
			message = fmt.Sprintf("Failed to validate provisioner CRDs: %v", err)
			return fmt.Errorf("failed to validate provisioner CRDs: %w", err)
		}
	}
	status = metav1.ConditionTrue
	reason = kcmv1.StateManagementProviderProvisionerCRDsReadyReason
	message = kcmv1.StateManagementProviderProvisionerCRDsReadyMessage
	return nil
}

// buildRBACRules builds the RBAC rules for the given GVRs.
func buildRBACRules(gvrList []schema.GroupVersionResource) []rbacv1.PolicyRule {
	apisMap := make(map[string]map[string]struct{})
	for _, gvr := range gvrList {
		if _, ok := apisMap[gvr.Group]; !ok {
			apisMap[gvr.Group] = make(map[string]struct{})
		}
		apisMap[gvr.Group][gvr.Resource] = struct{}{}
	}
	groupToResources := make(map[string][]string, len(apisMap))
	for group, resources := range apisMap {
		groupToResources[group] = make([]string, 0, len(resources))
		for resource := range resources {
			groupToResources[group] = append(groupToResources[group], resource)
		}
	}
	rules := make([]rbacv1.PolicyRule, 0, len(groupToResources)+1)
	for group, resources := range groupToResources {
		slices.Sort(resources)
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups: []string{group},
			Resources: resources,
			Verbs:     []string{"get", "list", "watch"},
		})
	}
	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{apiExtensionsGroup},
		Resources: []string{apiExtensionsResource},
		Verbs:     []string{"get", "list", "watch"},
	})
	return rules
}

// getReferencedObject gets the referenced object from the cluster and returns it as an unstructured object.
func (r *Reconciler) getReferencedObject(ctx context.Context, config *rest.Config, ref kcmv1.ResourceReference) (*unstructured.Unstructured, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Getting referenced object", "ref", ref)
	gvr, err := r.gvrFromResourceReference(ctx, config, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get GVK from resource reference %s: %w", ref, err)
	}
	c, err := r.dynamicClientFunc(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	var dyn dynamic.ResourceInterface
	dyn = c.Resource(gvr)
	if ref.Namespace != "" {
		dyn = c.Resource(gvr).Namespace(ref.Namespace)
	}
	return dyn.Get(ctx, ref.Name, metav1.GetOptions{})
}

// gvrFromResourceReference returns the GVR for the given resource reference.
func (r *Reconciler) gvrFromResourceReference(ctx context.Context, config *rest.Config, ref kcmv1.ResourceReference) (schema.GroupVersionResource, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Getting GVR from resource reference", "resource_reference", ref)
	gvk := schema.GroupVersionKind{
		Kind: ref.Kind,
	}
	groupVersion := strings.Split(ref.APIVersion, "/")
	switch len(groupVersion) {
	case 1:
		gvk.Version = groupVersion[0]
	case 2:
		gvk.Group = groupVersion[0]
		gvk.Version = groupVersion[1]
	default:
		err := fmt.Errorf("invalid API version %s", ref.APIVersion)
		return schema.GroupVersionResource{}, fmt.Errorf("failed to get GVR from resource reference %s: %w", ref, err)
	}

	dc, err := r.discoveryClientFunc(config)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("failed to create discovery client: %w", err)
	}

	apiResources, err := restmapper.GetAPIGroupResources(dc)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("failed to get API group resources: %w", err)
	}

	mapper := restmapper.NewDiscoveryRESTMapper(apiResources)
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("failed to get REST mapping for %s: %w", gvk.String(), err)
	}
	l.V(1).Info("Found GVR", "gvr", mapping.Resource)
	return mapping.Resource, nil
}

// validateProvisionerCRDs validates that the given CRDs exist and contain the given version.
func validateProvisionerCRDs(ctx context.Context, config *rest.Config, group, version string, resources []string) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Validating resources", "group", group, "version", version, "resources", resources)
	if group == "" || version == "" || len(resources) == 0 {
		return errors.New("invalid GVR")
	}
	c, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}
	dyn := c.Resource(schema.GroupVersionResource{
		Group:    apiExtensionsGroup,
		Version:  apiExtensionsVersion,
		Resource: apiExtensionsResource,
	})
	for _, resource := range resources {
		name := fmt.Sprintf("%s.%s", resource, group)
		crd := new(apiextv1.CustomResourceDefinition)
		obj, err := dyn.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get CRD %s: %w", name, err)
		}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.UnstructuredContent(), crd)
		if err != nil {
			return errors.New("failed to convert unstructured CRD to typed CRD object")
		}
		if !slices.ContainsFunc(crd.Spec.Versions, func(c apiextv1.CustomResourceDefinitionVersion) bool {
			return c.Name == version
		}) {
			return fmt.Errorf("version %s not found in CRD %s", version, name)
		}
	}
	return nil
}

// evaluateReadiness evaluates the readiness of the given object using the given CEL rule.
func evaluateReadiness(obj *unstructured.Unstructured, rule string) (bool, error) {
	if rule == "" {
		return true, nil
	}

	env, err := cel.NewEnv(cel.VariableDecls(decls.NewVariable("self", types.NewMapType(types.StringType, types.DynType))))
	if err != nil {
		return false, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	ast, issues := env.Compile(rule)
	if issues != nil && issues.Err() != nil {
		return false, fmt.Errorf("failed to compile CEL expression: %w", issues.Err())
	}

	program, err := env.Program(ast, cel.EvalOptions(cel.OptOptimize))
	if err != nil {
		return false, fmt.Errorf("failed to create CEL program: %w", err)
	}

	objectMap := obj.UnstructuredContent()
	out, _, err := program.Eval(map[string]any{"self": objectMap})
	if err != nil {
		return false, fmt.Errorf("failed to evaluate CEL expression: %w", err)
	}
	result, ok := out.Value().(bool)
	if !ok {
		return false, errors.New("CEL expression did not return a boolean value")
	}
	return result, nil
}

// impersonationConfigForServiceAccount returns a rest.Config that can be used to impersonate the service account for the StateManagementProvider.
func impersonationConfigForServiceAccount(config *rest.Config, name, namespace string) *rest.Config {
	impersonationConfig := rest.CopyConfig(config)
	impersonationConfig.Impersonate = rest.ImpersonationConfig{
		UserName: "system:serviceaccount:" + namespace + ":" + name + serviceAccountSuffix,
	}
	return impersonationConfig
}

// fillConditions fills absent conditions of the StateManagementProvider.
func fillConditions(smp *kcmv1.StateManagementProvider, now time.Time) {
	if smp.Status.Conditions == nil {
		smp.Status.Conditions = []metav1.Condition{}
	}

	if condition, created := findCondition(smp, kcmv1.StateManagementProviderRBACCondition); created {
		updateCondition(smp, condition, metav1.ConditionUnknown,
			kcmv1.StateManagementProviderRBACUnknownReason, emptyConditionMessage, now)
	}
	if condition, created := findCondition(smp, kcmv1.StateManagementProviderAdapterCondition); created {
		updateCondition(smp, condition, metav1.ConditionUnknown,
			kcmv1.StateManagementProviderAdapterUnknownReason, emptyConditionMessage, now)
	}
	if condition, created := findCondition(smp, kcmv1.StateManagementProviderProvisionerCondition); created {
		updateCondition(smp, condition, metav1.ConditionUnknown,
			kcmv1.StateManagementProviderProvisionerUnknownReason, emptyConditionMessage, now)
	}
	if condition, created := findCondition(smp, kcmv1.StateManagementProviderProvisionerCRDsCondition); created {
		updateCondition(smp, condition, metav1.ConditionUnknown,
			kcmv1.StateManagementProviderProvisionerCRDsUnknownReason, emptyConditionMessage, now)
	}
}

// findCondition finds the condition of the given type in the StateManagementProvider.
// If no condition is found, a new condition of given type is created.
func findCondition(smp *kcmv1.StateManagementProvider, conditionType string) (metav1.Condition, bool) {
	var created bool
	condition := apimeta.FindStatusCondition(smp.Status.Conditions, conditionType)
	if condition == nil {
		condition = &metav1.Condition{Type: conditionType, ObservedGeneration: smp.Generation}
		created = true
	}
	return *condition, created
}

// updateCondition updates the given condition of the StateManagementProvider.
func updateCondition(
	smp *kcmv1.StateManagementProvider,
	condition metav1.Condition,
	status metav1.ConditionStatus,
	reason string,
	message string,
	transitionTime time.Time,
) bool {
	if condition.Status != status || condition.Reason != reason || condition.Message != message {
		condition.LastTransitionTime = metav1.NewTime(transitionTime)
	}
	condition.ObservedGeneration = smp.Generation
	condition.Status = status
	condition.Reason = reason
	condition.Message = message
	return apimeta.SetStatusCondition(&smp.Status.Conditions, condition)
}
