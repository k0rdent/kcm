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
	"errors"
	"net"
	"net/netip"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// ClusterIPAMClaimKind Denotes the clusteripamclaim resource Kind.
	ClusterIPAMClaimKind = "ClusterIPAMClaim"

	// InvalidClaimConditionType Denotes that the claim is invalid
	InvalidClaimConditionType = "InvalidClaimCondition"

	// InClusterProviderName denotes the In-Cluster CAPI IPAM provider name
	InClusterProviderName = "in-cluster"
	// InfobloxProviderName denotes the Infoblox CAPI IPAM provider name
	InfobloxProviderName = "ipam-infoblox"
)

// ClusterIPAMClaimSpec defines the desired state of ClusterIPAMClaim
type ClusterIPAMClaimSpec struct {
	// +kubebuilder:validation:Enum=in-cluster;ipam-infoblox

	// Provider is the name of the provider that this claim will be consumed by
	Provider string `json:"provider"`

	// +kubebuilder:validation:XValidation:rule="oldSelf == '' || self == oldSelf",message="Cluster reference is immutable once set"

	// Cluster is the reference to the [ClusterDeployment] that this claim is for
	Cluster string `json:"cluster,omitempty"`

	// +kubebuilder:validation:XValidation:rule="oldSelf == '' || self == oldSelf",message="ClusterIPAM reference is immutable once set"

	// ClusterIPAMRef is the reference to the [ClusterIPAM] resource that this claim is for
	ClusterIPAMRef string `json:"clusterIPAMRef,omitempty"`

	// NodeNetwork defines the allocation requisitioning ip addresses for cluster nodes
	NodeNetwork AddressSpaceSpec `json:"nodeNetwork,omitempty"`

	// ClusterNetwork defines the allocation for requisitioning ip addresses for use by the k8s cluster itself
	ClusterNetwork AddressSpaceSpec `json:"clusterNetwork,omitempty"`

	// ExternalNetwork defines the allocation for requisitioning ip addresses for use by services such as load balancers
	ExternalNetwork AddressSpaceSpec `json:"externalNetwork,omitempty"`
}

// AddressSpaceSpec defines the ip address space that will be allocated
type AddressSpaceSpec struct {
	// CIDR notation of the allocated address space
	CIDR string `json:"cidr,omitempty"`

	// IPAddresses to be allocated
	IPAddresses []string `json:"ipAddresses,omitempty"`
}

// ClusterIPAMClaimStatus defines the observed state of ClusterIPAMClaim
type ClusterIPAMClaimStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type

	// Conditions contains details for the current state of the [ClusterIPAMClaim]
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +kubebuilder:default:=false

	// Bound is a flag to indicate that the claim is bound because all ip addresses are allocated
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

func (c *ClusterIPAMClaim) Validate() error {
	return errors.Join(c.Spec.NodeNetwork.validate(), c.Spec.ClusterNetwork.validate(), c.Spec.ExternalNetwork.validate())
}

func (a *AddressSpaceSpec) validate() error {
	var err error

	if len(a.CIDR) > 0 {
		_, _, err = net.ParseCIDR(a.CIDR)
	}

	for _, ip := range a.IPAddresses {
		_, ipErr := netip.ParseAddr(ip)
		err = errors.Join(err, ipErr)
	}
	return err
}
