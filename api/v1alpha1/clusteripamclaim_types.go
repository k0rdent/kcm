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

// ClusterIPAMClaimKind Denotes the clusteripamclaim resource Kind.
const ClusterIPAMClaimKind = "ClusterIPAMClaim"

// ClusterIPAMClaimSpec defines the desired state of ClusterIPAMClaim
type ClusterIPAMClaimSpec struct {
	// The provider that this claim will be consumed by
	Provider string `json:"provider,omitempty"`

	// The IP Pool for requisitioning ip addresses for cluster nodes
	NodeIPPool IPPoolSpec `json:"nodeIPPool,omitempty"`

	// The IP Pool for requisitioning ip addresses for use by the k8s cluster itself
	ClusterIPPool IPPoolSpec `json:"clusterIPPool,omitempty"`

	// The IP Pool for requisitioning ip addresses for use by services such as load balancers
	ExternalIPPool IPPoolSpec `json:"externalIPPool,omitempty"`
}

// IPPoolSpec defines the reference to an IP Pool and the number of ips to request
type IPPoolSpec struct {
	// A reference to the ip address pool
	corev1.TypedLocalObjectReference `json:",inline"`

	// +kubebuilder:validation:Minimum=0

	// The number of ip addresses to requisition from the ip pool
	Count int `json:"count"`
}

// ClusterIPAMClaimStatus defines the observed state of ClusterIPAMClaim
type ClusterIPAMClaimStatus struct {
	ClusterIPAMRef string `json:"clusterIPAMRef,omitempty"`

	// +kubebuilder:default:=false

	// flag to indicate that the claim is bound because all ip addresses are allocated
	Bound bool `json:"bound"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="bound",type="string",JSONPath=".status.bound",description="Bound",priority=0
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="Time elapsed since object creation",priority=0

// ClusterIPAMClaim is the Schema for the clusteripamclaims API
type ClusterIPAMClaim struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterIPAMClaimSpec   `json:"spec,omitempty"`
	Status ClusterIPAMClaimStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterIPAMClaimList contains a list of ClusterIPAMClaim
type ClusterIPAMClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterIPAMClaim `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterIPAMClaim{}, &ClusterIPAMClaimList{})
}
