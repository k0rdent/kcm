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

package v1beta1

import (
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	RegionKind = "Region"
)

const (
	RegionFinalizer       = "k0rdent.mirantis.com/region"
	KCMRegionLabelKey     = "k0rdent.mirantis.com/region"
	RegionPauseAnnotation = "k0rdent.mirantis.com/region-pause"
)

const (
	// RegionConfigurationErrorReason declares that the [Region] object has configuration issues.
	RegionConfigurationErrorReason = "ConfigurationError"
)

// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:XValidation:rule="has(self.kubeConfig) != has(self.clusterDeployment)",message="exactly one of kubeConfig or clusterDeployment must be set"

// RegionSpec defines the desired state of Region
type RegionSpec struct {
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="kubeConfig is immutable"
	// +optional

	// kubeConfig references the Secret containing the kubeconfig
	// of the cluster being onboarded as a regional cluster.
	// The Secret must reside in the system namespace.
	KubeConfig *fluxmeta.SecretKeyReference `json:"kubeConfig,omitempty"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="clusterDeployment is immutable"
	// +optional

	// clusterDeployment is the reference to the existing ClusterDeployment object
	// to be onboarded as a regional cluster.
	ClusterDeployment *ClusterDeploymentRef `json:"clusterDeployment,omitempty,omitzero"`

	// ComponentsCommonSpec defines the desired state of regional components.
	ComponentsCommonSpec `json:",inline"`
}

// ClusterDeploymentRef is the reference to the existing ClusterDeployment object.
type ClusterDeploymentRef struct {
	// +required
	// +kubebuilder:validation:MinLength=1

	// namespace identifies the ClusterDeployment namespace
	Namespace string `json:"namespace,omitempty"`
	// +required
	// +kubebuilder:validation:MinLength=1

	// name identifies the ClusterDeployment
	Name string `json:"name,omitempty"`
}

// +kubebuilder:validation:MinProperties=0

// ComponentsCommonSpec defines the desired state of management or regional Components.
type ComponentsCommonSpec struct {
	// +optional

	// core holds the core components that are mandatory.
	// If not specified, will be populated with the default values.
	Core *Core `json:"core,omitempty"`
	// +listType=atomic
	// +optional
	// +kubebuilder:validation:MinItems=0

	// providers is the list of enabled CAPI providers.
	Providers []Provider `json:"providers,omitempty"`
}

// +kubebuilder:validation:MinProperties=1

// RegionStatus defines the observed state of Region
type RegionStatus struct {
	// ComponentsCommonStatus represents the status of enabled components.
	ComponentsCommonStatus `json:",inline"`
	// +listType=map
	// +listMapKey=type
	// +optional
	// +kubebuilder:validation:MinItems=0

	// conditions represents the observations of a Region's current state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1

	// observedGeneration is the last observed generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:validation:MinProperties=1

// ComponentsCommonStatus defines the observed state of enabled management or regional Components.
type ComponentsCommonStatus struct {
	// +optional
	// +kubebuilder:validation:MinProperties=0

	// capiContracts holds compatibility [contract versions] for each CAPI provider
	// in a key-value pairs, where the key is the core CAPI contract version,
	// and the value is an underscore-delimited (_) list of provider contract versions
	// supported by the core CAPI.
	//
	// [contract versions]: https://cluster-api.sigs.k8s.io/developer/providers/contracts
	CAPIContracts map[string]CompatibilityContracts `json:"capiContracts,omitempty"`
	// +optional
	// +kubebuilder:validation:MinProperties=0

	// components indicates the status of installed KCM components and CAPI providers.
	Components map[string]ComponentStatus `json:"components,omitempty"`
	// +optional

	// availableProviders holds all available CAPI providers.
	AvailableProviders Providers `json:"availableProviders,omitempty"`
}

// GetConditions returns Region conditions
func (in *Region) GetConditions() *[]metav1.Condition {
	return &in.Status.Conditions
}

// Components returns core components and a list of providers defined in the Region object
func (in *Region) Components() ComponentsCommonSpec {
	return in.Spec.ComponentsCommonSpec
}

// KCMComponentInfo returns the KCM regional component metadata.
// The kcmReleaseName parameter is accepted for interface consistency but not used
// for regional components (they always use CoreKCMRegionalName).
func (*Region) KCMComponentInfo(release *Release, _ string) KCMComponentInfo {
	return KCMComponentInfo{
		ChartName:       CoreKCMRegionalName,
		DefaultTemplate: release.getKCMRegionalTemplateName(),
		ReleaseName:     CoreKCMRegionalName,
	}
}

// HelmReleasePrefix returns the Region name as a prefix for HelmRelease names.
func (in *Region) HelmReleasePrefix() string {
	return in.Name
}

// GetComponentsStatus returns the common status for enabled components
func (in *Region) GetComponentsStatus() *ComponentsCommonStatus {
	return &in.Status.ComponentsCommonStatus
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rgn,scope=Cluster
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status",description="Overall readiness of the Region resource"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Time duration since creation of Region"

// Region is the Schema for the regions API
type Region struct {
	metav1.TypeMeta `json:",inline"`
	// +optional

	// metadata contains the object metadata
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional

	// spec defines the desired state
	Spec RegionSpec `json:"spec,omitempty"`
	// +optional

	// status describes the observed state
	Status RegionStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// RegionList contains a list of Regions
type RegionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Region `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Region{}, &RegionList{})
}
