// Copyright 2025
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

const (
	// ProviderInterfaceKind represents the kind for provider interfaces
	ProviderInterfaceKind = "ProviderInterface"

	// InfrastructureProviderPrefix is the prefix used for infrastructure provider names
	InfrastructureProviderPrefix = "infrastructure-"
)

// GroupVersionKind unambiguously identifies a kind. It doesn't anonymously include GroupVersion
// to avoid automatic coercion. It doesn't use a GroupVersion to avoid custom marshalling
// Note: mirror of https://github.com/kubernetes/apimachinery/blob/v0.32.3/pkg/runtime/schema/group_version.go#L140-L146
type GroupVersionKind struct {
	Group   string `json:"group"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

// ProviderInterfaceSpec defines the desired state of ProviderInterface
type ProviderInterfaceSpec struct {
	// ClusterGVKs defines the Group-Version-Kind resources this provider can manage
	ClusterGVKs []GroupVersionKind `json:"clusterGVKs,omitempty"`

	// ClusterIdentityKinds defines the Kind of identity objects supported by this provider
	ClusterIdentityKinds []string `json:"clusterIdentityKinds,omitempty"`

	// Description provides a human-readable explanation of what this provider does
	Description string `json:"description,omitempty"`
}

// ProviderInterfaceStatus defines the observed state of ProviderInterface
type ProviderInterfaceStatus struct {
	// ExposedProviders contains the list of exposed provider
	ExposedProviders string `json:"exposedProviders,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=pi,scope=Cluster
// +kubebuilder:printcolumn:name="Providers",type=string,JSONPath=`.status.exposedProviders`
// +kubebuilder:printcolumn:name="Description",type=string,JSONPath=`.spec.description`

// ProviderInterface is the Schema for the ProviderInterface API
type ProviderInterface struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProviderInterfaceSpec   `json:"spec,omitempty"`
	Status ProviderInterfaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProviderInterfaceList contains a list of ProviderInterfaces
type ProviderInterfaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderInterface `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProviderInterface{}, &ProviderInterfaceList{})
}
