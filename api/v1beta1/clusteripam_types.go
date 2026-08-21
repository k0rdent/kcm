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
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:MinProperties=0

// ClusterIPAMSpec defines the desired state of ClusterIPAM
type ClusterIPAMSpec struct {
	// +kubebuilder:validation:Enum=in-cluster;ipam-infoblox
	// +optional

	// provider identifies the provider that will consume this claim
	Provider string `json:"provider,omitempty"`
	// +kubebuilder:validation:XValidation:rule="oldSelf == '' || self == oldSelf",message="Claim reference is immutable once set"
	// +optional
	// +kubebuilder:validation:MinLength=1

	// clusterIPAMClaimRef is a reference to the [ClusterIPAMClaim] that this [ClusterIPAM] is bound to.
	ClusterIPAMClaimRef string `json:"clusterIPAMClaimRef,omitempty"`
}

// +kubebuilder:validation:MinProperties=1

// ClusterIPAMStatus defines the observed state of ClusterIPAM
type ClusterIPAMStatus struct {
	// +kubebuilder:validation:Enum=Pending;Bound
	// +kubebuilder:example=`Pending`
	// +optional

	// phase is the current phase of the ClusterIPAM.
	Phase ClusterIPAMPhase `json:"phase,omitempty"`
	// +optional
	// +listType=atomic

	// providerData is the provider specific data produced for the ClusterIPAM.
	// This field is represented as a list, because it will store multiple entries
	// for different networks - nodes, cluster (pods, services), external - for
	// the same provider.
	ProviderData []ClusterIPAMProviderData `json:"providerData,omitempty"`
}

// +kubebuilder:validation:MinProperties=1

type ClusterIPAMProviderData struct {
	// +optional

	// config contains the IPAM provider-specific data
	Data *apiextv1.JSON `json:"config,omitempty"`
	// +optional
	// +kubebuilder:validation:MinLength=1

	// name of the IPAM provider data
	Name string `json:"name,omitempty"`
	// +optional

	// ready indicates that the IPAM provider data is ready
	Ready bool `json:"ready,omitempty"`
}

// ClusterIPAMPhase represents the phase of the [ClusterIPAM].
type ClusterIPAMPhase string

const (
	ClusterIPAMPhasePending ClusterIPAMPhase = "Pending"
	ClusterIPAMPhaseBound   ClusterIPAMPhase = "Bound"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="phase",type="string",JSONPath=".status.phase",description="Phase",priority=0
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="Time elapsed since object creation",priority=0

// ClusterIPAM is the Schema for the clusteripams API
type ClusterIPAM struct {
	metav1.TypeMeta `json:",inline"`
	// +optional

	// metadata contains the object metadata
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional

	// spec defines the desired state
	Spec ClusterIPAMSpec `json:"spec,omitempty"`
	// +optional

	// status describes the observed state
	Status ClusterIPAMStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ClusterIPAMList contains a list of ClusterIPAM
type ClusterIPAMList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterIPAM `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterIPAM{}, &ClusterIPAMList{})
}
