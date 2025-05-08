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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterIPAMSpec defines the desired state of ClusterIPAM
type ClusterIPAMSpec struct {
	// The provider that this claim will be consumed by
	Provider string `json:"provider,omitempty"`

	ClusterIPAMClaimRef string `json:"ClusterIPAMClaimRefs,omitempty"`

	// TODO: I think provider specific configuration should be here similar to how the cluster deployment is configured
	//  Config *apiextensionsv1.JSON `json:"config,omitempty"`
}

// ClusterIPAMStatus defines the observed state of ClusterIPAM
type ClusterIPAMStatus struct {
	// +kubebuilder:validation:Enum=Pending;Bound
	// +kubebuilder:example=`Pending`

	Phase ClusterIPAMPhase `json:"phase,omitempty"`

	ProviderData []ClusterIPAMProviderData `json:"providerData,omitempty"`
}

type ClusterIPAMProviderData struct {
	Name  string                `json:"name,omitempty"`
	Data  *apiextensionsv1.JSON `json:"config,omitempty"`
	Ready bool                  `json:"ready,omitempty"`
}

// The current phase of the ClusterIPAM.
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
