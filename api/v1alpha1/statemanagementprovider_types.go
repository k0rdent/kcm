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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const StateManagementProviderKind = "StateManagementProvider"

// StateManagementProviderSpec defines the desired state of StateManagementProvider
type StateManagementProviderSpec struct {
	// Adapter is the adapter to use for the state management provider
	Adapter ResourceReference `json:"adapter"`

	// Provisioners is a list of provisioners to use for the state management provider
	Provisioners []ResourceReference `json:"provisioners"`

	// GVK is the GVK of the resources used by the state management provisioners
	GVK []GVKSpec `json:"gvk"`
}

// ResourceReference is a cross-namespace reference to a resource
type ResourceReference struct {
	// APIVersion is the API version of the resource
	APIVersion string `json:"apiVersion,omitempty"`

	// Kind is the kind of the resource
	Kind string `json:"kind"`

	// Name is the name of the resource
	Name string `json:"name"`

	// Namespace is the namespace of the resource
	Namespace string `json:"namespace"`
}

// GVKSpec is a GVK for a resource
type GVKSpec struct {
	// APIVersion is the API version of the resource
	APIVersion string `json:"apiVersion"`

	// Resources is the list of resources under given APIVersion
	Resources []string `json:"kind"`
}

// StateManagementProviderStatus defines the observed state of StateManagementProvider
type StateManagementProviderStatus struct {
	// Conditions is a list of conditions for the state management provider
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Valid is true if the state management provider is valid
	Valid bool `json:"valid"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=smp

// StateManagementProvider is the Schema for the statemanagementproviders API
type StateManagementProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Spec is immutable"

	Spec   StateManagementProviderSpec   `json:"spec,omitempty"`
	Status StateManagementProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// StateManagementProviderList contains a list of StateManagementProvider
type StateManagementProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&StateManagementProvider{}, &StateManagementProviderList{})
}
