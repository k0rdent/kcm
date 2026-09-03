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
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	// clusterRoleBindingNamePrefix is prepended to a binding's Name to name the ClusterRoleBinding
	// created for it in the child cluster (e.g. name "compute-admin" becomes
	// "k0rdent-compute-admin").
	clusterRoleBindingNamePrefix = "k0rdent-"

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

// Sync creates/updates the ClusterRoles and ClusterRoleBindings implied by policy's role catalog,
// and returns the set of object names that are now desired (for use by [Prune]) and whether
// anything was actually created or updated.
func Sync(ctx context.Context, childCl client.Client, policy *kcmv1.RBACPolicy) (desiredRoles, desiredBindings map[string]struct{}, changed bool, _ error) {
	desiredRoles = make(map[string]struct{})
	desiredBindings = make(map[string]struct{})

	if policy == nil {
		return desiredRoles, desiredBindings, false, nil
	}

	for _, binding := range policy.Spec.Bindings {
		if len(binding.Rules) > 0 {
			roleChanged, err := applyClusterRole(ctx, childCl, binding.ClusterRole, binding.Rules)
			if err != nil {
				return nil, nil, false, fmt.Errorf("applying ClusterRole %s: %w", binding.ClusterRole, err)
			}
			changed = changed || roleChanged
			desiredRoles[binding.ClusterRole] = struct{}{}
		}

		bindingName := clusterRoleBindingNamePrefix + binding.Name
		bindingChanged, err := applyClusterRoleBinding(ctx, childCl, bindingName, binding.ClusterRole, toSubjects(binding.Subjects))
		if err != nil {
			return nil, nil, false, fmt.Errorf("applying ClusterRoleBinding %s: %w", bindingName, err)
		}
		changed = changed || bindingChanged
		desiredBindings[bindingName] = struct{}{}
	}

	return desiredRoles, desiredBindings, changed, nil
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
	case err == nil && role.Labels[ManagedByLabelKey] != ManagedByLabelValue:
		return false, fmt.Errorf("ClusterRole %s already exists and was not created by this RBACPolicy; refusing to overwrite it", name)
	case client.IgnoreNotFound(err) != nil:
		return false, err
	}

	op, err := controllerutil.CreateOrUpdate(ctx, childCl, role, func() error {
		role.Labels = mergeManagedLabels(role.Labels)
		role.Rules = rules
		return nil
	})
	return op != controllerutil.OperationResultNone, err
}

func applyClusterRoleBinding(ctx context.Context, childCl client.Client, name, clusterRoleName string, subjects []rbacv1.Subject) (bool, error) {
	desiredRoleRef := rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: clusterRoleName}

	recreated := false
	binding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name}}
	err := childCl.Get(ctx, client.ObjectKeyFromObject(binding), binding)
	switch {
	case err == nil && binding.RoleRef != desiredRoleRef:
		// roleRef is immutable on an existing ClusterRoleBinding: recreate it if it changed.
		if err := childCl.Delete(ctx, binding); err != nil {
			return false, fmt.Errorf("deleting outdated ClusterRoleBinding to change roleRef: %w", err)
		}
		binding = &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name}}
		recreated = true
	case client.IgnoreNotFound(err) != nil:
		return false, err
	}

	op, err := controllerutil.CreateOrUpdate(ctx, childCl, binding, func() error {
		binding.Labels = mergeManagedLabels(binding.Labels)
		binding.RoleRef = desiredRoleRef
		binding.Subjects = subjects
		return nil
	})
	return recreated || op != controllerutil.OperationResultNone, err
}

func mergeManagedLabels(existing map[string]string) map[string]string {
	if existing == nil {
		existing = make(map[string]string, 2)
	}
	existing[kcmv1.KCMManagedLabelKey] = kcmv1.KCMManagedLabelValue
	existing[ManagedByLabelKey] = ManagedByLabelValue
	return existing
}

func managedSelector() labels.Selector {
	return labels.SelectorFromSet(map[string]string{ManagedByLabelKey: ManagedByLabelValue})
}

// Prune removes ClusterRoles and ClusterRoleBindings previously created by [Sync] in the child
// cluster that are no longer present in desiredRoles/desiredBindings, and reports whether
// anything was actually deleted. Passing nil/empty maps removes everything this package manages
// there — used both for normal drift cleanup after a [Sync], and to tear down everything a
// ClusterDeployment's child cluster once had once it stops referencing an RBACPolicy at all.
func Prune(ctx context.Context, childCl client.Client, desiredRoles, desiredBindings map[string]struct{}) (bool, error) {
	rolesChanged, err := pruneManaged(ctx, childCl, &rbacv1.ClusterRoleList{}, desiredRoles, "ClusterRole")
	if err != nil {
		return false, err
	}
	bindingsChanged, err := pruneBindings(ctx, childCl, desiredBindings)
	return rolesChanged || bindingsChanged, err
}

// pruneBindings removes ClusterRoleBindings previously created by [Sync] in the child cluster
// that are no longer present in desiredBindings, without touching ClusterRoles, and reports
// whether anything was actually deleted. Passing a nil/empty map removes every
// ClusterRoleBinding this package manages there.
func pruneBindings(ctx context.Context, childCl client.Client, desiredBindings map[string]struct{}) (bool, error) {
	return pruneManaged(ctx, childCl, &rbacv1.ClusterRoleBindingList{}, desiredBindings, "ClusterRoleBinding")
}

// pruneManaged deletes every ManagedByLabelKey-selected item in list not present in desired,
// reporting whether anything was deleted. kind is used only for error messages.
func pruneManaged(ctx context.Context, childCl client.Client, list client.ObjectList, desired map[string]struct{}, kind string) (bool, error) {
	if err := childCl.List(ctx, list, &client.ListOptions{LabelSelector: managedSelector()}); err != nil {
		return false, fmt.Errorf("listing %ss: %w", kind, err)
	}

	items, err := apimeta.ExtractList(list)
	if err != nil {
		return false, fmt.Errorf("extracting %s list: %w", kind, err)
	}

	changed := false
	for _, item := range items {
		obj, ok := item.(client.Object)
		if !ok {
			return changed, fmt.Errorf("unexpected %s list item type %T", kind, item)
		}
		if _, ok := desired[obj.GetName()]; ok {
			continue
		}
		if err := childCl.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
			return changed, fmt.Errorf("deleting stale %s %s: %w", kind, obj.GetName(), err)
		}
		changed = true
	}

	return changed, nil
}
