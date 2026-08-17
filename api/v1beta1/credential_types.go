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

// CredentialStatus defines the observed state of Credential
type CredentialStatus struct {
	// +optional

	// ready holds the readiness of [Credential].
	Ready *bool `json:"ready,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	// +kubebuilder:validation:MinItems=0

	// conditions contains details for the current state of the [Credential].
	Conditions []metav1.Condition `json:"conditions,omitempty"`
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
