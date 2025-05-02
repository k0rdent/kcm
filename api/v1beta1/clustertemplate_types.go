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
	"fmt"

	"github.com/Masterminds/semver/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Denotes the clustertemplate resource Kind.
	ClusterTemplateKind = "ClusterTemplate"
	// ChartAnnotationKubernetesVersion is an annotation containing the Kubernetes exact version in the SemVer format associated with a ClusterTemplate.
	ChartAnnotationKubernetesVersion = "k0rdent.mirantis.com/k8s-version"
)

// +kubebuilder:validation:XValidation:rule="!has(self.helm.chartSource)",message=".spec.helm.chartSource is not supported for ClusterTemplates"

// ClusterTemplateSpec defines the desired state of ClusterTemplate
type ClusterTemplateSpec struct {
	Helm HelmSpec `json:"helm"`
	// Holds key-value pairs with compatibility [contract versions],
	// where the key is the name of the provider,
	// and the value is the provider contract version
	// required to be supported by the provider.
	//
	// [contract versions]: https://cluster-api.sigs.k8s.io/developer/providers/contracts
	ProviderContracts CompatibilityContracts `json:"providerContracts,omitempty"`
	// Kubernetes exact version in the SemVer format provided by this ClusterTemplate.
	KubernetesVersion string `json:"k8sVersion,omitempty"`
	// Providers represent required CAPI providers.
	// Should be set if not present in the Helm chart metadata.
	Providers Providers `json:"providers,omitempty"`
}

// ClusterTemplateStatus defines the observed state of ClusterTemplate
type ClusterTemplateStatus struct {
	// Holds key-value pairs with compatibility [contract versions],
	// where the key is the name of the provider,
	// and the value is the provider contract version
	// required to be supported by the provider.
	//
	// [contract versions]: https://cluster-api.sigs.k8s.io/developer/providers/contracts
	ProviderContracts CompatibilityContracts `json:"providerContracts,omitempty"`
	// Kubernetes exact version in the SemVer format provided by this ClusterTemplate.
	KubernetesVersion string `json:"k8sVersion,omitempty"`
	// Providers represent required CAPI providers.
	Providers Providers `json:"providers,omitempty"`

	TemplateStatusCommon `json:",inline"`
}

// FillStatusWithProviders sets the status of the template with providers
// either from the spec or from the given annotations.
func (t *ClusterTemplate) FillStatusWithProviders(annotations map[string]string) error {
	t.Status.Providers = getProvidersList(t.Spec.Providers, annotations)

	contractsStatus, err := getCAPIContracts(t.Kind, t.Spec.ProviderContracts, annotations)
	if err != nil {
		return fmt.Errorf("failed to get CAPI contract versions for ClusterTemplate %s/%s: %w", t.GetNamespace(), t.GetName(), err)
	}

	t.Status.ProviderContracts = contractsStatus

	kversion := annotations[ChartAnnotationKubernetesVersion]
	if t.Spec.KubernetesVersion != "" {
		kversion = t.Spec.KubernetesVersion
	}
	if kversion == "" {
		return nil
	}

	if _, err := semver.NewVersion(kversion); err != nil {
		return fmt.Errorf("failed to parse kubernetes version %s for ClusterTemplate %s/%s: %w", kversion, t.GetNamespace(), t.GetName(), err)
	}

	t.Status.KubernetesVersion = kversion

	return nil
}

// GetSpecProviders returns .spec.providers of the Template.
func (t *ClusterTemplate) GetSpecProviders() Providers {
	return t.Spec.Providers
}

// GetHelmSpec returns .spec.helm of the Template.
func (t *ClusterTemplate) GetHelmSpec() *HelmSpec {
	return &t.Spec.Helm
}

// GetCommonStatus returns common status of the Template.
func (t *ClusterTemplate) GetCommonStatus() *TemplateStatusCommon {
	return &t.Status.TemplateStatusCommon
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=clustertmpl
// +kubebuilder:printcolumn:name="valid",type="boolean",JSONPath=".status.valid",description="Valid",priority=0
// +kubebuilder:printcolumn:name="validationError",type="string",JSONPath=".status.validationError",description="Validation Error",priority=1
// +kubebuilder:printcolumn:name="description",type="string",JSONPath=".status.description",description="Description",priority=1

// ClusterTemplate is the Schema for the clustertemplates API
type ClusterTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Spec is immutable"

	Spec   ClusterTemplateSpec   `json:"spec,omitempty"`
	Status ClusterTemplateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterTemplateList contains a list of ClusterTemplate
type ClusterTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterTemplate{}, &ClusterTemplateList{})
}
