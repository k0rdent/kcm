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
	// Error is the error message occurred during the reconciliation (if any)
	Error string `json:"error,omitempty"`
	// Current reflects the applied access rules configuration.
	Current []AccessRule `json:"current,omitempty"`
	// ObservedGeneration is the last observed generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// AccessRule is the definition of the AccessManagement access rule. Each AccessRule enforces
// Templates and Credentials distribution to the TargetNamespaces
type AccessRule struct {
	// TargetNamespaces defines the namespaces where selected objects will be distributed.
	// Templates and Credentials will be distributed to all namespaces if unset.
	TargetNamespaces TargetNamespaces `json:"targetNamespaces,omitempty"`
	// ClusterTemplateChains lists the names of ClusterTemplateChains whose ClusterTemplates
	// will be distributed to all namespaces specified in TargetNamespaces.
	ClusterTemplateChains []string `json:"clusterTemplateChains,omitempty"`
	// ServiceTemplateChains lists the names of ServiceTemplateChains whose ServiceTemplates
	// will be distributed to all namespaces specified in TargetNamespaces.
	ServiceTemplateChains []string `json:"serviceTemplateChains,omitempty"`
	// Credentials is the list of Credential names that will be distributed to all the
	// namespaces specified in TargetNamespaces.
	Credentials []string `json:"credentials,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="((has(self.stringSelector) ? 1 : 0) + (has(self.selector) ? 1 : 0) + (has(self.list) ? 1 : 0)) <= 1", message="only one of spec.targetNamespaces.selector or spec.targetNamespaces.stringSelector or spec.targetNamespaces.list can be specified"

// TargetNamespaces defines the list of namespaces or the label selector to select namespaces
type TargetNamespaces struct {
	// StringSelector is a label query to select namespaces.
	// Mutually exclusive with Selector and List.
	StringSelector string `json:"stringSelector,omitempty"`
	// Selector is a structured label query to select namespaces.
	// Mutually exclusive with StringSelector and List.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
	// List is the list of namespaces to select.
	// Mutually exclusive with StringSelector and Selector.
	List []string `json:"list,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:unservedversion
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
