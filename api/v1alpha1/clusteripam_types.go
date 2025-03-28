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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterIPAMSpec defines the desired state of ClusterIPAM
type ClusterIPAMSpec struct {
	// The provider that this claim will be consumed by
	Provider string `json:"provider,omitempty"`

	NodeIPClaims []corev1.ObjectReference `json:"nodeIPClaims,omitempty"`

	ClusterIPClaims []corev1.ObjectReference `json:"clusterIPClaims,omitempty"`

	ExternalIPClaims []corev1.ObjectReference `json:"ExternalIPClaims,omitempty"`
}

// ClusterIPAMStatus defines the observed state of ClusterIPAM
type ClusterIPAMStatus struct {
	Phase ClusterIPAMPhase `json:"phase,omitempty"`
	// Conditions contains details for the current state of the ClusterIPAM.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +enum
type ClusterIPAMPhase string

const (
	IpamPhasePending ClusterIPAMPhase = "Pending"
	IpamPhaseBound   ClusterIPAMPhase = "Bound"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="phase",type="string",JSONPath=".status.phase",description="Phase",priority=0

// ClusterIPAM is the Schema for the clusteripams API
type ClusterIPAM struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterIPAMSpec   `json:"spec,omitempty"`
	Status ClusterIPAMStatus `json:"status,omitempty"`
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
