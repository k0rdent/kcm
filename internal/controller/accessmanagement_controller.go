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
	"encoding/json"
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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/record"
	accessmanagementutil "github.com/K0rdent/kcm/internal/util/accessmanagement"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	labelsutil "github.com/K0rdent/kcm/internal/util/labels"
	pollerutil "github.com/K0rdent/kcm/internal/util/poller"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
)

const (
	// defaultPollInterval is how often AccessManagement is periodically re-reconciled so that
	// changes to source objects of a referenced Kind (built-in or custom) get picked up promptly.
	// No per-Kind watch is registered for referenced Kinds: since the set of Kinds is unbounded and
	// only known at runtime, a simple poller is a much smaller mechanism than registering/tearing
	// down dynamic informers, at the cost of up-to-defaultPollInterval propagation latency.
	defaultPollInterval = 2 * time.Minute

	// accessManagementDynamicClusterRoleSuffix names the ClusterRole this controller
	// maintains to hold the RBAC rules it needs for whatever Kinds are currently referenced
	// across AccessManagement's Resources rules.
	accessManagementDynamicClusterRoleSuffix = "-resources"

	// aggregateToManagerLabelKey/Value must match the label the Helm chart's aggregation
	// ClusterRole selects on (templates/provider/kcm/templates/rbac/controller/roles.yaml), so
	// that rules granted here are folded into the controller-manager's own permissions without
	// any additional binding.
	aggregateToManagerLabelKey   = "k0rdent.mirantis.com/aggregate-to-manager"
	aggregateToManagerLabelValue = "true"
)

// errClusterScopedKindSkipped is returned by collectGroupKindResources for a cluster-scoped
// Kind. It's a sentinel, not a failure: callers must treat it as "this Kind was skipped, already
// logged as a warning" rather than joining it into the reconciliation's aggregate error.
var errClusterScopedKindSkipped = errors.New("cluster-scoped kind skipped")

// AccessManagementReconciler reconciles an AccessManagement object
type AccessManagementReconciler struct {
	client.Client

	// RESTMapper resolves a ResourceRule's APIGroup+Kind into a GroupVersionResource (letting the
	// mapper pick the preferred/served version) and its scope (namespaced vs cluster-scoped).
	// Defaults to the manager's RESTMapper in SetupWithManager; overridable for tests.
	RESTMapper apimeta.RESTMapper

	// DynamicClient performs generic List/Get/Create/Delete against arbitrary GVRs, built-in
	// or custom CRD. Defaults to a client built from the manager's rest.Config in
	// SetupWithManager; overridable for tests (e.g. with a fake dynamic client).
	DynamicClient dynamic.Interface

	// MetadataClient lists already-managed objects by ObjectMeta only (PartialObjectMetadata),
	// without transferring/decoding their spec/status. Cleanup only ever needs a managed object's
	// namespace and name, so this avoids paying for the full body of what can be, by far, the
	// largest list this controller performs (every managed copy of a Kind, across every target
	// namespace). Defaults to a client built from the manager's rest.Config in SetupWithManager;
	// overridable for tests (e.g. with a fake metadata client).
	MetadataClient metadata.Interface

	SystemNamespace string

	// pollInterval overrides defaultPollInterval for the poller registered in SetupWithManager;
	// used by tests to avoid waiting on the real interval. Zero means defaultPollInterval.
	// Unexported: nothing outside this package should need to override it, since it's purely an
	// internal implementation detail of the poller.
	pollInterval time.Duration
}

// groupKindResources holds the objects relevant to distributing a single Kind: the "system"
// objects read from SystemNamespace, keyed by name, and the "managed" copies already
// distributed into other namespaces by a previous reconciliation, used for cleanup.
type groupKindResources struct {
	// ownerRef is what every copy distributed by this reconciliation is owned by.
	ownerRef metav1.OwnerReference
	system   map[string]*unstructured.Unstructured
	// managedByNamespacedName indexes the same objects as managed, plus whatever this
	// reconciliation has since learned about them, so a copy can be settled without fetching
	// it again. managed keeps its own order, which cleanup follows.
	managedByNamespacedName map[string]*metav1.PartialObjectMetadata
	managed                 []*metav1.PartialObjectMetadata
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

	return ctrl.Result{}, err
}

func (r *AccessManagementReconciler) reconcileObj(ctx context.Context, accessMgmt *kcmv1.AccessManagement) error {
	currentGKs := accessmanagementutil.ReferencedGroupKinds(accessMgmt)
	currentGKSet := make(map[schema.GroupKind]struct{}, len(currentGKs))
	for _, gk := range currentGKs {
		currentGKSet[gk] = struct{}{}
	}

	// staleGKs are Kinds this controller was still tracking as of the previous reconcile (per
	// accessMgmt.Status.Resources, read here before it's overwritten below) but that no rule
	// references anymore. Without still collecting/cleaning them up here, a Kind dropped from the
	// spec would leave its previously-distributed copies orphaned forever: cleanup below only
	// ever runs for Kinds it's about to look at, and a Kind absent from both the spec and this
	// carried-over set would never be looked at again.
	staleGKs := r.staleManagedGroupKinds(accessMgmt, currentGKSet)

	// Both currently- and previously-referenced Kinds need RBAC this cycle: a stale Kind's
	// permissions must outlive its last rule by (at least) one reconcile, or the cleanup pass
	// below would immediately fail with a 403 right after this call revokes them.
	if err := r.ensureDynamicRBAC(ctx, accessMgmt, append(slices.Clone(currentGKs), staleGKs...)); err != nil {
		return fmt.Errorf("failed to ensure RBAC for referenced resources: %w", err)
	}

	// Invariant for the whole reconciliation and resolved through the scheme, so it is built
	// once here rather than per Kind per namespace per object.
	ownerRef, err := r.accessManagementOwnerReference(accessMgmt)
	if err != nil {
		return err
	}

	// Resolved once, independent of any Kind: reused for every Kind in the per-Kind loop below.
	rules, err := accessmanagementutil.ResolveRules(ctx, r.Client, r.SystemNamespace, accessMgmt.Spec.AccessRules)
	if err != nil {
		return err
	}

	statuses := make(map[schema.GroupKind]*kcmv1.ResourceKindStatus, len(currentGKs)+len(staleGKs))
	var errs error

	// One Kind at a time: collect, distribute (if still referenced) and clean up before moving to
	// the next Kind, instead of collecting every Kind's system+managed objects up front and
	// holding them all in memory simultaneously for the whole reconcile. Peak memory is then
	// bounded by the single largest Kind's dataset rather than the sum across every Kind
	// referenced by this AccessManagement.
	for _, gk := range append(slices.Clone(currentGKs), staleGKs...) {
		status := &kcmv1.ResourceKindStatus{APIGroup: gk.Group, Kind: gk.Kind}
		statuses[gk] = status

		res, err := r.collectGroupKindResources(ctx, accessMgmt, gk, ownerRef)
		switch {
		case errors.Is(err, errClusterScopedKindSkipped):
			// Not a reconciliation failure (already logged/warned inside
			// collectGroupKindResources), but still surfaced per-Kind so status doesn't read
			// as "processed successfully" for a Kind that was actually never distributed.
			status.Error = fmt.Sprintf("skipped: %s is cluster-scoped and cannot be distributed by AccessManagement", gk)
			continue
		case err != nil:
			status.Error = err.Error()
			errs = errors.Join(errs, fmt.Errorf("failed to collect resources for %s: %w", gk, err))
			continue
		}

		// Admission checks an update against this very plan, see
		// accessmanagementutil.ClusterTemplatesToRemove. A stale gk, which no rule references anymore, plans no copy,
		// so every managed copy of it is removed. A plan that cannot be made leaves the copies
		// alone: removing them on a partial view of the rules would be worse than retrying.
		plan, err := accessmanagementutil.PlanGroupKind(rules, gk, res.system, res.managed, accessMgmt)
		if err != nil {
			status.Error = err.Error()
			errs = errors.Join(errs, err)
			continue
		}

		if err := r.distribute(ctx, accessMgmt, gk, plan.Desired, res); err != nil {
			errs = errors.Join(errs, err)
			r.recordFirstError(status, err)
		}

		if err := r.cleanupManagedResources(ctx, accessMgmt, gk, plan.Remove, plan.Release); err != nil {
			errs = errors.Join(errs, err)
			r.recordFirstError(status, err)
		}
	}

	// A stale Kind is only worth persisting in status while cleanup for it hasn't fully
	// succeeded yet, so it's picked up and retried by staleManagedGroupKinds on the next
	// reconcile; once cleanup succeeds it can simply be forgotten.
	finalStatuses := make(map[schema.GroupKind]*kcmv1.ResourceKindStatus, len(statuses))
	for gk, status := range statuses {
		if _, ok := currentGKSet[gk]; ok || status.Error != "" {
			finalStatuses[gk] = status
		}
	}
	accessMgmt.Status.Resources = r.sortedResourceStatuses(finalStatuses)

	if errs != nil {
		return errs
	}

	accessMgmt.Status.Current = accessMgmt.Spec.AccessRules
	return nil
}

// staleManagedGroupKinds returns the GroupKinds recorded in accessMgmt.Status.Resources (i.e.
// Kinds this controller was still tracking as of the last reconcile) that currentGKs, the set
// referenced by the current spec, no longer contains.
func (*AccessManagementReconciler) staleManagedGroupKinds(accessMgmt *kcmv1.AccessManagement, currentGKs map[schema.GroupKind]struct{}) []schema.GroupKind {
	var stale []schema.GroupKind
	seen := make(map[schema.GroupKind]struct{})

	for _, status := range accessMgmt.Status.Resources {
		gk := schema.GroupKind{Group: status.APIGroup, Kind: status.Kind}
		if _, ok := currentGKs[gk]; ok {
			continue
		}
		if _, ok := seen[gk]; ok {
			continue
		}
		seen[gk] = struct{}{}
		stale = append(stale, gk)
	}

	return stale
}

func (*AccessManagementReconciler) recordFirstError(status *kcmv1.ResourceKindStatus, err error) {
	if status != nil && status.Error == "" {
		status.Error = err.Error()
	}
}

func (*AccessManagementReconciler) sortedResourceStatuses(statuses map[schema.GroupKind]*kcmv1.ResourceKindStatus) []kcmv1.ResourceKindStatus {
	if len(statuses) == 0 {
		return nil
	}

	gks := slices.Collect(maps.Keys(statuses))
	slices.SortFunc(gks, func(a, b schema.GroupKind) int {
		return cmp.Or(
			cmp.Compare(a.Group, b.Group),
			cmp.Compare(a.Kind, b.Kind),
		)
	})

	result := make([]kcmv1.ResourceKindStatus, 0, len(gks))
	for _, gk := range gks {
		result = append(result, *statuses[gk])
	}

	return result
}

// collectGroupKindResources resolves gk to a GroupVersionResource (letting the RESTMapper pick
// the preferred/served version, since AccessManagement only ever manipulates objects generically
// via unstructured.Unstructured) and lists both the system objects (the only allowed source for
// distribution) and any already-managed copies. Returns errClusterScopedKindSkipped if gk is
// cluster-scoped: that's not treated as a reconciliation failure, since a dynamically-referenced
// custom Kind having the wrong scope shouldn't fail the whole reconciliation — it's simply
// skipped (logged as a warning and surfaced via a Warning event) and every other resolvable Kind
// is still processed normally. Callers must check for this sentinel with errors.Is before
// treating a non-nil error as a real failure.
func (r *AccessManagementReconciler) collectGroupKindResources(ctx context.Context, accessMgmt *kcmv1.AccessManagement, gk schema.GroupKind, ownerRef metav1.OwnerReference) (*groupKindResources, error) {
	mapping, err := r.RESTMapper.RESTMapping(gk)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve %s (ensure the CRD is installed): %w", gk, err)
	}

	if mapping.Scope.Name() != apimeta.RESTScopeNameNamespace {
		l := ctrl.LoggerFrom(ctx)
		l.Info("skipping cluster-scoped Kind: AccessManagement can only distribute namespaced Kinds", "groupKind", gk.String())
		r.warnf(accessMgmt, "ClusterScopedKindSkipped", "Skipping %s: cluster-scoped Kinds cannot be distributed by AccessManagement", gk)
		return nil, errClusterScopedKindSkipped
	}

	gvr := mapping.Resource

	systemList, err := r.DynamicClient.Resource(gvr).Namespace(r.SystemNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list %s in namespace %s: %w", gk, r.SystemNamespace, err)
	}

	system := make(map[string]*unstructured.Unstructured, len(systemList.Items))
	for i := range systemList.Items {
		system[systemList.Items[i].GetName()] = &systemList.Items[i]
	}

	managedList, err := r.MetadataClient.Resource(gvr).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		LabelSelector: kcmv1.KCMManagedLabelKey + "=" + kcmv1.KCMManagedLabelValue,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list managed %s: %w", gk, err)
	}

	managed := make([]*metav1.PartialObjectMetadata, 0, len(managedList.Items))
	managedByNamespacedName := make(map[string]*metav1.PartialObjectMetadata, len(managedList.Items))
	for i := range managedList.Items {
		if managedList.Items[i].GetNamespace() == r.SystemNamespace {
			continue
		}
		managed = append(managed, &managedList.Items[i])
		managedByNamespacedName[accessmanagementutil.NamespacedName(managedList.Items[i].GetNamespace(), managedList.Items[i].GetName())] = &managedList.Items[i]
	}

	return &groupKindResources{system: system, managed: managed, managedByNamespacedName: managedByNamespacedName, ownerRef: ownerRef}, nil
}

// distribute makes sure every copy in desired exists, reporting the ones whose source is missing
// from the system namespace.
func (r *AccessManagementReconciler) distribute(ctx context.Context, accessMgmt *kcmv1.AccessManagement, gk schema.GroupKind, desired []accessmanagementutil.Target, res *groupKindResources) error {
	var errs error
	for _, target := range desired {
		sourceObj, ok := res.system[target.Name]
		if !ok {
			errs = errors.Join(errs, fmt.Errorf("%s %s/%s is not found", gk.Kind, r.SystemNamespace, target.Name))
			continue
		}

		created, err := r.createManagedObject(ctx, accessMgmt, gk, sourceObj, target.Namespace, res)
		if err != nil {
			r.warnf(accessMgmt, gk.Kind+"CreationFailed", "Failed to create %s %s/%s: %v", gk.Kind, target.Namespace, target.Name, err)
			errs = errors.Join(errs, err)
			continue
		}

		if created {
			r.eventf(accessMgmt, gk.Kind+"Created", "Successfully created %s %s/%s", gk.Kind, target.Namespace, target.Name)
		}
	}

	return errs
}

// createManagedObject creates targetNamespace's managed copy of sourceObj, applying the built-in
// per-Kind namespace field rewrites where applicable. The copy is owned by accessMgmt, so that
// deleting the AccessManagement has the garbage collector remove everything it ever distributed.
func (r *AccessManagementReconciler) createManagedObject(ctx context.Context, accessMgmt *kcmv1.AccessManagement, gk schema.GroupKind, sourceObj *unstructured.Unstructured, targetNamespace string, res *groupKindResources) (created bool, _ error) {
	// guards against a caller passing the source namespace: if the source were deleted between
	// collectGroupKindResources' List and this Create, the copy would take over its name,
	// stripped of its metadata and invisible to cleanup
	if targetNamespace == sourceObj.GetNamespace() {
		return false, nil
	}

	// Settled before anything reaches the API: a copy is never refreshed once it exists, so
	// there is nothing left to do for one already owned, or for a name another rule found
	// blocked earlier in this pass. The cost is immediacy — a copy deleted since the listing is
	// recreated on the next reconciliation rather than on this one.
	namespacedName := accessmanagementutil.NamespacedName(targetNamespace, sourceObj.GetName())
	if existing, ok := res.managedByNamespacedName[namespacedName]; ok && r.settled(existing, res.ownerRef) {
		return false, nil
	}

	if err := kubeutil.EnsureNamespace(ctx, r.Client, targetNamespace); err != nil {
		return false, fmt.Errorf("failed to ensure namespace %s: %w", targetNamespace, err)
	}

	target := sourceObj.DeepCopy()
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

	// set rather than upserted, unlike the adoption path: a copy created here starts scrubbed,
	// so the AccessManagement is its only owner by construction
	target.SetOwnerReferences([]metav1.OwnerReference{res.ownerRef})

	if err := r.applyBuiltinRewrite(gk, target, sourceObj); err != nil {
		return false, fmt.Errorf("failed to rewrite fields for %s: %w", gk, err)
	}

	mapping, err := r.RESTMapper.RESTMapping(gk)
	if err != nil {
		return false, fmt.Errorf("failed to resolve %s: %w", gk, err)
	}

	if _, err := r.DynamicClient.Resource(mapping.Resource).Namespace(targetNamespace).Create(ctx, target, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// reached only for what the listing did not show as settled, so the authoritative
			// object is worth reading: either a copy to adopt or somebody else's to leave alone
			adopted, err := r.adoptManagedObject(ctx, accessMgmt, mapping.Resource, targetNamespace, target.GetName(), res.ownerRef)
			if err != nil {
				return false, err
			}

			if adopted != nil {
				res.managedByNamespacedName[namespacedName] = adopted
			}

			return false, nil
		}
		return false, err
	}

	// the listing predates this copy, so record it: every rule after the first that distributes
	// the same name into the same namespace settles on it instead of colliding again
	res.managedByNamespacedName[namespacedName] = partialMetadata(target)

	ctrl.LoggerFrom(ctx).Info(gk.Kind+" was successfully created", "target namespace", targetNamespace, "source name", sourceObj.GetName())
	return true, nil
}

// settled reports whether an object the listing carries needs nothing further this
// reconciliation: either it is a managed copy already owned by ownerRef, or it is not a managed
// copy at all.
func (*AccessManagementReconciler) settled(existing *metav1.PartialObjectMetadata, ownerRef metav1.OwnerReference) bool {
	if existing.GetLabels()[kcmv1.KCMManagedLabelKey] != kcmv1.KCMManagedLabelValue {
		return true
	}

	return hasOwnerReference(existing.GetOwnerReferences(), ownerRef)
}

// partialMetadata reduces obj to what the listing holds for it: everything the fast path in
// createManagedObject reads, and nothing else.
func partialMetadata(obj *unstructured.Unstructured) *metav1.PartialObjectMetadata {
	return &metav1.PartialObjectMetadata{
		Namespace:       obj.GetNamespace(),
		Name:            obj.GetName(),
		Labels:          obj.GetLabels(),
		OwnerReferences: obj.GetOwnerReferences(),
	}
}

// adoptManagedObject backfills accessMgmt's owner reference on a managed copy that already
// exists in namespace, which is the upgrade path for copies distributed by a KCM version that
// didn't set one yet. Only objects carrying the managed label are ever touched.
func (r *AccessManagementReconciler) adoptManagedObject(ctx context.Context, accessMgmt *kcmv1.AccessManagement, gvr schema.GroupVersionResource, namespace, name string, ownerRef metav1.OwnerReference) (*metav1.PartialObjectMetadata, error) {
	var adopted *metav1.PartialObjectMetadata

	// the read is inside the retry: on a conflict the object has moved on, so the references to
	// merge into have to be read again
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := r.DynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			// gone between the Create above and this Get: the next reconciliation recreates it,
			// owned from the start
			if apierrors.IsNotFound(err) {
				adopted = nil
				return nil
			}
			return fmt.Errorf("failed to get %s %s/%s: %w", gvr.Resource, namespace, name, err)
		}

		adopted = partialMetadata(existing)

		if existing.GetLabels()[kcmv1.KCMManagedLabelKey] != kcmv1.KCMManagedLabelValue {
			// somebody else's object, left untouched: distribution is blocked for this name for
			// as long as it stands, and nothing else would report that
			ctrl.LoggerFrom(ctx).Info("Skipping an object that is not a managed copy: distribution is blocked for this name",
				"resource", gvr.Resource, "namespace", namespace, "name", name)
			r.warnf(accessMgmt, "ObjectNotManagedByKCM", "Not distributing %s %s/%s: an object with that name already exists and is not managed by KCM", gvr.Resource, namespace, name)

			return nil
		}

		if hasOwnerReference(existing.GetOwnerReferences(), ownerRef) {
			return nil
		}

		// SetOwnerReference replaces a reference naming the same owner (group, Kind and Name,
		// whatever the version) rather than appending to it, which is what refreshes the UID of
		// one left by a since-recreated AccessManagement
		if err := controllerutil.SetOwnerReference(accessMgmt, existing, r.Scheme()); err != nil {
			return fmt.Errorf("failed to set owner reference on %s %s/%s: %w", gvr.Resource, namespace, name, err)
		}

		patch, err := ownerReferencesPatch(existing)
		if err != nil {
			return fmt.Errorf("failed to build the owner references patch for %s %s/%s: %w", gvr.Resource, namespace, name, err)
		}

		if _, err := r.DynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to patch the owner references of %s %s/%s: %w", gvr.Resource, namespace, name, err)
		}

		adopted = partialMetadata(existing)
		ctrl.LoggerFrom(ctx).Info("Owner reference was successfully set on the already existing managed object", "resource", gvr.Resource, "namespace", namespace, "name", name)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return adopted, nil
}

// ownerReferencesPatch writes obj's owner references back and nothing else; the resourceVersion
// is a precondition, since a merge patch replaces the list wholesale.
func ownerReferencesPatch(obj *unstructured.Unstructured) ([]byte, error) {
	return json.Marshal(map[string]any{
		"metadata": map[string]any{
			"resourceVersion": obj.GetResourceVersion(),
			"ownerReferences": obj.GetOwnerReferences(),
		},
	})
}

// accessManagementOwnerReference returns what a distributed copy is owned by: a plain reference,
// not a controller one, which implies BlockOwnerDeletion — and that needs update on the owner's
// finalizers wherever the OwnerReferencesPermissionEnforcement admission plugin is on.
func (r *AccessManagementReconciler) accessManagementOwnerReference(accessMgmt *kcmv1.AccessManagement) (metav1.OwnerReference, error) {
	gvk, err := apiutil.GVKForObject(accessMgmt, r.Scheme())
	if err != nil {
		return metav1.OwnerReference{}, fmt.Errorf("failed to resolve the GroupVersionKind of AccessManagement %s: %w", accessMgmt.Name, err)
	}

	return metav1.OwnerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Name:       accessMgmt.Name,
		UID:        accessMgmt.UID,
	}, nil
}

// hasOwnerReference reports whether refs already carry ownerRef, UID included: controllerutil's
// HasOwnerReference ignores the UID, so a reference left by a since-recreated AccessManagement
// would pass as up to date, and the garbage collector deletes a copy owned by something gone.
func hasOwnerReference(refs []metav1.OwnerReference, ownerRef metav1.OwnerReference) bool {
	return slices.ContainsFunc(refs, func(ref metav1.OwnerReference) bool {
		return ref.APIVersion == ownerRef.APIVersion &&
			ref.Kind == ownerRef.Kind &&
			ref.Name == ownerRef.Name &&
			ref.UID == ownerRef.UID
	})
}

// applyBuiltinRewrite applies the small, explicit table of per-Kind field rewrites for the known
// built-in special cases; it is a no-op for any other Kind, including custom CRDs, which are
// copied verbatim. An error means target may have been left in a partially rewritten state and
// must not be created: silently distributing a copy with a stale/wrong reference would be worse
// than failing the reconcile and retrying.
func (r *AccessManagementReconciler) applyBuiltinRewrite(gk schema.GroupKind, target, sourceObj *unstructured.Unstructured) error {
	sourceNamespace := sourceObj.GetNamespace()

	switch gk {
	case kcmv1.GroupVersion.WithKind(kcmv1.ClusterTemplateKind).GroupKind():
		// ClusterTemplate.Spec.Helm: same as the ClusterTemplateChain distribution, the copy
		// references the HelmChart the source has already been reconciled into instead of
		// defining its own chart source.
		return r.rewriteClusterTemplateChartRef(target, sourceObj)
	case kcmv1.GroupVersion.WithKind(kcmv1.CredentialKind).GroupKind():
		// Credential.Spec.IdentityRef.Namespace: when the source object points somewhere in
		// particular, the copy should point at itself instead of the system namespace.
		return r.rewriteNamespaceIfSet(target, target.GetNamespace(), "spec", "identityRef", "namespace")
	case kcmv1.GroupVersion.WithKind(kcmv1.ClusterAuthenticationKind).GroupKind():
		// ClusterAuthentication.Spec.CASecret.Namespace: when unset, the Secret is expected to
		// exist alongside the source object; a copy keeps referencing that same Secret.
		return r.rewriteNamespaceIfEmpty(target, sourceNamespace, "spec", "caSecret", "namespace")
	case kcmv1.GroupVersion.WithKind(kcmv1.DataSourceKind).GroupKind():
		// DataSource.Spec.CertificateAuthority.Namespace: same rationale as CASecret above.
		return r.rewriteNamespaceIfEmpty(target, sourceNamespace, "spec", "certificateAuthority", "namespace")
	}
	return nil
}

// rewriteClusterTemplateChartRef replaces target's Helm spec with a reference to the chart the
// source ClusterTemplate was reconciled into, mirroring the ClusterTemplateChain distribution.
// The source has to be valid: until it is, it has no chart reference to point the copy at.
func (*AccessManagementReconciler) rewriteClusterTemplateChartRef(target, sourceObj *unstructured.Unstructured) error {
	clusterTemplate := new(kcmv1.ClusterTemplate)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(sourceObj.Object, clusterTemplate); err != nil {
		return fmt.Errorf("failed to convert %s %s/%s: %w", kcmv1.ClusterTemplateKind, sourceObj.GetNamespace(), sourceObj.GetName(), err)
	}

	if !clusterTemplate.Status.Valid || clusterTemplate.Status.ChartRef == nil {
		return fmt.Errorf("source %s %s/%s does not have chart reference yet", kcmv1.ClusterTemplateKind, clusterTemplate.Namespace, clusterTemplate.Name)
	}

	spec := clusterTemplate.Spec
	spec.Helm = kcmv1.HelmSpec{ChartRef: clusterTemplate.Status.ChartRef}

	specObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&spec)
	if err != nil {
		return fmt.Errorf("failed to convert the spec of %s %s/%s: %w", kcmv1.ClusterTemplateKind, clusterTemplate.Namespace, clusterTemplate.Name, err)
	}

	if err := unstructured.SetNestedMap(target.Object, specObj, "spec"); err != nil {
		return fmt.Errorf("failed to set spec: %w", err)
	}

	return nil
}

func (*AccessManagementReconciler) rewriteNamespaceIfSet(obj *unstructured.Unstructured, newNamespace string, fields ...string) error {
	current, found, err := unstructured.NestedString(obj.Object, fields...)
	if err != nil {
		return fmt.Errorf("failed to read %v: %w", fields, err)
	}
	if !found || current == "" {
		return nil
	}
	if err := unstructured.SetNestedField(obj.Object, newNamespace, fields...); err != nil {
		return fmt.Errorf("failed to set %v: %w", fields, err)
	}
	return nil
}

func (*AccessManagementReconciler) rewriteNamespaceIfEmpty(obj *unstructured.Unstructured, sourceNamespace string, fields ...string) error {
	_, parentFound, err := unstructured.NestedMap(obj.Object, fields[:len(fields)-1]...)
	if err != nil {
		return fmt.Errorf("failed to read %v: %w", fields[:len(fields)-1], err)
	}
	if !parentFound {
		return nil
	}

	current, _, err := unstructured.NestedString(obj.Object, fields...)
	if err != nil {
		return fmt.Errorf("failed to read %v: %w", fields, err)
	}
	if current != "" {
		return nil
	}
	if err := unstructured.SetNestedField(obj.Object, sourceNamespace, fields...); err != nil {
		return fmt.Errorf("failed to set %v: %w", fields, err)
	}
	return nil
}

// cleanupManagedResources deletes the managed copies in remove and lets go of those in release,
// see accessmanagementutil.Plan.
func (r *AccessManagementReconciler) cleanupManagedResources(ctx context.Context, accessMgmt *kcmv1.AccessManagement, gk schema.GroupKind, remove, release []*metav1.PartialObjectMetadata) error {
	var errs error
	for _, obj := range release {
		namespacedName := accessmanagementutil.NamespacedName(obj.GetNamespace(), obj.GetName())

		released, err := r.releaseManagedObject(ctx, gk, obj, accessMgmt)
		if err != nil {
			r.warnf(accessMgmt, gk.Kind+"ReleaseFailed", "Failed to release %s %s: %v", gk.Kind, namespacedName, err)
			errs = errors.Join(errs, err)
			continue
		}

		if released {
			r.eventf(accessMgmt, gk.Kind+"Released", "Successfully released %s %s", gk.Kind, namespacedName)
		}
	}

	for _, obj := range remove {
		namespacedName := accessmanagementutil.NamespacedName(obj.GetNamespace(), obj.GetName())

		deleted, err := r.deleteManagedObject(ctx, gk, obj)
		if err != nil {
			r.warnf(accessMgmt, gk.Kind+"DeletionFailed", "Failed to delete %s %s: %v", gk.Kind, namespacedName, err)
			errs = errors.Join(errs, err)
			continue
		}

		if deleted {
			r.eventf(accessMgmt, gk.Kind+"Deleted", "Successfully deleted %s %s", gk.Kind, namespacedName)
		}
	}
	return errs
}

func (r *AccessManagementReconciler) deleteManagedObject(ctx context.Context, gk schema.GroupKind, obj *metav1.PartialObjectMetadata) (deleted bool, _ error) {
	mapping, err := r.RESTMapper.RESTMapping(gk)
	if err != nil {
		return false, fmt.Errorf("failed to resolve %s: %w", gk, err)
	}

	if err := r.DynamicClient.Resource(mapping.Resource).Namespace(obj.GetNamespace()).Delete(ctx, obj.GetName(), metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	ctrl.LoggerFrom(ctx).Info(gk.Kind+" was successfully deleted", "namespace", obj.GetNamespace(), "name", obj.GetName())
	return true, nil
}

// releaseManagedObject drops accessMgmt's owner references from a managed copy that something
// else owns as well, leaving the copy itself in place for its other owners.
func (r *AccessManagementReconciler) releaseManagedObject(ctx context.Context, gk schema.GroupKind, obj *metav1.PartialObjectMetadata, accessMgmt *kcmv1.AccessManagement) (released bool, _ error) {
	isOwnRef := func(ref metav1.OwnerReference) bool {
		return accessmanagementutil.IsAccessManagementOwnerReference(ref, accessMgmt)
	}
	if !slices.ContainsFunc(obj.GetOwnerReferences(), isOwnRef) {
		return false, nil
	}

	mapping, err := r.RESTMapper.RESTMapping(gk)
	if err != nil {
		return false, fmt.Errorf("failed to resolve %s: %w", gk, err)
	}

	gvr := mapping.Resource
	namespace, name := obj.GetNamespace(), obj.GetName()

	// the read is inside the retry: on a conflict the object has moved on, so the references to
	// filter have to be read again
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := r.DynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				released = false
				return nil
			}
			return fmt.Errorf("failed to get %s %s/%s: %w", gvr.Resource, namespace, name, err)
		}

		refs := existing.GetOwnerReferences()
		if !slices.ContainsFunc(refs, isOwnRef) {
			released = false
			return nil
		}

		existing.SetOwnerReferences(slices.DeleteFunc(slices.Clone(refs), isOwnRef))

		patch, err := ownerReferencesPatch(existing)
		if err != nil {
			return fmt.Errorf("failed to build the owner references patch for %s %s/%s: %w", gvr.Resource, namespace, name, err)
		}

		if _, err := r.DynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to patch the owner references of %s %s/%s: %w", gvr.Resource, namespace, name, err)
		}

		released = true
		return nil
	})
	if err != nil {
		return false, err
	}

	if released {
		ctrl.LoggerFrom(ctx).Info(gk.Kind+" was successfully released", "namespace", namespace, "name", name)
	}

	return released, nil
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
		l.Error(
			err,
			"failed to list AccessManagement resources by namespace list index, falling back to full scan",
			"namespace", namespace.Name,
		)
		return fallback()
	}

	allNamespaceAccessManagements, err := r.listAccessManagementByField(ctx, kcmv1.AccessManagementTargetsAllNamespacesIndexKey, "true")
	if err != nil {
		l.Error(
			err,
			"failed to list AccessManagement resources by all-namespaces index, falling back to full scan",
			"namespace", namespace.Name,
		)
		return fallback()
	}

	selectorAccessManagements, err := r.listAccessManagementByField(ctx, kcmv1.AccessManagementUsesSelectorIndexKey, "true")
	if err != nil {
		l.Error(
			err,
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
			l.Error(
				selectorErr,
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
		l.Error(
			err,
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
			l.Error(
				selectorErr,
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
		l.Error(
			err,
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
			l.Error(
				err,
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

func (*AccessManagementReconciler) ruleTargetsNamespace(rule kcmv1.AccessRule, namespace *corev1.Namespace) (bool, error) {
	if len(rule.TargetNamespaces.List) > 0 {
		return slices.Contains(rule.TargetNamespaces.List, namespace.Name), nil
	}

	selector, selectorNonEmpty, err := accessmanagementutil.LabelSelector(rule.TargetNamespaces.Selector, rule.TargetNamespaces.StringSelector)
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

	selector, selectorNonEmpty, err := accessmanagementutil.LabelSelector(rule.TargetNamespaces.Selector, rule.TargetNamespaces.StringSelector)
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

// ensureDynamicRBAC computes the RBAC rules needed to read/write every Kind currently referenced
// across accessMgmt's AccessRules, and applies them via a dedicated, label-aggregated
// ClusterRole. This is the "dynamic RBAC" guardrail: the controller is granted exactly what
// AccessManagement's own spec asks for, nothing pre-provisioned and nothing wildcard.
func (r *AccessManagementReconciler) ensureDynamicRBAC(ctx context.Context, accessMgmt *kcmv1.AccessManagement, gks []schema.GroupKind) error {
	rules := r.buildResourceRBACRules(gks)

	name := accessMgmt.Name + accessManagementDynamicClusterRoleSuffix
	clusterRole := &rbacv1.ClusterRole{}
	err := r.Get(ctx, client.ObjectKey{Name: name}, clusterRole)
	switch {
	case apierrors.IsNotFound(err):
		if len(rules) == 0 {
			return nil
		}
		desired := &rbacv1.ClusterRole{
			Name:   name,
			Labels: map[string]string{aggregateToManagerLabelKey: aggregateToManagerLabelValue},
			Rules:  rules,
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
	case clusterRole.Labels[aggregateToManagerLabelKey] == aggregateToManagerLabelValue && equality.Semantic.DeepEqual(clusterRole.Rules, rules):
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

// buildResourceRBACRules computes the get/list/watch/create/update/delete PolicyRules needed to
// distribute objects of every given Kind; update is what lets adoptManagedObject backfill the
// owner reference on copies distributed before this controller set one. Unresolvable Kinds (CRD
// not installed yet, discovery not ready) are skipped and will be retried on a later reconcile
// once discovery catches up (surfaced separately via per-resource status).
func (r *AccessManagementReconciler) buildResourceRBACRules(gks []schema.GroupKind) []rbacv1.PolicyRule {
	groupToResources := make(map[string]map[string]struct{})
	for _, gk := range gks {
		mapping, err := r.RESTMapper.RESTMapping(gk)
		if err != nil {
			continue
		}

		if mapping.Scope.Name() != apimeta.RESTScopeNameNamespace {
			// cluster-scoped Kinds are rejected by collectGroupKindResources and must never be
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
			Verbs:     []string{"get", "list", "watch", "create", "update", "delete"},
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
func (*AccessManagementReconciler) builtinKinds() []client.Object {
	return []client.Object{
		&kcmv1.ClusterTemplate{},
		&kcmv1.ClusterTemplateChain{},
		&kcmv1.ServiceTemplateChain{},
		&kcmv1.Credential{},
		&kcmv1.ClusterAuthentication{},
		&kcmv1.DataSource{},
		&kcmv1.ClusterAuditPolicy{},
		&kcmv1.RBACPolicy{},
	}
}

// builtinKindPredicate returns the events of obj's Kind that warrant a reconciliation. On top of
// getEventPredicates, a ClusterTemplate turning valid is reacted to as well: only from then on
// does it carry the chart reference its copies are distributed with (see
// rewriteClusterTemplateChartRef), and the status update setting it changes no labels.
func (r *AccessManagementReconciler) builtinKindPredicate(obj client.Object) predicate.Predicate {
	if _, ok := obj.(*kcmv1.ClusterTemplate); !ok {
		return r.getEventPredicates()
	}

	return predicate.Or(r.getEventPredicates(), predicate.TypedFuncs[client.Object]{
		CreateFunc:  func(event.TypedCreateEvent[client.Object]) bool { return false },
		DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
		GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
		UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
			oldTemplate, okOld := tue.ObjectOld.(*kcmv1.ClusterTemplate)
			newTemplate, okNew := tue.ObjectNew.(*kcmv1.ClusterTemplate)
			return okOld && okNew && !oldTemplate.Status.Valid && newTemplate.Status.Valid
		},
	})
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
		q.Add(ctrl.Request{Name: kcmv1.AccessManagementName})
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

	if r.MetadataClient == nil {
		mc, err := metadata.NewForConfig(mgr.GetConfig())
		if err != nil {
			return fmt.Errorf("failed to create metadata client: %w", err)
		}
		r.MetadataClient = mc
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

	for _, obj := range r.builtinKinds() {
		bldr = bldr.Watches(obj, r.builtinKindEventHandler(), builder.WithPredicates(r.builtinKindPredicate(obj)))
	}

	pollInterval := r.pollInterval
	if pollInterval == 0 {
		pollInterval = defaultPollInterval
	}

	poller := pollerutil.NewRunner(
		r.accessManagementPollEnqueue,
		pollerutil.WithInterval(pollInterval),
		pollerutil.WithName("accessmanagement_poller"),
	)
	if err := mgr.Add(poller); err != nil {
		return fmt.Errorf("failed to add AccessManagement poller: %w", err)
	}

	bldr = bldr.WatchesRawSource(source.TypedChannel(poller.GetEventChannel(), &handler.TypedEnqueueRequestForObject[*kcmv1.AccessManagement]{}))

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
