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

package v1beta1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	AccessManagementKind = "AccessManagement"

	AccessManagementName = "kcm"
)

// AccessManagementSpec defines the desired state of AccessManagement
type AccessManagementSpec struct {
	// AccessRules is the list of access rules. Each AccessRule enforces
	// objects distribution to the TargetNamespaces.
	AccessRules []AccessRule `json:"accessRules,omitempty"`
}

// AccessManagementStatus defines the observed state of AccessManagement
type AccessManagementStatus struct {
	// Error is the aggregate error message occurred during the reconciliation (if any).
	// See Resources for a per-Kind breakdown.
	Error string `json:"error,omitempty"`
	// Current reflects the applied access rules configuration.
	Current []AccessRule `json:"current,omitempty"`
	// Resources reports the resolution outcome for each distinct Kind referenced across all
	// AccessRules during the last reconciliation. A generic multi-Kind mechanism can fail
	// per-Kind (bad selector matched nothing, target Kind rejected as cluster-scoped,
	// RBAC/discovery not ready for a GVK) independently of the others, so those failures are
	// reported individually here in addition to the aggregate Error above.
	Resources []ResourceKindStatus `json:"resources,omitempty"`
	// ObservedGeneration is the last observed generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// ResourceKindStatus reports the outcome of resolving/distributing objects of a single Kind
// referenced by one or more ResourceRules across the AccessManagement's AccessRules.
type ResourceKindStatus struct {
	// APIVersion of the Kind this status entry applies to.
	APIVersion string `json:"apiVersion,omitempty"`
	// Kind this status entry applies to.
	Kind string `json:"kind,omitempty"`
	// Error is the error message occurred while resolving/distributing objects of this Kind, if any.
	// An empty Error means this Kind was processed successfully as of ObservedGeneration.
	Error string `json:"error,omitempty"`
}

// AccessRule is the definition of the AccessManagement access rule. Each AccessRule enforces
// Templates and Credentials distribution to the TargetNamespaces
type AccessRule struct {
	// TargetNamespaces defines the namespaces where selected objects will be distributed.
	// Templates and Credentials will be distributed to all namespaces if unset.
	TargetNamespaces TargetNamespaces `json:"targetNamespaces,omitempty"`

	// Resources is the generic list of resource rules. Each entry selects a set of objects of
	// a given Kind (built-in or a custom/third-party CRD) to distribute into the namespaces
	// matched by TargetNamespaces. Supersedes ClusterTemplateChains, ServiceTemplateChains,
	// Credentials, ClusterAuthentications, DataSources and ClusterAuditPolicies below: any of
	// those still populated are automatically translated into equivalent entries here.
	Resources []ResourceRule `json:"resources,omitempty"`

	// Deprecated: use Resources with kind: ClusterTemplateChain instead. Populating this field
	// is still accepted; it is automatically translated into an equivalent entry in Resources
	// by a mutating webhook on write. Will be removed in a future API version.
	//
	// ClusterTemplateChains is the list of [ClusterTemplateChain] names whose ClusterTemplates
	// will be distributed to all namespaces specified in TargetNamespaces.
	ClusterTemplateChains []string `json:"clusterTemplateChains,omitempty"`
	// Deprecated: use Resources with kind: ServiceTemplateChain instead. Populating this field
	// is still accepted; it is automatically translated into an equivalent entry in Resources
	// by a mutating webhook on write. Will be removed in a future API version.
	//
	// ServiceTemplateChains is the list of [ServiceTemplateChain] names whose ServiceTemplates
	// will be distributed to all namespaces specified in TargetNamespaces.
	ServiceTemplateChains []string `json:"serviceTemplateChains,omitempty"`
	// Deprecated: use Resources with kind: Credential instead. Populating this field is still
	// accepted; it is automatically translated into an equivalent entry in Resources by a
	// mutating webhook on write. Will be removed in a future API version.
	//
	// Credentials is the list of [Credential] names that will be distributed to all the
	// namespaces specified in TargetNamespaces.
	Credentials []string `json:"credentials,omitempty"`
	// Deprecated: use Resources with kind: ClusterAuthentication instead. Populating this field
	// is still accepted; it is automatically translated into an equivalent entry in Resources
	// by a mutating webhook on write. Will be removed in a future API version.
	//
	// ClusterAuthentications is the list of [ClusterAuthentication] names that will be distributed to all the
	// namespaces specified in TargetNamespaces.
	ClusterAuthentications []string `json:"clusterAuthentications,omitempty"`
	// Deprecated: use Resources with kind: DataSource instead. Populating this field is still
	// accepted; it is automatically translated into an equivalent entry in Resources by a
	// mutating webhook on write. Will be removed in a future API version.
	//
	// DataSources is the list of [DataSource] names that will be distributed to all the
	// namespaces specified in TargetNamespaces.
	DataSources []string `json:"dataSources,omitempty"`
	// Deprecated: use Resources with kind: ClusterAuditPolicy instead. Populating this field is
	// still accepted; it is automatically translated into an equivalent entry in Resources by a
	// mutating webhook on write. Will be removed in a future API version.
	//
	// ClusterAuditPolicies is the list of [ClusterAuditPolicy] names that will be distributed to all the
	// namespaces specified in TargetNamespaces.
	ClusterAuditPolicies []string `json:"clusterAuditPolicies,omitempty"`
}

// HasDeprecatedFields reports whether the AccessRule still populates any of the deprecated
// one-field-per-Kind selectors superseded by Resources.
func (r *AccessRule) HasDeprecatedFields() bool {
	return len(r.ClusterTemplateChains) > 0 ||
		len(r.ServiceTemplateChains) > 0 ||
		len(r.Credentials) > 0 ||
		len(r.ClusterAuthentications) > 0 ||
		len(r.DataSources) > 0 ||
		len(r.ClusterAuditPolicies) > 0
}

// EffectiveResources returns the ResourceRules that should actually be resolved/distributed for
// this AccessRule. Resources wins outright when set, even if deprecated fields are also
// populated (mid-migration hand-edited YAML): the two are never merged. Only when Resources is
// completely empty are equivalent entries synthesized from the deprecated one-field-per-Kind
// selectors, so the controller keeps honoring old-styled AccessRules for backward compatibility
// when the mutating webhook that normally performs this migration on write is disabled or
// unavailable — without that fallback, previously-distributed objects for an old-styled rule
// would be silently orphaned (never refreshed, and never cleaned up either) once the admission
// path stops running the migration.
func (r *AccessRule) EffectiveResources() []ResourceRule {
	if len(r.Resources) > 0 {
		return r.Resources
	}

	return synthesizeResourceRules(*r)
}

// synthesizeResourceRules builds the ResourceRule entries equivalent to whatever deprecated
// one-field-per-Kind selectors are populated on rule. Returns nil if none are.
func synthesizeResourceRules(rule AccessRule) []ResourceRule {
	var resources []ResourceRule

	appendResourceRule := func(kind string, names []string) {
		if len(names) == 0 {
			return
		}
		resources = append(resources, ResourceRule{APIVersion: GroupVersion.String(), Kind: kind, Names: names})
	}

	appendResourceRule(ClusterTemplateChainKind, rule.ClusterTemplateChains)
	appendResourceRule(ServiceTemplateChainKind, rule.ServiceTemplateChains)
	appendResourceRule(CredentialKind, rule.Credentials)
	appendResourceRule(ClusterAuthenticationKind, rule.ClusterAuthentications)
	appendResourceRule(DataSourceKind, rule.DataSources)
	appendResourceRule(ClusterAuditPolicyKind, rule.ClusterAuditPolicies)

	return resources
}

// +kubebuilder:validation:XValidation:rule="((has(self.stringSelector) ? 1 : 0) + (has(self.selector) ? 1 : 0) + (has(self.list) ? 1 : 0)) <= 1", message="only one of list, selector, stringSelector can be specified"

// ResourceSelector selects a set of objects/namespaces either by an explicit name list, a structured
// label selector, or a string label selector. At most one of the three may be set.
type ResourceSelector struct {
	// Selector is a structured label query to select objects/namespaces.
	// Mutually exclusive with List and StringSelector.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
	// StringSelector is a label query in string form to select objects/namespaces.
	// Mutually exclusive with List and Selector.
	StringSelector string `json:"stringSelector,omitempty"`
	// List is the list of object/namespace names to select.
	// Mutually exclusive with Selector and StringSelector.
	List []string `json:"list,omitempty"`
}

// TargetNamespaces defines the list of namespaces or the label selector to select namespaces.
// Empty TargetNamespaces selects all namespaces. This is a pure Go-side alias of the generic
// ResourceSelector: the wire format is unchanged.
type TargetNamespaces = ResourceSelector

// +kubebuilder:validation:XValidation:rule="((has(self.names) ? 1 : 0) + (has(self.selector) ? 1 : 0) + (has(self.stringSelector) ? 1 : 0)) <= 1", message="only one of names, selector, stringSelector can be specified"
// +kubebuilder:validation:XValidation:rule="has(self.names) || has(self.selector) || has(self.stringSelector)", message="at least one of names, selector, stringSelector must be set"

// ResourceRule selects a set of objects of a given Kind to distribute into the namespaces
// matched by the enclosing AccessRule's TargetNamespaces. Matching objects are always read
// from the KCM system namespace.
type ResourceRule struct {
	// Selector selects objects in the system namespace by label.
	// Mutually exclusive with Names and StringSelector.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// APIVersion of the referenced Kind, e.g. "k0rdent.mirantis.com/v1beta1" or a custom CRD's
	// "group/version". Defaults to "k0rdent.mirantis.com/v1beta1" if omitted, covering all
	// built-in kinds without requiring users to spell it out.
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`

	// Kind of the referenced objects. Not restricted to an enum: any namespaced Kind the
	// controller has permission to read/write may be referenced, including custom CRDs.
	// Cluster-scoped Kinds are rejected, since AccessManagement only ever distributes
	// namespaced objects.
	Kind string `json:"kind"`

	// StringSelector is a label query in string form.
	// Mutually exclusive with Names and Selector.
	StringSelector string `json:"stringSelector,omitempty"`

	// Names is an explicit list of object names in the system namespace.
	// Mutually exclusive with Selector and StringSelector.
	Names []string `json:"names,omitempty"`
}

// GroupVersionKind returns the parsed schema.GroupVersionKind for the ResourceRule, defaulting
// APIVersion to the built-in k0rdent.mirantis.com/v1beta1 group/version when unset.
func (r *ResourceRule) GroupVersionKind() (schema.GroupVersionKind, error) {
	apiVersion := r.APIVersion
	if apiVersion == "" {
		apiVersion = GroupVersion.String()
	}

	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionKind{}, fmt.Errorf("invalid apiVersion %q: %w", apiVersion, err)
	}

	return gv.WithKind(r.Kind), nil
}

// MigrateAccessRules translates any deprecated one-field-per-Kind selectors still populated on
// each AccessRule into equivalent Resources entries, and defaults the APIVersion of every
// Resources entry (migrated or user-authored) that omits it. It reports whether it changed
// anything, so callers (the mutating webhook) can skip a write when the rules are already
// fully migrated and defaulted.
//
// Migration is additive and idempotent: deprecated fields are cleared once translated, and
// re-running against an already-migrated AccessRule is a no-op. If both a deprecated field and
// a Resources entry for the same Kind are present (e.g. mid-migration hand-edited YAML), the
// translated entry is appended rather than merged with the existing one; duplicate names in
// the resolved set are harmless since the controller de-duplicates by namespaced name when
// distributing objects.
func MigrateAccessRules(rules []AccessRule) ([]AccessRule, bool) {
	if len(rules) == 0 {
		return rules, false
	}

	changed := false
	migrated := make([]AccessRule, len(rules))
	for i, rule := range rules {
		newRule, ruleChanged := migrateAccessRule(rule)
		migrated[i] = newRule
		if ruleChanged {
			changed = true
		}
	}

	return migrated, changed
}

func migrateAccessRule(rule AccessRule) (AccessRule, bool) {
	changed := false

	if synthesized := synthesizeResourceRules(rule); len(synthesized) > 0 {
		rule.Resources = append(rule.Resources, synthesized...)
		changed = true
	}

	if rule.HasDeprecatedFields() {
		rule.ClusterTemplateChains = nil
		rule.ServiceTemplateChains = nil
		rule.Credentials = nil
		rule.ClusterAuthentications = nil
		rule.DataSources = nil
		rule.ClusterAuditPolicies = nil
	}

	for i, res := range rule.Resources {
		if res.APIVersion == "" {
			rule.Resources[i].APIVersion = GroupVersion.String()
			changed = true
		}
	}

	return rule, changed
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=am,scope=Cluster

// AccessManagement is the Schema for the AccessManagements API
type AccessManagement struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AccessManagementSpec   `json:"spec,omitempty"`
	Status AccessManagementStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AccessManagementList contains a list of AccessManagement
type AccessManagementList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AccessManagement `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AccessManagement{}, &AccessManagementList{})
}
