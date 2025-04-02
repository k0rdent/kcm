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

const ServiceTemplateChainKind = "ServiceTemplateChain"

func (*ServiceTemplateChain) Kind() string {
	return ServiceTemplateChainKind
}

func (*ServiceTemplateChain) TemplateKind() string {
	return ServiceTemplateKind
}

func (t *ServiceTemplateChain) GetSpec() *TemplateChainSpec {
	return &t.Spec
}

func (t *ServiceTemplateChain) GetStatus() *TemplateChainStatus {
	return &t.Status
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Valid",type=boolean,JSONPath=`.status.isValid`,description="Is the chain valid",priority=0
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="Time elapsed since object creation",priority=0

// ServiceTemplateChain is the Schema for the servicetemplatechains API
type ServiceTemplateChain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Spec is immutable"

	Spec   TemplateChainSpec   `json:"spec,omitempty"`
	Status TemplateChainStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceTemplateChainList contains a list of ServiceTemplateChain
type ServiceTemplateChainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceTemplateChain `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceTemplateChain{}, &ServiceTemplateChainList{})
}
