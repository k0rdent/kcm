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

const ClusterTemplateChainKind = "ClusterTemplateChain"

func (*ClusterTemplateChain) Kind() string {
	return ClusterTemplateChainKind
}

func (*ClusterTemplateChain) TemplateKind() string {
	return ClusterTemplateKind
}

func (t *ClusterTemplateChain) GetSpec() *TemplateChainSpec {
	return &t.Spec
}

func (t *ClusterTemplateChain) GetStatus() *TemplateChainStatus {
	return &t.Status
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Valid",type=boolean,JSONPath=`.status.isValid`,description="Is the chain valid",priority=0
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="Time elapsed since object creation",priority=0

// ClusterTemplateChain is the Schema for the clustertemplatechains API
type ClusterTemplateChain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Spec is immutable"

	Spec   TemplateChainSpec   `json:"spec,omitempty"`
	Status TemplateChainStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterTemplateChainList contains a list of ClusterTemplateChain
type ClusterTemplateChainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterTemplateChain `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterTemplateChain{}, &ClusterTemplateChainList{})
}
