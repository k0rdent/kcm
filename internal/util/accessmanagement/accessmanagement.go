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

// Package accessmanagement works out what reconciling an AccessManagement does to the objects it
// distributes: which copies its rules want and which already distributed copies they no longer
// do. The controller acts on it, and admission uses the very same answer to check, before an
// update is accepted, that the copies it would remove can be removed.
package accessmanagement

import (
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// Rule is an AccessRule resolved against the cluster: the namespaces it distributes into and the
// resource rules selecting what it distributes there.
type Rule struct {
	Namespaces []string
	Resources  []kcmv1.ResourceRule
}

// Target is one copy the rules distribute: the object Name from the system namespace, into
// Namespace.
type Target struct {
	Namespace string
	Name      string
}

// Plan is what reconciling an AccessManagement does for a single Kind.
type Plan[M metav1.Object] struct {
	// Desired holds the copies the rules distribute, de-duplicated, in rule order.
	Desired []Target
	// Remove holds the managed copies no rule distributes anymore and that nothing but the
	// AccessManagement owns: they are deleted.
	Remove []M
	// Release holds the managed copies no rule distributes anymore but that something else owns
	// too, such as a ClusterTemplate a ClusterTemplateChain also distributed into the same
	// namespace: they are still wanted there, so the AccessManagement only drops its own owner
	// reference.
	Release []M
}

// labeled is what resolving a ResourceRule needs of an object in the system namespace.
type labeled interface {
	GetLabels() map[string]string
}

// PlanGroupKind works out what reconciling accessMgmt does for gk. system holds the gk objects in
// the system namespace by name, the only source copies are ever made from; managed holds the
// managed copies of gk found outside it. Both the controller, acting on the whole plan, and
// admission, checking that what it removes can be removed, go through this.
func PlanGroupKind[T labeled, M metav1.Object](rules []Rule, gk schema.GroupKind, system map[string]T, managed []M, accessMgmt *kcmv1.AccessManagement) (*Plan[M], error) {
	desired, err := desiredTargets(rules, gk, system)
	if err != nil {
		return nil, err
	}

	keep := make(map[string]struct{}, len(desired))
	for _, target := range desired {
		keep[NamespacedName(target.Namespace, target.Name)] = struct{}{}
	}

	plan := &Plan[M]{Desired: desired}
	for _, obj := range managed {
		if _, ok := keep[NamespacedName(obj.GetNamespace(), obj.GetName())]; ok {
			continue
		}

		if HasForeignOwnerReference(obj.GetOwnerReferences(), accessMgmt) {
			plan.Release = append(plan.Release, obj)
			continue
		}
		plan.Remove = append(plan.Remove, obj)
	}

	return plan, nil
}

// desiredTargets returns the copies of gk the rules distribute, de-duplicated, in rule order.
func desiredTargets[T labeled](rules []Rule, gk schema.GroupKind, system map[string]T) ([]Target, error) {
	var (
		desired []Target
		seen    = make(map[Target]struct{})
	)
	for _, rule := range rules {
		for _, res := range rule.Resources {
			if res.GroupKind() != gk {
				continue
			}

			names, err := ResolveNames(res, system)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve names for %s: %w", gk, err)
			}

			for _, namespace := range rule.Namespaces {
				for _, name := range names {
					target := Target{Namespace: namespace, Name: name}
					if _, ok := seen[target]; ok {
						continue
					}
					seen[target] = struct{}{}
					desired = append(desired, target)
				}
			}
		}
	}

	return desired, nil
}

// ClusterTemplatesToRemove returns the ClusterTemplates reconciling accessMgmt would delete, as
// the cluster stands: managed copies its rules no longer distribute and that nothing else owns,
// leaving out the ones already being deleted.
// It is nil unless the controller still handles ClusterTemplates for accessMgmt at all, see
// TracksGroupKind.
func ClusterTemplatesToRemove(ctx context.Context, cl client.Client, systemNamespace string, accessMgmt *kcmv1.AccessManagement) ([]*kcmv1.ClusterTemplate, error) {
	gk := kcmv1.GroupVersion.WithKind(kcmv1.ClusterTemplateKind).GroupKind()
	if !TracksGroupKind(accessMgmt, gk) {
		return nil, nil
	}

	rules, err := ResolveRules(ctx, cl, systemNamespace, accessMgmt.Spec.AccessRules)
	if err != nil {
		return nil, err
	}

	systemTemplates := new(kcmv1.ClusterTemplateList)
	if err := cl.List(ctx, systemTemplates, client.InNamespace(systemNamespace)); err != nil {
		return nil, fmt.Errorf("failed to list %s in namespace %s: %w", gk, systemNamespace, err)
	}
	system := make(map[string]*kcmv1.ClusterTemplate, len(systemTemplates.Items))
	for i := range systemTemplates.Items {
		system[systemTemplates.Items[i].Name] = &systemTemplates.Items[i]
	}

	managedTemplates := new(kcmv1.ClusterTemplateList)
	if err := cl.List(ctx, managedTemplates, client.MatchingLabels{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue}); err != nil {
		return nil, fmt.Errorf("failed to list managed %s: %w", gk, err)
	}
	managed := make([]*kcmv1.ClusterTemplate, 0, len(managedTemplates.Items))
	for i := range managedTemplates.Items {
		// a copy already being deleted is going away whatever the rules say
		if managedTemplates.Items[i].Namespace != systemNamespace && managedTemplates.Items[i].DeletionTimestamp.IsZero() {
			managed = append(managed, &managedTemplates.Items[i])
		}
	}

	plan, err := PlanGroupKind(rules, gk, system, managed, accessMgmt)
	if err != nil {
		return nil, err
	}

	return plan.Remove, nil
}

// ResolveRules resolves every AccessRule against the cluster. EffectiveResources falls back to the
// deprecated one-field-per-Kind selectors when Resources is empty, for backward compatibility when
// the mutating webhook that normally migrates them on write is disabled/unavailable. When both are
// present, Resources wins outright.
func ResolveRules(ctx context.Context, cl client.Client, systemNamespace string, accessRules []kcmv1.AccessRule) ([]Rule, error) {
	rules := make([]Rule, len(accessRules))
	for i, rule := range accessRules {
		namespaces, err := TargetNamespaces(ctx, cl, systemNamespace, rule.TargetNamespaces)
		if err != nil {
			return nil, fmt.Errorf("failed to collect target namespaces: %w", err)
		}

		rules[i] = Rule{Namespaces: namespaces, Resources: rule.EffectiveResources()}
	}

	return rules, nil
}

// TargetNamespaces resolves the namespaces a rule distributes into, always excluding
// systemNamespace: an object copied back into the namespace it was read from is either a no-op or
// a copy taking over the name of its own source.
func TargetNamespaces(ctx context.Context, cl client.Client, systemNamespace string, targetNamespaces kcmv1.TargetNamespaces) ([]string, error) {
	if len(targetNamespaces.List) > 0 {
		// no match is the common case and keeps the spec's slice as is; from the first match on,
		// the prefix is bulk-copied and only the remainder is filtered
		i := slices.Index(targetNamespaces.List, systemNamespace)
		if i < 0 {
			return targetNamespaces.List, nil
		}

		result := make([]string, i, len(targetNamespaces.List)-1)
		copy(result, targetNamespaces.List[:i])
		for _, ns := range targetNamespaces.List[i+1:] {
			if ns != systemNamespace {
				result = append(result, ns)
			}
		}

		return result, nil
	}

	selector, selectorNonEmpty, err := LabelSelector(targetNamespaces.Selector, targetNamespaces.StringSelector)
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

	if err := cl.List(ctx, namespaces, listOpts); err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	result := make([]string, 0, len(namespaces.Items))
	for _, ns := range namespaces.Items {
		if ns.Name == systemNamespace {
			continue
		}
		result = append(result, ns.Name)
	}

	return result, nil
}

// ResolveNames returns the object names in the system namespace selected by rule. Whether Names
// was set at all (rather than just non-empty) is what decides the branch taken: an explicitly-empty
// Names ([] rather than omitted) means "select nothing" and must not fall through to selector
// matching, or it would silently select every object of the Kind. A non-empty Names is used
// verbatim (even for names not found in system, so callers can still report a clear "not found"
// error and keep the target from being deleted-then-recreated on the next reconcile). Only when
// Names is nil is Selector/StringSelector matched against the system objects' labels — same
// convention as TargetNamespaces: no selector, or an empty one, matches every object of the Kind.
func ResolveNames[T labeled](rule kcmv1.ResourceRule, system map[string]T) ([]string, error) {
	if rule.Names != nil {
		return rule.Names, nil
	}

	selector, selectorNonEmpty, err := LabelSelector(rule.Selector, rule.StringSelector)
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

// LabelSelector is the single helper behind both TargetNamespaces and ResourceRule selector
// matching: a non-empty stringSelector takes precedence, then a structured selector. The returned
// bool reports whether the resulting selector is non-empty (an empty selector, or no selector at
// all, conventionally means "match everything" for both use sites).
func LabelSelector(selector *metav1.LabelSelector, stringSelector string) (labels.Selector, bool, error) {
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

// ReferencedGroupKinds returns the de-duplicated set of GroupKinds referenced across all of
// accessMgmt's AccessRules' EffectiveResources, in first-seen order.
func ReferencedGroupKinds(accessMgmt *kcmv1.AccessManagement) []schema.GroupKind {
	seen := make(map[schema.GroupKind]struct{})
	var gks []schema.GroupKind

	for _, rule := range accessMgmt.Spec.AccessRules {
		for _, res := range rule.EffectiveResources() {
			gk := res.GroupKind()

			if _, ok := seen[gk]; ok {
				continue
			}
			seen[gk] = struct{}{}
			gks = append(gks, gk)
		}
	}

	return gks
}

// TracksGroupKind reports whether the controller handles gk for accessMgmt: either a rule
// references it, or accessMgmt.Status.Resources still records it from a previous reconcile, which
// is how copies of a Kind dropped from every rule still get cleaned up.
func TracksGroupKind(accessMgmt *kcmv1.AccessManagement, gk schema.GroupKind) bool {
	if slices.Contains(ReferencedGroupKinds(accessMgmt), gk) {
		return true
	}

	return slices.ContainsFunc(accessMgmt.Status.Resources, func(status kcmv1.ResourceKindStatus) bool {
		return status.APIGroup == gk.Group && status.Kind == gk.Kind
	})
}

// NamespacedName returns the namespace/name key a copy is tracked under.
func NamespacedName(namespace, name string) string {
	return namespace + "/" + name
}

// IsAccessManagementOwnerReference reports whether ref names accessMgmt, whatever its version or
// UID: a reference left by a since-recreated AccessManagement is still the controller's to drop.
func IsAccessManagementOwnerReference(ref metav1.OwnerReference, accessMgmt *kcmv1.AccessManagement) bool {
	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	return err == nil && gv.Group == kcmv1.GroupVersion.Group && ref.Kind == kcmv1.AccessManagementKind && ref.Name == accessMgmt.Name
}

// HasForeignOwnerReference reports whether refs carry any owner besides accessMgmt.
func HasForeignOwnerReference(refs []metav1.OwnerReference, accessMgmt *kcmv1.AccessManagement) bool {
	return slices.ContainsFunc(refs, func(ref metav1.OwnerReference) bool {
		return !IsAccessManagementOwnerReference(ref, accessMgmt)
	})
}
