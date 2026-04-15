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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	CredentialKind = "Credential"

	// CredentialReadyCondition indicates if referenced Credential exists and has Ready state
	CredentialReadyCondition = "CredentialReady"

	// CredentialLabelKeyPrefix is a label key prefix applied to all ClusterIdentity objects and their references.
	// Each managed ClusterIdentity will have this label set in format of:
	// k0rdent.mirantis.com/credential.<cred-namespace>.<cred-name>: true
	// Which means that this ClusterIdentity is managed by the Credential `cred-namespace/cred-name`.
	// One ClusterIdentity can be managed by multiple Credential objects.
	CredentialLabelKeyPrefix = "k0rdent.mirantis.com/credential"

	CredentialFinalizer = "k0rdent.mirantis.com/credential"
)

// CredentialSpec defines the desired state of Credential
type CredentialSpec struct {
	// +optional

	// projectionConfig defines how cloud credentials should be projected
	// onto child clusters. When set, the KCM controller will render the
	// referenced ConfigMap template using the identity objects and apply
	// the resulting resources directly to the child cluster
	ProjectionConfig CredentialProjectionConfig `json:"projectionConfig,omitzero"`
	// +required

	// identityRef references the Credential identity
	IdentityRef *corev1.ObjectReference `json:"identityRef,omitempty"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Region is immutable"
	// +optional
	// +kubebuilder:validation:MinLength=1

	// region specifies the region where [ClusterDeployment] resources using
	// this [Credential] will be deployed
	Region string `json:"region,omitempty"`
	// +optional
	// +kubebuilder:validation:MinLength=1

	// description of the [Credential] object
	Description string `json:"description,omitempty"` // WARN: noop
}

// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:XValidation:rule="(has(self.resourceTemplateRef) ? !has(self.resourceTemplate): true)",message="resourceTemplateRef and resourceTemplate are mutually exclusive"
// +kubebuilder:validation:XValidation:rule="(has(self.resourceTemplate) ? !has(self.resourceTemplateRef): true)",message="resourceTemplateRef and resourceTemplate are mutually exclusive"
// +kubebuilder:validation:XValidation:rule="has(self.resourceTemplateRef) || (has(self.resourceTemplate) && self.resourceTemplate != '')",message="one of resourceTemplateRef or non-empty resourceTemplate must be set"
// +kubebuilder:validation:XValidation:rule="!has(self.resourceTemplateRef) || self.resourceTemplateRef.name != ''",message="resourceTemplateRef.name must not be empty when set"
// +kubebuilder:validation:XValidation:rule="!has(self.secretDataRef) || self.secretDataRef.name != ''",message="secretDataRef.name must not be empty when set"

// CredentialProjectionConfig defines how credentials are projected onto child clusters
type CredentialProjectionConfig struct {
	// +optional

	// secretDataRef is an optional explicit reference to the companion Secret
	// that accompanies a non-Secret identity object. When set, this Secret is
	// available in templates as .IdentitySecret.
	// When not set, the companion secret is not populated and templates must not
	// reference it
	SecretDataRef *corev1.LocalObjectReference `json:"secretDataRef,omitempty"`
	// +optional

	// resourceTemplateRef is a reference to a ConfigMap in the same namespace
	// containing Go text/template entries under its data keys. Each key's template
	// is rendered with the identity objects as data and the resulting YAML manifests
	// are applied to the child cluster
	//
	// Mutually exclusive with .resourceTemplate
	ResourceTemplateRef *corev1.LocalObjectReference `json:"resourceTemplateRef,omitempty"`
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=65536

	// resourceTemplate is an inline Go text/template string. When set, the template
	// is rendered with the identity objects as data and the resulting YAML manifests
	// are applied to the child cluster
	//
	// Mutually exclusive with .resourceTemplateRef
	ResourceTemplate string `json:"resourceTemplate,omitempty"`
}

// IsZero reports whether the projection config carries no user-provided
// configuration. It is also consumed by encoding/json via the omitzero tag on
// [CredentialSpec.ProjectionConfig], hence an empty config is never serialized.
func (c *CredentialProjectionConfig) IsZero() bool {
	return c == nil || (c.ResourceTemplate == "" && c.ResourceTemplateRef == nil && c.SecretDataRef == nil)
}

// +kubebuilder:validation:MinProperties=1

// CredentialStatus defines the observed state of Credential
type CredentialStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	// +kubebuilder:validation:MinItems=0

	// conditions contains details for the current state of the [Credential].
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	// +default=false

	// ready holds the readiness of [Credential].
	Ready bool `json:"ready,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cred
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="Description",type=string,JSONPath=`.spec.description`

// Credential is the Schema for the credentials API
type Credential struct {
	metav1.TypeMeta `json:",inline"`
	// +optional

	// metadata contains the object metadata
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +required

	// spec defines the desired state
	Spec CredentialSpec `json:"spec,omitzero"`
	// +optional

	// status describes the observed state
	Status CredentialStatus `json:"status,omitempty,omitzero"`
}

func (in *Credential) GetConditions() *[]metav1.Condition {
	return &in.Status.Conditions
}

// +kubebuilder:object:root=true

// CredentialList contains a list of Credential
type CredentialList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Credential `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Credential{}, &CredentialList{})
}
