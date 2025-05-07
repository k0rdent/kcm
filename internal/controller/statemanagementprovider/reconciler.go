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
	"github.com/google/cel-go/checker/decls"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
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
)

// Reconciler reconciles a StateManagementProvider object
type Reconciler struct {
	client.Client

	config          *rest.Config
	timeFunc        func() time.Time
	SystemNamespace string
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling StateManagementProvider", "namespaced_name", req.NamespacedName)

	smp := new(kcmv1beta1.StateManagementProvider)
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

	if smp.Status.Conditions == nil {
		smp.Status.Conditions = []metav1.Condition{}
	}

	var reconcileErr error
	defer func() {
		smp.Status.Valid = len(smp.Status.Conditions) > 0 && !slices.ContainsFunc(smp.Status.Conditions, func(c metav1.Condition) bool {
			return c.Status == metav1.ConditionFalse
		})
		err = errors.Join(reconcileErr, r.Status().Update(ctx, smp))
	}()

	if reconcileErr = r.ensureRBAC(ctx, smp); reconcileErr != nil {
		record.Warnf(smp, smp.Generation, kcmv1beta1.StateManagementProviderFailedRBACEvent,
			"Failed to ensure RBAC for %s: %v", client.ObjectKeyFromObject(smp), reconcileErr)
		return ctrl.Result{}, reconcileErr
	}
	config := r.impersonatedConfigForServiceAccount(ctx, smp)
	if reconcileErr = r.ensureAdapter(ctx, config, smp); reconcileErr != nil {
		record.Warnf(smp, smp.Generation, kcmv1beta1.StateManagementProviderFailedAdapterEvent,
			"Failed to ensure adapter for %s: %v", client.ObjectKeyFromObject(smp), reconcileErr)
		return ctrl.Result{}, reconcileErr
	}
	if reconcileErr = r.ensureProvisioners(ctx, config, smp); reconcileErr != nil {
		record.Warnf(smp, smp.Generation, kcmv1beta1.StateManagementProviderFailedProvisionersEvent,
			"Failed to ensure provisioners for %s: %v", client.ObjectKeyFromObject(smp), reconcileErr)
		return ctrl.Result{}, reconcileErr
	}
	if reconcileErr = r.ensureGVKs(ctx, config, smp); reconcileErr != nil {
		record.Warnf(smp, smp.Generation, kcmv1beta1.StateManagementProviderFailedGVREvent,
			"Failed to ensure GVRs for %s: %v", client.ObjectKeyFromObject(smp), reconcileErr)
		return ctrl.Result{}, reconcileErr
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.timeFunc == nil {
		r.timeFunc = time.Now
	}
	r.config = mgr.GetConfig()

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			MaxConcurrentReconciles: 10,
			RateLimiter:             ratelimit.DefaultFastSlow(),
		}).
		For(&kcmv1beta1.StateManagementProvider{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.ClusterRole{}).
		Owns(&rbacv1.ClusterRoleBinding{}).
		Complete(r)
}

// gvrFromResourceReference returns the GVR for the given resource reference.
func (r *Reconciler) gvrFromResourceReference(ctx context.Context, ref kcmv1beta1.ResourceReference) (schema.GroupVersionResource, error) {
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

	dc, err := discovery.NewDiscoveryClientForConfig(r.config)
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

// ensureRBAC ensures that ClusterRole and ServiceAccount exist for the StateManagementProvider.
func (r *Reconciler) ensureRBAC(ctx context.Context, smp *kcmv1beta1.StateManagementProvider) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring RBAC exists")
	rbacCondition := apimeta.FindStatusCondition(smp.Status.Conditions, kcmv1beta1.StateManagementProviderRBACCondition)

	status := metav1.ConditionFalse
	reason := kcmv1beta1.StateManagementProviderRBACFailedReason
	message := kcmv1beta1.StateManagementProviderRBACFailedMessage

	defer func() {
		if rbacCondition == nil {
			rbacCondition = &metav1.Condition{
				Type:               kcmv1beta1.StateManagementProviderRBACCondition,
				ObservedGeneration: smp.Generation,
			}
		}
		condition := buildCondition(rbacCondition, status, reason, message, r.timeFunc())
		apimeta.SetStatusCondition(&smp.Status.Conditions, *condition)
	}()

	apisMap := make(map[string]map[string]struct{})
	adapterGVR, err := r.gvrFromResourceReference(ctx, smp.Spec.Adapter)
	if err != nil {
		message = fmt.Sprintf("failed to ensure RBAC for %s %s: %v", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
		return fmt.Errorf("failed to ensure RBAC for %s %s: %w", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
	}
	apisMap[adapterGVR.Group] = map[string]struct{}{adapterGVR.Resource: {}}

	for _, provisioner := range smp.Spec.Provisioner {
		provisionerGVR, err := r.gvrFromResourceReference(ctx, provisioner)
		if err != nil {
			message = fmt.Sprintf("failed to ensure RBAC for %s %s: %v", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
			return fmt.Errorf("failed to ensure RBAC for %s %s: %w", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
		}
		if _, ok := apisMap[provisionerGVR.Group]; !ok {
			apisMap[provisionerGVR.Group] = make(map[string]struct{})
		}
		apisMap[adapterGVR.Group][provisionerGVR.Resource] = struct{}{}
	}
	for _, gvr := range smp.Spec.ProvisionerCRDs {
		if _, ok := apisMap[gvr.Group]; !ok {
			apisMap[gvr.Group] = make(map[string]struct{})
		}
		for _, resource := range gvr.Resources {
			apisMap[gvr.Group][resource] = struct{}{}
		}
	}
	groupToResources := make(map[string][]string, len(apisMap))
	for group, resources := range apisMap {
		groupToResources[group] = make([]string, 0, len(resources))
		for resource := range resources {
			groupToResources[group] = append(groupToResources[group], resource)
		}
	}

	if err = r.ensureClusterRole(ctx, smp, groupToResources); err != nil {
		message = fmt.Sprintf("failed to ensure RBAC for %s %s: %v", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
		return fmt.Errorf("failed to ensure RBAC for %s %s: %w", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
	}
	if err = r.ensureServiceAccount(ctx, smp); err != nil {
		message = fmt.Sprintf("failed to ensure RBAC for %s %s: %v", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
		return fmt.Errorf("failed to ensure RBAC for %s %s: %w", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
	}
	if err = r.ensureClusterRoleBinding(ctx, smp); err != nil {
		message = fmt.Sprintf("failed to ensure RBAC for %s %s: %v", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
		return fmt.Errorf("failed to ensure RBAC for %s %s: %w", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
	}

	status = metav1.ConditionTrue
	reason = kcmv1beta1.StateManagementProviderRBACSuccessReason
	message = kcmv1beta1.StateManagementProviderRBACSuccessMessage
	return nil
}

// ensureClusterRole ensures that the ClusterRole exists for the StateManagementProvider.
func (r *Reconciler) ensureClusterRole(ctx context.Context, smp *kcmv1beta1.StateManagementProvider, apisMap map[string][]string) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring ClusterRole exists")

	rules := make([]rbacv1.PolicyRule, 0, len(apisMap)+1)
	for group, resources := range apisMap {
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups: []string{group},
			Resources: resources,
			Verbs:     []string{"get", "list", "watch"},
		})
	}
	l.V(1).Info("Resulting rule set", "rules", rules)

	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{apiExtensionsGroup},
		Resources: []string{apiExtensionsResource},
		Verbs:     []string{"get", "list", "watch"},
	})
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: smp.Name + clusterRoleSuffix,
		},
		Rules: rules,
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, clusterRole, func() error {
		return ctrl.SetControllerReference(smp, clusterRole, r.Scheme())
	})
	if err != nil {
		return fmt.Errorf("failed to ensure ClusterRole for %s %s: %w", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
	}
	l.V(1).Info("Ensured ClusterRole", "operation", op, "cluster_role", client.ObjectKeyFromObject(clusterRole))

	return nil
}

// ensureServiceAccount ensures that the ServiceAccount exists for the StateManagementProvider.
func (r *Reconciler) ensureServiceAccount(ctx context.Context, smp *kcmv1beta1.StateManagementProvider) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring ServiceAccount exists")

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      smp.Name + serviceAccountSuffix,
			Namespace: r.SystemNamespace,
		},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return ctrl.SetControllerReference(smp, sa, r.Scheme())
	})
	if err != nil {
		return fmt.Errorf("failed to ensure ServiceAccount for %s %s: %w", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
	}
	l.V(1).Info("Ensured ServiceAccount", "operation", op, "service_account", client.ObjectKeyFromObject(sa))
	return nil
}

// ensureClusterRoleBinding ensures that the ClusterRoleBinding exists for the StateManagementProvider.
func (r *Reconciler) ensureClusterRoleBinding(ctx context.Context, smp *kcmv1beta1.StateManagementProvider) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring ClusterRoleBinding exists")

	binding := &rbacv1.ClusterRoleBinding{
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
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
		return ctrl.SetControllerReference(smp, binding, r.Scheme())
	})
	if err != nil {
		return fmt.Errorf("failed to ensure ClusterRoleBinding for %s %s: %w", kcmv1beta1.StateManagementProviderKind, client.ObjectKeyFromObject(smp), err)
	}
	l.V(1).Info("Ensured ClusterRoleBinding", "operation", op, "cluster_role_binding", client.ObjectKeyFromObject(binding))
	return nil
}

// impersonatedConfigForServiceAccount returns a rest.Config that can be used to impersonate the service account for the StateManagementProvider.
func (r *Reconciler) impersonatedConfigForServiceAccount(ctx context.Context, smp *kcmv1beta1.StateManagementProvider) *rest.Config {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Getting impersonated client for service account")

	impersonationConfig := rest.CopyConfig(r.config)
	impersonationConfig.Impersonate = rest.ImpersonationConfig{
		UserName: "system:serviceaccount:" + r.SystemNamespace + ":" + smp.Name + serviceAccountSuffix,
	}
	return impersonationConfig
}

// ensureAdapter ensures that the adapter exists and is running.
func (r *Reconciler) ensureAdapter(ctx context.Context, config *rest.Config, smp *kcmv1beta1.StateManagementProvider) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring adapter exists and is running")
	adapterCondition := apimeta.FindStatusCondition(smp.Status.Conditions, kcmv1beta1.StateManagementProviderAdapterCondition)

	status := metav1.ConditionFalse
	reason := kcmv1beta1.StateManagementProviderAdapterFailedReason
	message := kcmv1beta1.StateManagementProviderAdapterFailedMessage

	defer func() {
		if adapterCondition == nil {
			adapterCondition = &metav1.Condition{
				Type:               kcmv1beta1.StateManagementProviderAdapterCondition,
				ObservedGeneration: smp.Generation,
			}
		}
		condition := buildCondition(adapterCondition, status, reason, message, r.timeFunc())
		apimeta.SetStatusCondition(&smp.Status.Conditions, *condition)
	}()

	adapter, err := r.getReferencedObject(ctx, config, smp.Spec.Adapter)
	if err != nil {
		reason = kcmv1beta1.StateManagementProviderAdapterFailedReason
		message = fmt.Sprintf("failed to get adapter object: %v", err)
		return fmt.Errorf("failed to get adapter object: %w", err)
	}
	ready, err := evaluateReadiness(ctx, adapter, smp.Spec.Adapter.ReadinessRule)
	if err != nil {
		reason = kcmv1beta1.StateManagementProviderAdapterFailedReason
		message = fmt.Sprintf("failed to evaluate adapter readiness: %v", err)
		return fmt.Errorf("failed to evaluate adapter readiness: %w", err)
	}
	if ready {
		status = metav1.ConditionTrue
		reason = kcmv1beta1.StateManagementProviderAdapterSuccessReason
		message = kcmv1beta1.StateManagementProviderAdapterSuccessMessage
	}
	return nil
}

// ensureProvisioners ensures that the provisioners exist and are running.
func (r *Reconciler) ensureProvisioners(ctx context.Context, config *rest.Config, smp *kcmv1beta1.StateManagementProvider) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring provisioners exist and are running")
	provisionersCondition := apimeta.FindStatusCondition(smp.Status.Conditions, kcmv1beta1.StateManagementProviderProvisionersCondition)

	status := metav1.ConditionFalse
	reason := kcmv1beta1.StateManagementProviderProvisionersFailedReason
	message := kcmv1beta1.StateManagementProviderProvisionersFailedMessage

	defer func() {
		if provisionersCondition == nil {
			provisionersCondition = &metav1.Condition{
				Type:               kcmv1beta1.StateManagementProviderProvisionersCondition,
				ObservedGeneration: smp.Generation,
			}
		}
		condition := buildCondition(provisionersCondition, status, reason, message, r.timeFunc())
		apimeta.SetStatusCondition(&smp.Status.Conditions, *condition)
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
			reason = kcmv1beta1.StateManagementProviderProvisionersFailedReason
			message = fmt.Sprintf("failed to get provisioner object: %v", err)
			break
		}
		ready, err = evaluateReadiness(ctx, provisioner, item.ReadinessRule)
		if err != nil {
			reason = kcmv1beta1.StateManagementProviderProvisionersFailedReason
			message = fmt.Sprintf("failed to evaluate provisioner readiness: %v", err)
			break
		}
		provisionersReady = provisionersReady && ready
	}
	if provisionersReady {
		status = metav1.ConditionTrue
		reason = kcmv1beta1.StateManagementProviderProvisionersSuccessReason
		message = kcmv1beta1.StateManagementProviderProvisionersSuccessMessage
	}
	return err
}

// ensureGVKs ensures that the desired GVKs exist.
func (r *Reconciler) ensureGVKs(ctx context.Context, config *rest.Config, smp *kcmv1beta1.StateManagementProvider) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring desired GVKs exist")
	gvrCondition := apimeta.FindStatusCondition(smp.Status.Conditions, kcmv1beta1.StateManagementProviderProvisionerCRDsCondition)

	status := metav1.ConditionFalse
	reason := kcmv1beta1.StateManagementProviderProvisionerCRDsFailedReason
	message := kcmv1beta1.StateManagementProviderProvisionerCRDsFailedMessage

	defer func() {
		if gvrCondition == nil {
			gvrCondition = &metav1.Condition{
				Type:               kcmv1beta1.StateManagementProviderProvisionerCRDsCondition,
				ObservedGeneration: smp.Generation,
			}
		}
		condition := buildCondition(gvrCondition, status, reason, message, r.timeFunc())
		apimeta.SetStatusCondition(&smp.Status.Conditions, *condition)
	}()

	var err error
	for _, gvr := range smp.Spec.ProvisionerCRDs {
		if err = validateResources(ctx, config, gvr.Group, gvr.Version, gvr.Resources); err != nil {
			reason = kcmv1beta1.StateManagementProviderProvisionerCRDsFailedReason
			message = fmt.Sprintf("failed to validate resources: %v", err)
			break
		}
	}
	if err == nil {
		status = metav1.ConditionTrue
		reason = kcmv1beta1.StateManagementProviderProvisionerCRDsSuccessReason
		message = kcmv1beta1.StateManagementProviderProvisionerCRDsSuccessMessage
	}
	return err
}

// getReferencedObject gets the referenced object from the cluster and returns it as an unstructured object.
func (r *Reconciler) getReferencedObject(ctx context.Context, config *rest.Config, ref kcmv1beta1.ResourceReference) (*unstructured.Unstructured, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Getting referenced object", "ref", ref)
	gvr, err := r.gvrFromResourceReference(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get GVK from resource reference %s: %w", ref, err)
	}
	c, err := dynamic.NewForConfig(config)
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

// validateResources validates the resources for the given GVR.
func validateResources(ctx context.Context, config *rest.Config, group, version string, resources []string) error {
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
		crd := new(apiextensionsv1.CustomResourceDefinition)
		obj, err := dyn.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get CRD %s: %w", name, err)
		}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.UnstructuredContent(), crd)
		if err != nil {
			return errors.New("failed to convert unstructured CRD to typed CRD object")
		}
		if !slices.ContainsFunc(crd.Spec.Versions, func(c apiextensionsv1.CustomResourceDefinitionVersion) bool {
			return c.Name == version
		}) {
			return fmt.Errorf("version %s not found in CRD %s", version, name)
		}
	}
	return nil
}

// evaluateReadiness evaluates the readiness of the given object using the given CEL rule.
func evaluateReadiness(ctx context.Context, obj *unstructured.Unstructured, rule string) (bool, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Evaluating readiness", "rule", rule)
	if rule == "" {
		return false, errors.New("no readiness rule specified")
	}

	env, err := cel.NewEnv(cel.Declarations(decls.NewVar("self", decls.NewMapType(decls.String, decls.Dyn))))
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

// buildCondition builds a condition from the given parameters.
func buildCondition(
	condition *metav1.Condition,
	status metav1.ConditionStatus,
	reason string,
	message string,
	transitionTime time.Time,
) *metav1.Condition {
	if condition.Status != status || condition.Reason != reason || condition.Message != message {
		condition.LastTransitionTime = metav1.NewTime(transitionTime)
	}
	condition.Status = status
	condition.Reason = reason
	condition.Message = message
	return condition
}
