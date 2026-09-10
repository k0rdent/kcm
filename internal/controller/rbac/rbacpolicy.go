// Copyright 2026
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

// Package rbac materializes the ClusterRoles and ClusterRoleBindings described by a
// [kcmv1.RBACPolicy] into a child cluster. It is invoked by
// [github.com/K0rdent/kcm/internal/controller.ClusterDeploymentReconciler] for the single
// RBACPolicy a ClusterDeployment references via spec.rbacPolicy — there is no dedicated
// controller in this package, since the sync is just one more step of that reconciler's loop.
package rbac

import (
	"context"
	"errors"
	"fmt"
	"slices"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	// rbacSubjectAPIGroup is the fixed APIGroup Kubernetes requires for User/Group RBAC subjects
	// (the only two kinds RBACPolicySubject supports) — not configurable, see RBACPolicySubject's
	// doc comment.
	rbacSubjectAPIGroup = rbacv1.GroupName

	// ManagedByLabelKey / ManagedByLabelValue mark every ClusterRole/ClusterRoleBinding this package
	// creates in a child cluster, and are what Prune selects on. Deliberately distinct from the
	// generic kcmv1.KCMManagedLabelKey so a prune here can never touch some other k0rdent-managed
	// object in the child cluster that isn't part of this RBACPolicy sync. It is also what tells
	// applyClusterRole a ClusterRole is safe to overwrite.
	ManagedByLabelKey   = "k0rdent.mirantis.com/managed-by"
	ManagedByLabelValue = "rbac-operator"
)

// ErrTerminal marks a failure that no amount of retrying can clear — only an RBACPolicy spec edit
// can. Callers use [Retriable] to decide whether to requeue.
var ErrTerminal = errors.New("terminal error")

// Retriable reports whether err holds at least one failure worth retrying. A [ErrTerminal]-only
// error (including a joined one) is not.
func Retriable(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		return slices.ContainsFunc(joined.Unwrap(), Retriable)
	}
	if wrapped := errors.Unwrap(err); wrapped != nil {
		return Retriable(wrapped)
	}
	return !errors.Is(err, ErrTerminal)
}

// Sync creates/updates the ClusterRoles and ClusterRoleBindings implied by policy's role catalog,
// and returns the set of object names that are now desired (for use by [Prune]) and whether
// anything was actually created or updated. A binding that fails to apply does not stop the rest
// from being applied; all failures are joined into the returned error.
func Sync(ctx context.Context, childCl client.Client, policy *kcmv1.RBACPolicy) (desiredRoles, desiredBindings map[string]struct{}, changed bool, _ error) {
	desiredRoles = make(map[string]struct{})
	desiredBindings = make(map[string]struct{})

	if policy == nil {
		return desiredRoles, desiredBindings, false, nil
	}

	// Everything the catalog names stays desired even if applying it fails below, so a transient
	// error can never make Prune revoke a binding that is still wanted. A ClusterRole is desired
	// whether or not this binding carries rules: dropping rules from a binding must not delete the
	// managed ClusterRole its live ClusterRoleBinding still points at.
	for _, binding := range policy.Spec.Bindings {
		desiredRoles[binding.ClusterRole] = struct{}{}
		desiredBindings[kcmv1.ClusterRoleBindingNamePrefix+binding.Name] = struct{}{}
	}

	var errs []error
	for _, binding := range policy.Spec.Bindings {
		if len(binding.Rules) > 0 {
			roleChanged, err := applyClusterRole(ctx, childCl, binding.ClusterRole, binding.Rules)
			if err != nil {
				errs = append(errs, fmt.Errorf("applying ClusterRole %s: %w", binding.ClusterRole, err))
				continue
			}
			changed = changed || roleChanged
		}

		bindingName := kcmv1.ClusterRoleBindingNamePrefix + binding.Name
		bindingChanged, err := applyClusterRoleBinding(ctx, childCl, bindingName, binding.ClusterRole, toSubjects(binding.Subjects))
		if err != nil {
			errs = append(errs, fmt.Errorf("applying ClusterRoleBinding %s: %w", bindingName, err))
			continue
		}
		changed = changed || bindingChanged
	}

	return desiredRoles, desiredBindings, changed, errors.Join(errs...)
}

// toSubjects converts policy subjects into rbacv1.Subjects, filling in the fixed APIGroup
// Kubernetes requires for them (see RBACPolicySubject's doc comment).
func toSubjects(subjects []kcmv1.RBACPolicySubject) []rbacv1.Subject {
	out := make([]rbacv1.Subject, len(subjects))
	for i, s := range subjects {
		out[i] = rbacv1.Subject{Kind: s.Kind, Name: s.Name, APIGroup: rbacSubjectAPIGroup}
	}
	return out
}

// applyClusterRole creates or updates the named ClusterRole with rules, unless a ClusterRole by
// that name already exists and wasn't created by this package (i.e. it's missing
// ManagedByLabelKey) — in which case it refuses, so a binding can never overwrite an
// already-existing ClusterRole such as a built-in "admin"/"edit"/"view", matching
// RBACPolicyBinding.Rules' doc comment.
func applyClusterRole(ctx context.Context, childCl client.Client, name string, rules []rbacv1.PolicyRule) (bool, error) {
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: name}}
	err := childCl.Get(ctx, client.ObjectKeyFromObject(role), role)
	switch {
	case apierrors.IsNotFound(err):
		role.Labels = mergeManagedLabels(nil)
		role.Rules = rules
		if err := childCl.Create(ctx, role); err != nil {
			return false, err
		}
		return true, nil
	case err != nil:
		return false, err
	case role.Labels[ManagedByLabelKey] != ManagedByLabelValue:
		return false, fmt.Errorf("%w: ClusterRole %s already exists and was not created by this RBACPolicy; refusing to overwrite it", ErrTerminal, name)
	}

	base := role.DeepCopy()
	role.Labels = mergeManagedLabels(role.Labels)
	role.Rules = rules
	return patchIfChanged(ctx, childCl, base, role)
}

func applyClusterRoleBinding(ctx context.Context, childCl client.Client, name, clusterRoleName string, subjects []rbacv1.Subject) (bool, error) {
	desiredRoleRef := rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: clusterRoleName}
	desired := func(labels map[string]string) *rbacv1.ClusterRoleBinding {
		return &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: mergeManagedLabels(labels)},
			RoleRef:    desiredRoleRef,
			Subjects:   subjects,
		}
	}

	binding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name}}
	err := childCl.Get(ctx, client.ObjectKeyFromObject(binding), binding)
	switch {
	case apierrors.IsNotFound(err):
		if err := childCl.Create(ctx, desired(nil)); err != nil {
			return false, err
		}
		return true, nil
	case err != nil:
		return false, err
	case binding.RoleRef != desiredRoleRef:
		// roleRef is immutable, so changing it means delete and recreate. Deletion can be
		// asynchronous, in which case Create fails with AlreadyExists and the caller's retry
		// reconciles it once the old object is gone.
		if err := childCl.Delete(ctx, binding); err != nil {
			return false, fmt.Errorf("deleting outdated ClusterRoleBinding to change roleRef: %w", err)
		}
		if err := childCl.Create(ctx, desired(nil)); err != nil {
			return false, fmt.Errorf("creating ClusterRoleBinding after deleting the outdated one: %w", err)
		}
		return true, nil
	}

	base := binding.DeepCopy()
	binding.Labels = mergeManagedLabels(binding.Labels)
	binding.Subjects = subjects
	return patchIfChanged(ctx, childCl, base, binding)
}

// patchIfChanged merge-patches obj onto base, reporting whether a write was actually needed.
func patchIfChanged(ctx context.Context, childCl client.Client, base, obj client.Object) (bool, error) {
	patch := client.MergeFrom(base)
	data, err := patch.Data(obj)
	if err != nil {
		return false, err
	}
	if string(data) == "{}" {
		return false, nil
	}
	return true, childCl.Patch(ctx, obj, patch)
}

func mergeManagedLabels(existing map[string]string) map[string]string {
	if existing == nil {
		existing = make(map[string]string, 2)
	}
	existing[kcmv1.KCMManagedLabelKey] = kcmv1.KCMManagedLabelValue
	existing[ManagedByLabelKey] = ManagedByLabelValue
	return existing
}

// Prune removes ClusterRoles and ClusterRoleBindings previously created by [Sync] in the child
// cluster that are no longer present in desiredRoles/desiredBindings, and reports whether
// anything was actually deleted. Passing nil/empty maps removes everything this package manages
// there — used both for normal drift cleanup after a [Sync], and to tear down everything a
// ClusterDeployment's child cluster once had once it stops referencing an RBACPolicy at all.
func Prune(ctx context.Context, childCl client.Client, desiredRoles, desiredBindings map[string]struct{}) (bool, error) {
	rolesChanged, err := pruneManaged(ctx, childCl, rbacv1.SchemeGroupVersion.WithKind("ClusterRole"), desiredRoles)
	if err != nil {
		return false, err
	}
	bindingsChanged, err := pruneManaged(ctx, childCl, rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBinding"), desiredBindings)
	return rolesChanged || bindingsChanged, err
}

// pruneManaged deletes every ManagedByLabelKey-selected object of the given kind that is not
// present in desired, reporting whether anything was deleted. It lists metadata only, since names
// are all it needs.
func pruneManaged(ctx context.Context, childCl client.Client, gvk schema.GroupVersionKind, desired map[string]struct{}) (bool, error) {
	list := &metav1.PartialObjectMetadataList{}
	list.SetGroupVersionKind(gvk)
	if err := childCl.List(ctx, list, client.MatchingLabels{ManagedByLabelKey: ManagedByLabelValue}); err != nil {
		return false, fmt.Errorf("listing %ss: %w", gvk.Kind, err)
	}

	changed := false
	for i := range list.Items {
		obj := &list.Items[i]
		if _, ok := desired[obj.GetName()]; ok {
			continue
		}
		obj.SetGroupVersionKind(gvk)
		if err := childCl.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
			return changed, fmt.Errorf("deleting stale %s %s: %w", gvk.Kind, obj.GetName(), err)
		}
		changed = true
	}

	return changed, nil
}
