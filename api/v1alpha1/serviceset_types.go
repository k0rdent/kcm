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

const ServiceSetKind = "ServiceSet"

// ServiceSetSpec defines the desired state of ServiceSet
type ServiceSetSpec struct {
	// Provider is a reference to the StateManagementProvider to use for the ServiceSet
	Provider string `json:"provider"`

	// Cluster is a reference to the ClusterDeployment ServiceSet is associated with
	Cluster string `json:"cluster"`

	// Services is a list of Services to be deployed
	Services []Service `json:"services"`
}

// ServiceSetStatus defines the observed state of ServiceSet
type ServiceSetStatus struct {
	// Services is a list of Service states in the ServiceSet
	Services []ServiceState `json:"services,omitempty"`
}

// ServiceState is the state of a Service
type ServiceState struct {
	// LastStateTransitionTime is the time the State was last transitioned
	LastStateTransitionTime *metav1.Time       `json:"lastStateTransitionTime"`

	// Name is the name of the Service
	Name                    string             `json:"name"`

	// Namespace is the namespace of the Service
	Namespace               string             `json:"namespace"`

	// Version is the version of the Service
	Version                 string             `json:"version"`

	// State is the state of the Service
	State                   string             `json:"state"`

	// Conditions is a list of conditions for the Service
	Conditions              []metav1.Condition `json:"conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ServiceSet is the Schema for the servicesets API
type ServiceSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Spec is immutable"

	Spec   ServiceSetSpec   `json:"spec,omitempty"`
	Status ServiceSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceSetList contains a list of ServiceSet
type ServiceSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceSet{}, &ServiceSetList{})
}
