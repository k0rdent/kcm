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
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/record"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	labelsutil "github.com/K0rdent/kcm/internal/util/labels"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
)

const (
	// defaultPollInterval is how often AccessManagement is periodically re-reconciled so that
	// changes to source objects of a referenced Kind (built-in or custom) get picked up promptly.
	// No per-GVK watch is registered for referenced Kinds: since the set of Kinds is unbounded and
	// only known at runtime, a simple poller is a much smaller mechanism than registering/tearing
	// down dynamic informers, at the cost of up-to-defaultPollInterval propagation latency.
	defaultPollInterval = 2 * time.Minute

	// accessManagementDynamicClusterRoleSuffix names the ClusterRole this controller
	// maintains to hold the RBAC rules it needs for whatever GVKs are currently referenced
	// across AccessManagement's Resources rules.
	accessManagementDynamicClusterRoleSuffix = "-resources"

	// aggregateToManagerLabelKey/Value must match the label the Helm chart's aggregation
	// ClusterRole selects on (templates/provider/kcm/templates/rbac/controller/roles.yaml), so
	// that rules granted here are folded into the controller-manager's own permissions without
	// any additional binding.
	aggregateToManagerLabelKey   = "k0rdent.mirantis.com/aggregate-to-manager"
	aggregateToManagerLabelValue = "true"
)

// errClusterScopedKindSkipped is returned by collectGVKResources for a cluster-scoped GVK. It's
// a sentinel, not a failure: callers must treat it as "this Kind was skipped, already logged as
// a warning" rather than joining it into the reconciliation's aggregate error.
var errClusterScopedKindSkipped = errors.New("cluster-scoped kind skipped")

// AccessManagementReconciler reconciles an AccessManagement object
type AccessManagementReconciler struct {
	client.Client

	// RESTMapper resolves a ResourceRule's APIVersion+Kind into a GroupVersionResource and its
	// scope (namespaced vs cluster-scoped). Defaults to the manager's RESTMapper in
	// SetupWithManager; overridable for tests.
	RESTMapper apimeta.RESTMapper

	// DynamicClient performs generic List/Get/Create/Delete against arbitrary GVRs, built-in
	// or custom CRD. Defaults to a client built from the manager's rest.Config in
	// SetupWithManager; overridable for tests (e.g. with a fake dynamic client).
	DynamicClient dynamic.Interface

	SystemNamespace string

	// pollInterval overrides defaultPollInterval; used by tests to avoid waiting on the real
	// interval. Zero means defaultPollInterval. Unexported: nothing outside this package should
	// need to override it, since it's purely an internal implementation detail of the poller.
	pollInterval time.Duration
}

// gvkResources holds the objects relevant to distributing a single GVK: the "system" objects
// read from SystemNamespace, keyed by name, and the "managed" copies already distributed into
// other namespaces by a previous reconciliation, used for cleanup.
type gvkResources struct {
	system  map[string]*unstructured.Unstructured
	managed []*unstructured.Unstructured
}

func (r *AccessManagementReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling AccessManagement")

	management := &kcmv1.Management{}
	if err := r.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, management); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Management: %w", err)
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping AccessManagement reconciliation")
		return ctrl.Result{}, nil
	}

	accessMgmt := &kcmv1.AccessManagement{}
	if err := r.Get(ctx, req.NamespacedName, accessMgmt); err != nil {
		l.Error(err, "unable to fetch AccessManagement")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if updated, err := labelsutil.AddKCMComponentLabel(ctx, r.Client, accessMgmt); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{}, err
	}

	errMsg := ""
	err := r.reconcileObj(ctx, accessMgmt)
	if err != nil {
		errMsg = err.Error()
	}
	accessMgmt.Status.ObservedGeneration = accessMgmt.Generation
	accessMgmt.Status.Error = errMsg

	if statusErr := r.updateStatus(ctx, accessMgmt); statusErr != nil {
		return ctrl.Result{}, errors.Join(err, statusErr)
	}

	if err != nil {
		return ctrl.Result{}, err
	}

	pollInterval := r.pollInterval
	if pollInterval == 0 {
		pollInterval = defaultPollInterval
	}

	return ctrl.Result{RequeueAfter: pollInterval}, nil
}

func (r *AccessManagementReconciler) reconcileObj(ctx context.Context, accessMgmt *kcmv1.AccessManagement) error {
	gvks, gvkErr := collectReferencedGVKs(accessMgmt)

	if err := r.ensureDynamicRBAC(ctx, accessMgmt, gvks); err != nil {
		return errors.Join(gvkErr, fmt.Errorf("failed to ensure RBAC for referenced resources: %w", err))
	}

	statuses := make(map[schema.GroupVersionKind]*kcmv1.ResourceKindStatus, len(gvks))
	resources := make(map[schema.GroupVersionKind]*gvkResources, len(gvks))
	keeper := make(map[schema.GroupVersionKind]map[string]bool, len(gvks))

	errs := gvkErr
	for _, gvk := range gvks {
		status := &kcmv1.ResourceKindStatus{APIVersion: gvk.GroupVersion().String(), Kind: gvk.Kind}
		statuses[gvk] = status
		keeper[gvk] = make(map[string]bool)

		res, err := r.collectGVKResources(ctx, accessMgmt, gvk)
		switch {
		case errors.Is(err, errClusterScopedKindSkipped):
			// Not a reconciliation failure (already logged/warned inside
			// collectGVKResources), but still surfaced per-Kind so status doesn't read as
			// "processed successfully" for a Kind that was actually never distributed.
			status.Error = fmt.Sprintf("skipped: %s is cluster-scoped and cannot be distributed by AccessManagement", gvk)
			continue
		case err != nil:
			status.Error = err.Error()
			errs = errors.Join(errs, fmt.Errorf("failed to collect resources for %s: %w", gvk, err))
			continue
		}
		resources[gvk] = res
	}

	for _, rule := range accessMgmt.Spec.AccessRules {
		namespaces, err := r.getTargetNamespaces(ctx, rule.TargetNamespaces)
		if err != nil {
			return errors.Join(errs, fmt.Errorf("failed to collect target namespaces: %w", err))
		}

		// EffectiveResources falls back to the deprecated one-field-per-Kind selectors when
		// Resources is empty, for backward compatibility when the mutating webhook that
		// normally migrates them on write is disabled/unavailable. When both are present,
		// Resources wins outright.
		effectiveResources := rule.EffectiveResources()

		for _, targetNamespace := range namespaces {
			for _, resRule := range effectiveResources {
				gvk, err := resRule.GroupVersionKind()
				if err != nil {
					errs = errors.Join(errs, err)
					continue
				}

				res, ok := resources[gvk]
				if !ok {
					// resource collection for this GVK either failed (recorded in status above)
					// or was skipped (e.g. cluster-scoped, already logged as a warning).
					continue
				}

				if err := r.processResourceRule(ctx, accessMgmt, resRule, gvk, targetNamespace, res, keeper[gvk]); err != nil {
					errs = errors.Join(errs, err)
					recordFirstError(statuses[gvk], err)
				}
			}
		}
	}

	for gvk, res := range resources {
		if err := r.cleanupManagedResources(ctx, accessMgmt, gvk, res.managed, keeper[gvk]); err != nil {
			errs = errors.Join(errs, err)
			recordFirstError(statuses[gvk], err)
		}
	}

	accessMgmt.Status.Resources = sortedResourceStatuses(statuses)

	if errs != nil {
		return errs
	}

	accessMgmt.Status.Current = accessMgmt.Spec.AccessRules
	return nil
}

func recordFirstError(status *kcmv1.ResourceKindStatus, err error) {
	if status != nil && status.Error == "" {
		status.Error = err.Error()
	}
}

func sortedResourceStatuses(statuses map[schema.GroupVersionKind]*kcmv1.ResourceKindStatus) []kcmv1.ResourceKindStatus {
	if len(statuses) == 0 {
		return nil
	}

	gvks := slices.Collect(maps.Keys(statuses))
	slices.SortFunc(gvks, func(a, b schema.GroupVersionKind) int {
		return cmp.Or(
			cmp.Compare(a.Group, b.Group),
			cmp.Compare(a.Version, b.Version),
			cmp.Compare(a.Kind, b.Kind),
		)
	})

	result := make([]kcmv1.ResourceKindStatus, 0, len(gvks))
	for _, gvk := range gvks {
		result = append(result, *statuses[gvk])
	}

	return result
}

// collectReferencedGVKs returns the de-duplicated set of GroupVersionKinds referenced across
// all of accessMgmt's AccessRules' EffectiveResources, in first-seen order.
func collectReferencedGVKs(accessMgmt *kcmv1.AccessManagement) ([]schema.GroupVersionKind, error) {
	seen := make(map[schema.GroupVersionKind]struct{})
	var (
		gvks []schema.GroupVersionKind
		errs error
	)

	for _, rule := range accessMgmt.Spec.AccessRules {
		for _, res := range rule.EffectiveResources() {
			gvk, err := res.GroupVersionKind()
			if err != nil {
				errs = errors.Join(errs, err)
				continue
			}

			if _, ok := seen[gvk]; ok {
				continue
			}
			seen[gvk] = struct{}{}
			gvks = append(gvks, gvk)
		}
	}

	return gvks, errs
}

// collectGVKResources resolves gvk to a GroupVersionResource and lists both the system objects
// (the only allowed source for distribution) and any already-managed copies. Returns
// errClusterScopedKindSkipped if gvk is cluster-scoped: that's not treated as a reconciliation
// failure, since a dynamically-referenced custom Kind having the wrong scope shouldn't fail the
// whole reconciliation — it's simply skipped (logged as a warning and surfaced via a Warning
// event) and every other resolvable Kind is still processed normally. Callers must check for
// this sentinel with errors.Is before treating a non-nil error as a real failure.
func (r *AccessManagementReconciler) collectGVKResources(ctx context.Context, accessMgmt *kcmv1.AccessManagement, gvk schema.GroupVersionKind) (*gvkResources, error) {
	mapping, err := r.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve %s (ensure the CRD is installed): %w", gvk, err)
	}

	if mapping.Scope.Name() != apimeta.RESTScopeNameNamespace {
		l := ctrl.LoggerFrom(ctx)
		l.Info("skipping cluster-scoped Kind: AccessManagement can only distribute namespaced Kinds", "gvk", gvk.String())
		r.warnf(accessMgmt, "ClusterScopedKindSkipped", "Skipping %s: cluster-scoped Kinds cannot be distributed by AccessManagement", gvk)
		return nil, errClusterScopedKindSkipped
	}

	gvr := mapping.Resource

	systemList, err := r.DynamicClient.Resource(gvr).Namespace(r.SystemNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list %s in namespace %s: %w", gvk, r.SystemNamespace, err)
	}

	system := make(map[string]*unstructured.Unstructured, len(systemList.Items))
	for i := range systemList.Items {
		system[systemList.Items[i].GetName()] = &systemList.Items[i]
	}

	managedList, err := r.DynamicClient.Resource(gvr).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		LabelSelector: kcmv1.KCMManagedLabelKey + "=" + kcmv1.KCMManagedLabelValue,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list managed %s: %w", gvk, err)
	}

	managed := make([]*unstructured.Unstructured, 0, len(managedList.Items))
	for i := range managedList.Items {
		if managedList.Items[i].GetNamespace() == r.SystemNamespace {
			continue
		}
		managed = append(managed, &managedList.Items[i])
	}

	return &gvkResources{system: system, managed: managed}, nil
}

func (r *AccessManagementReconciler) processResourceRule(
	ctx context.Context,
	accessMgmt *kcmv1.AccessManagement,
	rule kcmv1.ResourceRule,
	gvk schema.GroupVersionKind,
	targetNamespace string,
	res *gvkResources,
	keep map[string]bool,
) error {
	names, err := resolveResourceRuleNames(rule, res.system)
	if err != nil {
		return fmt.Errorf("failed to resolve names for %s: %w", gvk, err)
	}

	var errs error
	for _, name := range names {
		namespacedName := getNamespacedName(targetNamespace, name)
		keep[namespacedName] = true

		source, ok := res.system[name]
		if !ok {
			errs = errors.Join(errs, fmt.Errorf("%s %s/%s is not found", gvk.Kind, r.SystemNamespace, name))
			continue
		}

		created, err := r.createManagedObject(ctx, gvk, source, targetNamespace)
		if err != nil {
			r.warnf(accessMgmt, gvk.Kind+"CreationFailed", "Failed to create %s %s/%s: %v", gvk.Kind, targetNamespace, name, err)
			errs = errors.Join(errs, err)
			continue
		}

		if created {
			r.eventf(accessMgmt, gvk.Kind+"Created", "Successfully created %s %s/%s", gvk.Kind, targetNamespace, name)
		}
	}

	return errs
}

// resolveResourceRuleNames returns the object names in the system namespace selected by rule.
// Whether Names was set at all (rather than just non-empty) is what decides the branch taken:
// an explicitly-empty Names ([] rather than omitted) means "select nothing" and must not fall
// through to selector matching, or it would silently select every object of the Kind — the CEL
// validation on ResourceRule rejects a names-only rule with an empty list for the same reason,
// but this stays correct even for objects that predate that validation or an admission bypass.
// A non-empty Names is used verbatim (even for names not found in system, so callers can still
// report a clear "not found" error and keep the target from being deleted-then-recreated on the
// next reconcile). Only when Names is nil is Selector/StringSelector matched against the system
// objects' labels.
func resolveResourceRuleNames(rule kcmv1.ResourceRule, system map[string]*unstructured.Unstructured) ([]string, error) {
	if rule.Names != nil {
		return rule.Names, nil
	}

	selector, selectorNonEmpty, err := buildLabelSelector(rule.Selector, rule.StringSelector)
	if err != nil {
		return nil, fmt.Errorf("failed to construct selector: %w", err)
	}

	names := make([]string, 0, len(system))
	for name, obj := range system {
		if !selectorNonEmpty || selector.Matches(labels.Set(obj.GetLabels())) {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	return names, nil
}

// createManagedObject creates targetNamespace's managed copy of source, applying the built-in
// per-Kind namespace field rewrites where applicable. It returns created=false without error
// if the object already exists, matching the previous per-Kind behavior.
func (r *AccessManagementReconciler) createManagedObject(ctx context.Context, gvk schema.GroupVersionKind, source *unstructured.Unstructured, targetNamespace string) (created bool, _ error) {
	l := ctrl.LoggerFrom(ctx)

	if err := kubeutil.EnsureNamespace(ctx, r.Client, targetNamespace); err != nil {
		return false, fmt.Errorf("failed to ensure namespace %s: %w", targetNamespace, err)
	}

	target := source.DeepCopy()
	target.SetNamespace(targetNamespace)
	target.SetResourceVersion("")
	target.SetUID("")
	target.SetGeneration(0)
	target.SetCreationTimestamp(metav1.Time{})
	target.SetManagedFields(nil)
	target.SetOwnerReferences(nil)
	target.SetFinalizers(nil)
	target.SetAnnotations(nil)
	target.SetLabels(map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue})
	unstructured.RemoveNestedField(target.Object, "status")

	applyBuiltinNamespaceRewrite(gvk, target, source.GetNamespace())

	mapping, err := r.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return false, fmt.Errorf("failed to resolve %s: %w", gvk, err)
	}

	if _, err := r.DynamicClient.Resource(mapping.Resource).Namespace(targetNamespace).Create(ctx, target, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, err
	}

	l.Info(gvk.Kind+" was successfully created", "target namespace", targetNamespace, "source name", source.GetName())
	return true, nil
}

// applyBuiltinNamespaceRewrite applies the small, explicit table of per-Kind field rewrites for
// the three known built-in special cases; it is a no-op for any other GVK, including custom
// CRDs, which are copied verbatim.
func applyBuiltinNamespaceRewrite(gvk schema.GroupVersionKind, target *unstructured.Unstructured, sourceNamespace string) {
	switch gvk {
	case kcmv1.GroupVersion.WithKind(kcmv1.CredentialKind):
		// Credential.Spec.IdentityRef.Namespace: when the source object points somewhere in
		// particular, the copy should point at itself instead of the system namespace.
		rewriteNamespaceIfSet(target, target.GetNamespace(), "spec", "identityRef", "namespace")
	case kcmv1.GroupVersion.WithKind(kcmv1.ClusterAuthenticationKind):
		// ClusterAuthentication.Spec.CASecret.Namespace: when unset, the Secret is expected to
		// exist alongside the source object; a copy keeps referencing that same Secret.
		rewriteNamespaceIfEmpty(target, sourceNamespace, "spec", "caSecret", "namespace")
	case kcmv1.GroupVersion.WithKind(kcmv1.DataSourceKind):
		// DataSource.Spec.CertificateAuthority.Namespace: same rationale as CASecret above.
		rewriteNamespaceIfEmpty(target, sourceNamespace, "spec", "certificateAuthority", "namespace")
	}
}

func rewriteNamespaceIfSet(obj *unstructured.Unstructured, newNamespace string, fields ...string) {
	current, found, _ := unstructured.NestedString(obj.Object, fields...)
	if !found || current == "" {
		return
	}
	_ = unstructured.SetNestedField(obj.Object, newNamespace, fields...)
}

func rewriteNamespaceIfEmpty(obj *unstructured.Unstructured, sourceNamespace string, fields ...string) {
	_, parentFound, _ := unstructured.NestedMap(obj.Object, fields[:len(fields)-1]...)
	if !parentFound {
		return
	}

	current, _, _ := unstructured.NestedString(obj.Object, fields...)
	if current != "" {
		return
	}
	_ = unstructured.SetNestedField(obj.Object, sourceNamespace, fields...)
}

func (r *AccessManagementReconciler) cleanupManagedResources(ctx context.Context, accessMgmt *kcmv1.AccessManagement, gvk schema.GroupVersionKind, managedObjects []*unstructured.Unstructured, keep map[string]bool) error {
	var errs error
	for _, obj := range managedObjects {
		namespacedName := getNamespacedName(obj.GetNamespace(), obj.GetName())
		if keep[namespacedName] {
			continue
		}

		deleted, err := r.deleteManagedObject(ctx, gvk, obj)
		if err != nil {
			r.warnf(accessMgmt, gvk.Kind+"DeletionFailed", "Failed to delete %s %s: %v", gvk.Kind, namespacedName, err)
			errs = errors.Join(errs, err)
			continue
		}

		if deleted {
			r.eventf(accessMgmt, gvk.Kind+"Deleted", "Successfully deleted %s %s", gvk.Kind, namespacedName)
		}
	}
	return errs
}

func (r *AccessManagementReconciler) deleteManagedObject(ctx context.Context, gvk schema.GroupVersionKind, obj *unstructured.Unstructured) (deleted bool, _ error) {
	l := ctrl.LoggerFrom(ctx)

	mapping, err := r.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return false, fmt.Errorf("failed to resolve %s: %w", gvk, err)
	}

	if err := r.DynamicClient.Resource(mapping.Resource).Namespace(obj.GetNamespace()).Delete(ctx, obj.GetName(), metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	l.Info(gvk.Kind+" was successfully deleted", "namespace", obj.GetNamespace(), "name", obj.GetName())
	return true, nil
}

func getNamespacedName(namespace, name string) string {
	return namespace + "/" + name
}

func (r *AccessManagementReconciler) getTargetNamespaces(ctx context.Context, targetNamespaces kcmv1.TargetNamespaces) ([]string, error) {
	if len(targetNamespaces.List) > 0 {
		return targetNamespaces.List, nil
	}

	selector, selectorNonEmpty, err := buildLabelSelector(targetNamespaces.Selector, targetNamespaces.StringSelector)
	if err != nil {
		return nil, fmt.Errorf("failed to construct selector from target namespaces: %w", err)
	}

	var (
		namespaces = new(corev1.NamespaceList)
		listOpts   = new(client.ListOptions)
	)
	if selectorNonEmpty {
		listOpts.LabelSelector = selector
	}

	if err := r.List(ctx, namespaces, listOpts); err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	result := make([]string, len(namespaces.Items))
	for i, ns := range namespaces.Items {
		result[i] = ns.Name
	}

	return result, nil
}

func (r *AccessManagementReconciler) updateStatus(ctx context.Context, accessMgmt *kcmv1.AccessManagement) error {
	if err := r.Status().Update(ctx, accessMgmt); err != nil {
		return fmt.Errorf("failed to update status for AccessManagement %s: %w", accessMgmt.Name, err)
	}
	return nil
}

func (r *AccessManagementReconciler) mapNamespaceToRequests(ctx context.Context, obj client.Object) []ctrl.Request {
	namespace, ok := obj.(*corev1.Namespace)
	if !ok || namespace == nil {
		return nil
	}

	l := ctrl.LoggerFrom(ctx).WithName("am-map-create")

	fallback := func() []ctrl.Request {
		return r.collectAccessManagementRequests(ctx, namespace.Name, func(am *kcmv1.AccessManagement) (bool, error) {
			return r.accessManagementTargetsNamespace(am, namespace)
		})
	}

	listTargetedAccessManagements, err := r.listAccessManagementByField(ctx, kcmv1.AccessManagementTargetNamespaceListIndexKey, namespace.Name)
	if err != nil {
		l.Error(err,
			"failed to list AccessManagement resources by namespace list index, falling back to full scan",
			"namespace", namespace.Name,
		)
		return fallback()
	}

	allNamespaceAccessManagements, err := r.listAccessManagementByField(ctx, kcmv1.AccessManagementTargetsAllNamespacesIndexKey, "true")
	if err != nil {
		l.Error(err,
			"failed to list AccessManagement resources by all-namespaces index, falling back to full scan",
			"namespace", namespace.Name,
		)
		return fallback()
	}

	selectorAccessManagements, err := r.listAccessManagementByField(ctx, kcmv1.AccessManagementUsesSelectorIndexKey, "true")
	if err != nil {
		l.Error(err,
			"failed to list AccessManagement resources by selector index, falling back to full scan",
			"namespace", namespace.Name,
		)
		return fallback()
	}

	candidateCount := len(listTargetedAccessManagements) + len(allNamespaceAccessManagements) + len(selectorAccessManagements)
	requests := make([]ctrl.Request, 0, candidateCount)
	enqueued := make(map[string]struct{}, candidateCount)
	enqueueRequest := func(am *kcmv1.AccessManagement) {
		if _, ok := enqueued[am.Name]; ok {
			return
		}

		enqueued[am.Name] = struct{}{}
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(am)})
	}

	for i := range listTargetedAccessManagements {
		enqueueRequest(&listTargetedAccessManagements[i])
	}

	for i := range allNamespaceAccessManagements {
		enqueueRequest(&allNamespaceAccessManagements[i])
	}

	for i := range selectorAccessManagements {
		am := &selectorAccessManagements[i]
		shouldEnqueue, selectorErr := r.accessManagementTargetsNamespace(am, namespace)
		if selectorErr != nil {
			l.Error(selectorErr,
				"failed to evaluate AccessManagement namespace selector",
				"accessManagement", am.Name,
				"namespace", namespace.Name,
			)
			// skip enqueue on invalid selector to avoid fan-out on namespace churn
			continue
		}

		if shouldEnqueue {
			enqueueRequest(am)
		}
	}

	return requests
}

func (r *AccessManagementReconciler) mapNamespaceLabelUpdateToRequests(ctx context.Context, oldObj, newObj client.Object) []ctrl.Request {
	oldNamespace, okOld := oldObj.(*corev1.Namespace)
	newNamespace, okNew := newObj.(*corev1.Namespace)
	if !okOld || !okNew || oldNamespace == nil || newNamespace == nil {
		return nil
	}

	l := ctrl.LoggerFrom(ctx).WithName("am-map-update")

	selectorAccessManagements, err := r.listAccessManagementByField(ctx, kcmv1.AccessManagementUsesSelectorIndexKey, "true")
	if err != nil {
		l.Error(err,
			"failed to list AccessManagement resources by selector index, falling back to full scan",
			"namespace", newNamespace.Name,
		)

		return r.collectAccessManagementRequests(ctx, newNamespace.Name, func(am *kcmv1.AccessManagement) (bool, error) {
			return r.accessManagementAffectedByNamespaceLabelUpdate(am, oldNamespace, newNamespace)
		})
	}

	requests := make([]ctrl.Request, 0, len(selectorAccessManagements))
	for i := range selectorAccessManagements {
		am := &selectorAccessManagements[i]
		affected, selectorErr := r.accessManagementAffectedByNamespaceLabelUpdate(am, oldNamespace, newNamespace)
		if selectorErr != nil {
			l.Error(selectorErr,
				"failed to evaluate AccessManagement namespace selector",
				"accessManagement", am.Name,
				"namespace", newNamespace.Name,
			)
			// skip enqueue on invalid selector to avoid fan-out on namespace churn
			continue
		}

		if affected {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(am)})
		}
	}

	return requests
}

func (r *AccessManagementReconciler) listAccessManagementByField(ctx context.Context, indexKey, value string) ([]kcmv1.AccessManagement, error) {
	accessManagements := new(kcmv1.AccessManagementList)
	if err := r.List(ctx, accessManagements, client.MatchingFields{indexKey: value}); err != nil {
		return nil, fmt.Errorf("failed to list AccessManagement resources by field %q=%q: %w", indexKey, value, err)
	}

	return accessManagements.Items, nil
}

func (r *AccessManagementReconciler) collectAccessManagementRequests(ctx context.Context, namespaceName string, shouldEnqueueFn func(*kcmv1.AccessManagement) (bool, error)) []ctrl.Request {
	l := ctrl.LoggerFrom(ctx)

	accessManagements := new(kcmv1.AccessManagementList)
	if err := r.List(ctx, accessManagements); err != nil {
		l.Error(err,
			"failed to list AccessManagement resources for Namespace event",
			"namespace", namespaceName,
		)
		return nil
	}

	requests := make([]ctrl.Request, 0, len(accessManagements.Items))
	for i := range accessManagements.Items {
		am := &accessManagements.Items[i]
		shouldEnqueue, err := shouldEnqueueFn(am)
		if err != nil {
			l.Error(err,
				"failed to evaluate AccessManagement namespace selector",
				"accessManagement", am.Name,
				"namespace", namespaceName,
			)
			// skip enqueue on invalid selector to avoid fan-out on namespace churn
			continue
		}

		if shouldEnqueue {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(am)})
		}
	}

	return requests
}

// buildLabelSelector is the single helper behind both TargetNamespaces and ResourceRule
// selector matching: a non-empty stringSelector takes precedence, then a structured selector.
// The returned bool reports whether the resulting selector is non-empty (an empty selector, or
// no selector at all, conventionally means "match everything" for both use sites).
func buildLabelSelector(selector *metav1.LabelSelector, stringSelector string) (labels.Selector, bool, error) {
	if stringSelector != "" {
		sel, err := labels.Parse(stringSelector)
		if err != nil {
			return nil, false, fmt.Errorf("failed to parse string selector %q: %w", stringSelector, err)
		}

		return sel, !sel.Empty(), nil
	}

	if selector == nil {
		return nil, false, nil
	}

	sel, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, false, fmt.Errorf("failed to convert selector: %w", err)
	}

	return sel, !sel.Empty(), nil
}

func (*AccessManagementReconciler) ruleTargetsNamespace(rule kcmv1.AccessRule, namespace *corev1.Namespace) (bool, error) {
	if len(rule.TargetNamespaces.List) > 0 {
		return slices.Contains(rule.TargetNamespaces.List, namespace.Name), nil
	}

	selector, selectorNonEmpty, err := buildLabelSelector(rule.TargetNamespaces.Selector, rule.TargetNamespaces.StringSelector)
	if err != nil {
		return false, fmt.Errorf("failed to get target namespaces selector: %w", err)
	}

	if !selectorNonEmpty {
		// empty selector means all namespaces
		return true, nil
	}

	return selector.Matches(labels.Set(namespace.GetLabels())), nil
}

func (r *AccessManagementReconciler) accessManagementTargetsNamespace(accessMgmt *kcmv1.AccessManagement, namespace *corev1.Namespace) (bool, error) {
	for _, rule := range accessMgmt.Spec.AccessRules {
		matches, err := r.ruleTargetsNamespace(rule, namespace)
		if err != nil {
			return false, err
		}

		if matches {
			return true, nil
		}
	}

	return false, nil
}

func (*AccessManagementReconciler) ruleAffectedByNamespaceLabelUpdate(rule kcmv1.AccessRule, oldNamespace, newNamespace *corev1.Namespace) (bool, error) {
	if len(rule.TargetNamespaces.List) > 0 {
		return false, nil
	}

	selector, selectorNonEmpty, err := buildLabelSelector(rule.TargetNamespaces.Selector, rule.TargetNamespaces.StringSelector)
	if err != nil {
		return false, fmt.Errorf("failed to get target namespaces selector: %w", err)
	}

	if !selectorNonEmpty {
		// empty selector means all namespaces; labels do not change membership
		return false, nil
	}

	oldMatches := selector.Matches(labels.Set(oldNamespace.GetLabels()))
	newMatches := selector.Matches(labels.Set(newNamespace.GetLabels()))

	return oldMatches != newMatches, nil
}

func (r *AccessManagementReconciler) accessManagementAffectedByNamespaceLabelUpdate(accessMgmt *kcmv1.AccessManagement, oldNamespace, newNamespace *corev1.Namespace) (bool, error) {
	for _, rule := range accessMgmt.Spec.AccessRules {
		affected, err := r.ruleAffectedByNamespaceLabelUpdate(rule, oldNamespace, newNamespace)
		if err != nil {
			return false, err
		}

		if affected {
			return true, nil
		}
	}

	return false, nil
}

// ensureDynamicRBAC computes the RBAC rules needed to read/write every GVK currently
// referenced across accessMgmt's AccessRules, and applies them via a dedicated,
// label-aggregated ClusterRole. This is the "dynamic RBAC" guardrail: the controller is granted
// exactly what AccessManagement's own spec asks for, nothing pre-provisioned and nothing
// wildcard.
func (r *AccessManagementReconciler) ensureDynamicRBAC(ctx context.Context, accessMgmt *kcmv1.AccessManagement, gvks []schema.GroupVersionKind) error {
	rules := r.buildResourceRBACRules(gvks)

	name := accessMgmt.Name + accessManagementDynamicClusterRoleSuffix
	clusterRole := &rbacv1.ClusterRole{}
	err := r.Get(ctx, client.ObjectKey{Name: name}, clusterRole)
	switch {
	case apierrors.IsNotFound(err):
		if len(rules) == 0 {
			return nil
		}
		desired := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name:   name,
				Labels: map[string]string{aggregateToManagerLabelKey: aggregateToManagerLabelValue},
			},
			Rules: rules,
		}
		// AccessManagement is a singleton and cluster-scoped, same as ClusterRole, so a normal
		// controller owner reference applies cleanly here. Setting it lets SetupWithManager use
		// .Owns(&rbacv1.ClusterRole{}) to react promptly to drift on this one object, instead of
		// only noticing on the next poll.
		if err := controllerutil.SetControllerReference(accessMgmt, desired, r.Scheme()); err != nil {
			return fmt.Errorf("failed to set owner reference on ClusterRole %s: %w", name, err)
		}
		return r.Create(ctx, desired)
	case err != nil:
		return fmt.Errorf("failed to get ClusterRole %s: %w", name, err)
	case len(rules) == 0:
		return client.IgnoreNotFound(r.Delete(ctx, clusterRole))
	case equality.Semantic.DeepEqual(clusterRole.Rules, rules) && clusterRole.Labels[aggregateToManagerLabelKey] == aggregateToManagerLabelValue:
		return nil
	default:
		clusterRole.Rules = rules
		if clusterRole.Labels == nil {
			clusterRole.Labels = make(map[string]string)
		}
		clusterRole.Labels[aggregateToManagerLabelKey] = aggregateToManagerLabelValue
		return r.Update(ctx, clusterRole)
	}
}

// buildResourceRBACRules computes the get/list/watch/create/delete PolicyRules needed to
// distribute objects of every given GVK. Unresolvable GVKs (CRD not installed yet, discovery
// not ready) are skipped and will be retried on a later reconcile once discovery catches up
// (surfaced separately via per-resource status).
func (r *AccessManagementReconciler) buildResourceRBACRules(gvks []schema.GroupVersionKind) []rbacv1.PolicyRule {
	groupToResources := make(map[string]map[string]struct{})
	for _, gvk := range gvks {
		mapping, err := r.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			continue
		}

		if mapping.Scope.Name() != apimeta.RESTScopeNameNamespace {
			// cluster-scoped Kinds are rejected by collectGVKResources and must never be
			// granted RBAC here either, regardless of spec content.
			continue
		}

		if _, ok := groupToResources[mapping.Resource.Group]; !ok {
			groupToResources[mapping.Resource.Group] = make(map[string]struct{})
		}
		groupToResources[mapping.Resource.Group][mapping.Resource.Resource] = struct{}{}
	}

	groups := slices.Sorted(maps.Keys(groupToResources))
	rules := make([]rbacv1.PolicyRule, 0, len(groups))
	for _, group := range groups {
		resources := slices.Sorted(maps.Keys(groupToResources[group]))
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups: []string{group},
			Resources: resources,
			Verbs:     []string{"get", "list", "watch", "create", "delete"},
		})
	}

	return rules
}

func (*AccessManagementReconciler) getEventPredicates() predicate.TypedFuncs[client.Object] {
	return predicate.TypedFuncs[client.Object]{
		CreateFunc: func(event.TypedCreateEvent[client.Object]) bool { return true },
		// no need for delete events, they can produce transient failures while namespaces terminate
		DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
		GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
		UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
			if tue.ObjectOld == nil || tue.ObjectNew == nil {
				return false
			}

			// reconcile on labels change because namespace selectors are label-based
			return !maps.Equal(tue.ObjectOld.GetLabels(), tue.ObjectNew.GetLabels())
		},
	}
}

// builtinKinds lists the objects AccessManagement has always known how to distribute, watched
// directly (unlike custom/dynamically-referenced Kinds, which are only polled: see
// defaultPollInterval) so that creating an object or relabeling it in the system namespace
// triggers a prompt reconciliation instead of waiting for the next poll.
func builtinKinds() []client.Object {
	return []client.Object{
		&kcmv1.ClusterTemplateChain{},
		&kcmv1.ServiceTemplateChain{},
		&kcmv1.Credential{},
		&kcmv1.ClusterAuthentication{},
		&kcmv1.DataSource{},
		&kcmv1.ClusterAuditPolicy{},
	}
}

// builtinKindEventHandler enqueues the singleton AccessManagement/kcm object whenever a
// built-in Kind object in the system namespace is created or relabeled (see getEventPredicates
// for exactly which events that is). AccessManagement is a singleton, so — same as the
// Namespace watch below — no indexer or lookup is needed to know which object to enqueue.
// Objects outside the system namespace are ignored: they can only be managed copies (never a
// source AccessManagement reads from), so their labels changing can't affect what gets
// distributed.
func (r *AccessManagementReconciler) builtinKindEventHandler() handler.TypedFuncs[client.Object, ctrl.Request] {
	enqueueIfSystemNamespace := func(obj client.Object, q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
		if obj == nil || obj.GetNamespace() != r.SystemNamespace {
			return
		}
		q.Add(ctrl.Request{NamespacedName: client.ObjectKey{Name: kcmv1.AccessManagementName}})
	}

	return handler.TypedFuncs[client.Object, ctrl.Request]{
		CreateFunc: func(_ context.Context, tce event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
			enqueueIfSystemNamespace(tce.Object, q)
		},
		UpdateFunc: func(_ context.Context, tue event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
			enqueueIfSystemNamespace(tue.ObjectNew, q)
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *AccessManagementReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.RESTMapper == nil {
		r.RESTMapper = mgr.GetRESTMapper()
	}

	if r.DynamicClient == nil {
		dc, err := dynamic.NewForConfig(mgr.GetConfig())
		if err != nil {
			return fmt.Errorf("failed to create dynamic client: %w", err)
		}
		r.DynamicClient = dc
	}

	bldr := ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.AccessManagement{}).
		// The dynamic-RBAC ClusterRole ensureDynamicRBAC maintains is owned by the singleton
		// AccessManagement/kcm object, so Owns reacts promptly if it's edited/deleted out from
		// under the controller, instead of waiting for the next poll to notice and fix it.
		Owns(&rbacv1.ClusterRole{}).
		Watches(
			&corev1.Namespace{},
			handler.TypedFuncs[client.Object, ctrl.Request]{
				CreateFunc: func(ctx context.Context, tce event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
					for _, req := range r.mapNamespaceToRequests(ctx, tce.Object) {
						q.Add(req)
					}
				},
				UpdateFunc: func(ctx context.Context, tue event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
					for _, req := range r.mapNamespaceLabelUpdateToRequests(ctx, tue.ObjectOld, tue.ObjectNew) {
						q.Add(req)
					}
				},
			},
			builder.WithPredicates(r.getEventPredicates()),
		)

	for _, obj := range builtinKinds() {
		bldr = bldr.Watches(obj, r.builtinKindEventHandler(), builder.WithPredicates(r.getEventPredicates()))
	}

	return bldr.Complete(r)
}

// TODO: FIXME: pass meaningful non-empty action
func (*AccessManagementReconciler) eventf(am *kcmv1.AccessManagement, reason, message string, args ...any) {
	record.Eventf(am, nil, reason, "Reconcile", message, args...)
}

// TODO: FIXME: pass meaningful non-empty action
func (*AccessManagementReconciler) warnf(am *kcmv1.AccessManagement, reason, message string, args ...any) {
	record.Warnf(am, nil, reason, "Reconcile", message, args...)
}
