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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ReleaseKind = "Release"

	// TemplatesCreatedCondition indicates that all templates associated with the Release are created.
	TemplatesCreatedCondition = "TemplatesCreated"
	// TemplatesValidCondition indicates that all templates associated with the Release are valid.
	TemplatesValidCondition = "TemplatesValid"

	KCMRegionalTemplateAnnotation = "k0rdent.mirantis.com/kcm-regional-template"
)

// ReleaseSpec defines the desired state of Release
type ReleaseSpec struct {
	// +required
	// +kubebuilder:validation:MinLength=1

	// version of the KCM Release in the semver format.
	Version string `json:"version,omitempty"`
	// +required

	// kcm references the KCM template.
	KCM CoreProviderTemplate `json:"kcm,omitzero"`
	// +optional

	// regional references the KCM regional template.
	Regional CoreProviderTemplate `json:"regional,omitzero"`
	// +required

	// capi references the Cluster API template.
	CAPI CoreProviderTemplate `json:"capi,omitzero"`
	// +listType=atomic
	// +optional
	// +kubebuilder:validation:MinItems=0

	// providers contains a list of Providers associated with the Release.
	Providers []NamedProviderTemplate `json:"providers,omitempty"`
}

type CoreProviderTemplate struct {
	// +required
	// +kubebuilder:validation:MinLength=1

	// template references the Template associated with the provider.
	Template string `json:"template,omitempty"`
}

type NamedProviderTemplate struct {
	CoreProviderTemplate `json:",inline"`
	// +required
	// +kubebuilder:validation:MinLength=1

	// name of the provider.
	Name string `json:"name,omitempty"`
}

func (in *Release) ProviderTemplate(name string) string {
	for _, p := range in.Spec.Providers {
		if p.Name == name {
			return p.Template
		}
	}
	return ""
}

func (in *Release) Providers() []Provider {
	providers := make([]Provider, 0, len(in.Spec.Providers))
	for _, p := range in.Spec.Providers {
		providers = append(providers, Provider{Name: p.Name})
	}
	return providers
}

func (in *Release) Templates() []string {
	templates := make([]string, 0, len(in.Spec.Providers)+2)
	templates = append(templates, in.Spec.KCM.Template, in.Spec.CAPI.Template)
	kcmRegionalTemplateName := in.getKCMRegionalTemplateName()
	if kcmRegionalTemplateName != "" {
		templates = append(templates, kcmRegionalTemplateName)
	}
	for _, p := range in.Spec.Providers {
		templates = append(templates, p.Template)
	}
	return templates
}

func (in *Release) getKCMRegionalTemplateName() string {
	if in.Spec.Regional.Template != "" {
		return in.Spec.Regional.Template
	}
	return in.Annotations[KCMRegionalTemplateAnnotation]
}

// +kubebuilder:validation:MinProperties=1

// ReleaseStatus defines the observed state of Release
type ReleaseStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	// +kubebuilder:validation:MinItems=0

	// conditions contains details for the current state of the Release
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1

	// observedGeneration is the last observed generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional

	// ready indicates whether KCM is ready to be upgraded to this Release.
	Ready bool `json:"ready,omitempty"`
}

func (in *Release) GetConditions() *[]metav1.Condition {
	return &in.Status.Conditions
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.ready`,description="Denotes Release is ready to be used",priority=0
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="Time elapsed since object creation",priority=0

// Release is the Schema for the releases API
type Release struct {
	metav1.TypeMeta `json:",inline"`
	// +optional

	// metadata contains the object metadata
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional

	// spec defines the desired state
	Spec ReleaseSpec `json:"spec,omitempty"`
	// +optional

	// status describes the observed state
	Status ReleaseStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ReleaseList contains a list of Release
type ReleaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Release `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Release{}, &ReleaseList{})
}
