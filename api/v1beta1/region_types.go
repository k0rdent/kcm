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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	RegionKind = "Region"

	RegionFinalizer = "k0rdent.mirantis.com/region"

	KCMRegionLabelKey = "k0rdent.mirantis.com/region"
)

// +kubebuilder:validation:XValidation:rule="has(self.kubeConfig) != has(self.clusterDeployment)",message="exactly one of kubeConfig or clusterDeployment must be set"

// RegionSpec defines the desired state of Region
type RegionSpec struct {
	// KubeConfig references the Secret containing the kubeconfig
	// of the cluster being onboarded as a regional cluster.
	// The Secret must reside in the system namespace.
	KubeConfig *fluxmeta.SecretKeyReference `json:"kubeConfig,omitempty"`

	// ClusterDeployment is the reference to the existing ClusterDeployment object
	// to be onboarded as a regional cluster.
	ClusterDeployment *corev1.ObjectReference `json:"clusterDeployment,omitempty"`

	// ComponentsCommonSpec defines the desired state of regional components.
	ComponentsCommonSpec `json:",inline"`
}

// ComponentsCommonSpec defines the desired state of management or regional Components.
type ComponentsCommonSpec struct {
	// Core holds the core components that are mandatory.
	// If not specified, will be populated with the default values.
	Core *Core `json:"core,omitempty"`

	// Providers is the list of enabled CAPI providers.
	Providers []Provider `json:"providers,omitempty"`
}

// RegionStatus defines the observed state of Region
type RegionStatus struct {
	// ComponentsCommonStatus represents the status of enabled components.
	ComponentsCommonStatus `json:",inline"`

	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type

	// Conditions represents the observations of a Region's current state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the last observed generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// ComponentsCommonStatus defines the observed state of enabled management or regional Components.
type ComponentsCommonStatus struct {
	// For each CAPI provider name holds its compatibility [contract versions]
	// in a key-value pairs, where the key is the core CAPI contract version,
	// and the value is an underscore-delimited (_) list of provider contract versions
	// supported by the core CAPI.
	//
	// [contract versions]: https://cluster-api.sigs.k8s.io/developer/providers/contracts
	CAPIContracts map[string]CompatibilityContracts `json:"capiContracts,omitempty"`
	// Components indicates the status of installed KCM components and CAPI providers.
	Components map[string]ComponentStatus `json:"components,omitempty"`

	// AvailableProviders holds all available CAPI providers.
	AvailableProviders Providers `json:"availableProviders,omitempty"`
}

// Components returns core components and a list of providers defined in the Region object
func (in *Region) Components() ComponentsCommonSpec {
	return in.Spec.ComponentsCommonSpec
}

// KCMTemplate returns the KCM Regional template reference from the Release object
func (*Region) KCMTemplate(release *Release) string {
	return release.Spec.Regional.Template
}

// KCMHelmChartName returns the name of the helm chart with core KCM regional components
func (*Region) KCMHelmChartName() string {
	return CoreKCMRegionalName
}

// HelmReleaseName returns the final name of the HelmRelease managed by this object
func (in *Region) HelmReleaseName(chartName string) string {
	return in.Name + "-" + chartName
}

// GetComponentsStatus returns the common status for enabled components
func (in *Region) GetComponentsStatus() *ComponentsCommonStatus {
	return &in.Status.ComponentsCommonStatus
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rgn,scope=Cluster

// Region is the Schema for the regions API
type Region struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RegionSpec   `json:"spec,omitempty"`
	Status RegionStatus `json:"status,omitempty"`
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
