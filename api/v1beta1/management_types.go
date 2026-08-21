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
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	CoreKCMName         = "kcm"
	CoreKCMRegionalName = "kcm-regional"

	CoreCAPIName = "capi"

	ManagementKind      = "Management"
	ManagementName      = "kcm"
	ManagementFinalizer = "k0rdent.mirantis.com/management"

	K0rdentManagementClusterLabelKey   = "k0rdent.mirantis.com/management-cluster"
	K0rdentManagementClusterLabelValue = "true"
)

// ManagementSpec defines the desired state of Management
type ManagementSpec struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +required

	// release references the Release object.
	Release string `json:"release,omitempty"`

	// ComponentsCommonSpec defines the desired state of management components.
	ComponentsCommonSpec `json:",inline"`
	// +optional

	// cleanup configures CRD removal behaviour when the Management object is deleted.
	// CRD deletion is issued without waiting for it to complete (fire-and-forget).
	Cleanup ManagementCleanup `json:"cleanup,omitempty,omitzero"`
}

// +kubebuilder:validation:MinProperties=0

// ManagementCleanup controls which CRDs are removed when a Management object is deleted.
// CRD deletion is issued without waiting for it to complete (fire-and-forget).
type ManagementCleanup struct {
	// +optional

	// k0rdentCRDs indicates whether k0rdent-owned CRDs should be removed when a Management is deleted.
	// Note: this removes the Management CRD itself as part of the sweep.
	K0rdentCRDs bool `json:"k0rdentCRDs,omitempty"`
	// +optional

	// capiProviderCRDs indicates whether CRDs installed by the CAPI operator should be removed on
	// Management deletion. Note: this removes all CAPI provider CRDs on the cluster,
	// including any that were not installed by k0rdent.
	CAPIProviderCRDs bool `json:"capiProviderCRDs,omitempty"`
}

const (
	// AllComponentsHealthyReason surfaces overall readiness of Management's components.
	AllComponentsHealthyReason = "AllComponentsHealthy"
	// NotAllComponentsHealthyReason documents a condition not in Status=True because one or more components are failing.
	NotAllComponentsHealthyReason = "NotAllComponentsHealthy"
	// ReleaseIsNotFoundReason declares that the referenced in the [Management] [Release] object does not (yet) exist.
	ReleaseIsNotFoundReason = "ReleaseIsNotFound"
	// ReleaseIsNotReadyReason declares that the referenced in the [Management] [Release] object is not (yet) ready.
	ReleaseIsNotReadyReason = "ReleaseIsNotReady"
	// ReleaseIsNotObserved declares that the referenced in the [Management] [Release] object is not (yet) observed.
	ReleaseIsNotObserved = "ReleaseIsNotObserved"
	// HasIncompatibleContractsReason declares that the [Management] object has incompatible CAPI contracts in providers.
	HasIncompatibleContractsReason = "HasIncompatibleContracts"
)

const (
	// RegistryCredentialSecretReadyCondition indicates the registry credential secret has been created.
	RegistryCredentialSecretReadyCondition = "RegistryCredentialSecretReady"
)

// +kubebuilder:validation:MinProperties=0

// Core represents a structure describing core Management components.
type Core struct {
	// +optional

	// kcm represents the core KCM component and references the KCM template.
	KCM Component `json:"kcm,omitempty,omitzero"`
	// +optional

	// capi represents the core Cluster API component and references the Cluster API template.
	CAPI Component `json:"capi,omitempty,omitzero"`
}

// KCMComponentInfo holds KCM-specific component metadata used during reconciliation.
type KCMComponentInfo struct {
	// +kubebuilder:validation:MinLength=1

	// ChartName is the name of the KCM Helm chart (e.g., "kcm" or "kcm-regional").
	ChartName string
	// +kubebuilder:validation:MinLength=1

	// DefaultTemplate is the default ProviderTemplate name from the Release.
	DefaultTemplate string
	// +kubebuilder:validation:MinLength=1

	// ReleaseName is the Helm release name (spec.releaseName in the HelmRelease).
	ReleaseName string
}

// +kubebuilder:validation:MinProperties=1

// Component represents KCM management or regional component
type Component struct {
	// +optional

	// config allows to provide parameters for management component customization.
	// If no Config provided, the field will be populated with the default
	// values for the template.
	Config *apiextv1.JSON `json:"config,omitempty"`
	// +optional
	// +kubebuilder:validation:MinLength=1

	// template is the name of the Template associated with this component.
	// If not specified, will be taken from the Release object.
	Template string `json:"template,omitempty"`
}

type Provider struct { //nolint:recvcheck // false-positive
	Component `json:",inline"`
	// +required
	// +kubebuilder:validation:MinLength=1

	// name of the provider.
	Name string `json:"name,omitempty"`
}

func (p Provider) String() string {
	return p.Name
}

func (in *Component) HelmValues() (values map[string]any, err error) {
	if in.Config != nil {
		err = yaml.Unmarshal(in.Config.Raw, &values)
	}
	return values, err
}

// Templates returns a list of provider templates explicitly defined in the Management object
func (in *Management) Templates() []string {
	templates := []string{}
	if in.Spec.Core != nil {
		if in.Spec.Core.CAPI.Template != "" {
			templates = append(templates, in.Spec.Core.CAPI.Template)
		}
		if in.Spec.Core.KCM.Template != "" {
			templates = append(templates, in.Spec.Core.KCM.Template)
		}
	}
	for _, p := range in.Spec.Providers {
		if p.Template != "" {
			templates = append(templates, p.Template)
		}
	}
	return templates
}

// +kubebuilder:validation:MinProperties=1

// ManagementStatus defines the observed state of Management
type ManagementStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	// +kubebuilder:validation:MinItems=0

	// conditions represents the observations of a Management's current state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	// +kubebuilder:validation:MinLength=1

	// backupName is a name of the management cluster scheduled backup.
	BackupName string `json:"backupName,omitempty"`
	// +optional
	// +kubebuilder:validation:MinLength=1

	// release indicates the current Release object.
	Release string `json:"release,omitempty"`

	// ComponentsCommonStatus represents the status of enabled components.
	ComponentsCommonStatus `json:",inline"`
	// +optional
	// +kubebuilder:validation:Minimum=1

	// observedGeneration is the last observed generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// ComponentStatus is the status of Management component installation
type ComponentStatus struct {
	// +optional
	// +kubebuilder:validation:MinLength=1

	// template is the name of the Template associated with this component.
	Template string `json:"template,omitempty"`
	// +optional
	// +kubebuilder:validation:MinLength=1

	// error stores as error message in case of failed installation
	Error string `json:"error,omitempty"`
	// +optional

	// exposedProviders is a list of CAPI providers this component exposes
	ExposedProviders Providers `json:"exposedProviders,omitempty"`
	// +optional

	// success represents if a component installation was successful
	Success bool `json:"success,omitempty"`
}

func (in *Management) GetConditions() *[]metav1.Condition {
	return &in.Status.Conditions
}

// Components returns core components and a list of providers defined in the Management object
func (in *Management) Components() ComponentsCommonSpec {
	return in.Spec.ComponentsCommonSpec
}

// KCMComponentInfo returns the KCM component metadata.
// The kcmReleaseName parameter should be provided by the controller from its configuration.
func (*Management) KCMComponentInfo(release *Release, kcmReleaseName string) KCMComponentInfo {
	return KCMComponentInfo{
		ChartName:       CoreKCMName,
		DefaultTemplate: release.Spec.KCM.Template,
		ReleaseName:     kcmReleaseName,
	}
}

// HelmReleasePrefix returns an empty string since Management HelmReleases don't need a prefix.
func (*Management) HelmReleasePrefix() string {
	return ""
}

// GetComponentsStatus returns the common status for enabled components
func (in *Management) GetComponentsStatus() *ComponentsCommonStatus {
	return &in.Status.ComponentsCommonStatus
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:shortName=kcm-mgmt;mgmt,scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status",description="Overall readiness of the Management resource"
// +kubebuilder:printcolumn:name="Release",type="string",JSONPath=".status.release",description="Current release version"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Time duration since creation of Management"

// Management is the Schema for the managements API
type Management struct {
	metav1.TypeMeta `json:",inline"`
	// +optional

	// metadata contains the object metadata
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional

	// spec defines the desired state
	Spec ManagementSpec `json:"spec,omitempty"`
	// +optional

	// status describes the observed state
	Status ManagementStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ManagementList contains a list of Management
type ManagementList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Management `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Management{}, &ManagementList{})
}
