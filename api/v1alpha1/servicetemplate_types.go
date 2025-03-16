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
	"fmt"

	"github.com/Masterminds/semver/v3"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1beta2 "github.com/fluxcd/source-controller/api/v1beta2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Denotes the servicetemplate resource Kind.
	ServiceTemplateKind = "ServiceTemplate"
	// ChartAnnotationKubernetesConstraint is an annotation containing the Kubernetes constrained version in the SemVer format associated with a ServiceTemplate.
	ChartAnnotationKubernetesConstraint = "k0rdent.mirantis.com/k8s-version-constraint"
)

// todo: add CEL validation rules for the ServiceTemplate

// ServiceTemplateSpec defines the desired state of ServiceTemplate
type ServiceTemplateSpec struct {
	// Helm contains the Helm chart information for the template.
	Helm HelmSpec `json:"helm"`

	// Kustomize contains the Kustomize configuration for the template.
	Kustomize *KustomizeSpec `json:"kustomize,omitempty"`

	// Resources contains the resource configuration for the template.
	Resources *ResourceSpec `json:"resources,omitempty"`

	// Authorization is the authorization configuration for the source. Applicable for Git repositories,
	// Helm repositories, and OCI registries.
	Authorization *AuthorizationSpec `json:"authorization,omitempty"`

	// Constraint describing compatible K8S versions of the cluster set in the SemVer format.
	KubernetesConstraint string `json:"k8sConstraint,omitempty"`
}

// todo: add CEL validation rules for the KustomizeSpec

type KustomizeSpec struct {
	// Path to the directory containing the kustomize manifest.
	// +required
	Path string `json:"path"`

	// TargetNamespace is the namespace where the resources will be deployed.
	// +required
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// DeploymentType is the type of the deployment.
	// +kubebuilder:validation:Enum=local;remote
	// +kubebuilder:default=remote
	// +required
	DeploymentType string `json:"deploymentType,omitempty"`

	// LocalSourceRef is the local source of the kustomize manifest.
	// +optional
	LocalSourceRef *LocalSourceRef `json:"localSourceRef,omitempty"`

	// RemoteSourceSpec is the remote source of the kustomize manifest.
	// +optional
	RemoteSourceSpec *RemoteSourceSpec `json:"remoteSourceSpec,omitempty"`
}

// todo: add CEL validation rules for the ResourceSpec

type ResourceSpec struct {
	// Path to the directory containing the resource manifest.
	// +required
	Path string `json:"path"`

	// DeploymentType is the type of the deployment.
	// +kubebuilder:validation:Enum=local;remote
	// +kubebuilder:default=remote
	// +required
	DeploymentType string `json:"deploymentType,omitempty"`

	// LocalSourceRef is the local source of the kustomize manifest.
	// +optional
	LocalSourceRef *LocalSourceRef `json:"localSourceRef,omitempty"`

	// RemoteSourceSpec is the remote source of the kustomize manifest.
	// +optional
	RemoteSourceSpec *RemoteSourceSpec `json:"remoteSourceSpec,omitempty"`
}

type AuthorizationSpec struct {
	SecretRef      *corev1.LocalObjectReference `json:"secretRef,omitempty"`
	CertSecretRef  *corev1.LocalObjectReference `json:"certSecretRef,omitempty"`
	ProxySecretRef *corev1.LocalObjectReference `json:"proxySecretRef,omitempty"`
}

type LocalSourceRef struct {
	// Kind is the kind of the local source.
	// +kubebuilder:validation:Enum=ConfigMap;Secret
	Kind string `json:"kind"`

	// Name is the name of the local source.
	Name string `json:"name"`

	// Namespace is the namespace of the local source.
	Namespace string `json:"namespace"`
}

// todo: move partial source definitions to source_types.go to not to duplicate fields like secret references
// todo: add CEL validation rules for the RemoteSourceSpec

type RemoteSourceSpec struct {
	// Git is the definition of git repository source.
	// +optional
	Git *sourcev1.GitRepositorySpec `json:"git,omitempty"`

	// Bucket is the definition of bucket source.
	// +optional
	Bucket *sourcev1.BucketSpec `json:"bucket,omitempty"`

	// OCI is the definition of OCI repository source.
	// +optional
	OCI *sourcev1beta2.OCIRepositorySpec `json:"oci,omitempty"`
}

// ServiceTemplateStatus defines the observed state of ServiceTemplate
type ServiceTemplateStatus struct {
	// Constraint describing compatible K8S versions of the cluster set in the SemVer format.
	KubernetesConstraint string `json:"k8sConstraint,omitempty"`

	TemplateStatusCommon `json:",inline"`
}

// FillStatusWithProviders sets the status of the template with providers
// either from the spec or from the given annotations.
func (t *ServiceTemplate) FillStatusWithProviders(annotations map[string]string) error {
	kconstraint := annotations[ChartAnnotationKubernetesConstraint]
	if t.Spec.KubernetesConstraint != "" {
		kconstraint = t.Spec.KubernetesConstraint
	}
	if kconstraint == "" {
		return nil
	}

	if _, err := semver.NewConstraint(kconstraint); err != nil {
		return fmt.Errorf("failed to parse kubernetes constraint %s for ServiceTemplate %s/%s: %w", kconstraint, t.GetNamespace(), t.GetName(), err)
	}

	t.Status.KubernetesConstraint = kconstraint

	return nil
}

// GetHelmSpec returns .spec.helm of the Template.
func (t *ServiceTemplate) GetHelmSpec() *HelmSpec {
	return &t.Spec.Helm
}

// GetCommonStatus returns common status of the Template.
func (t *ServiceTemplate) GetCommonStatus() *TemplateStatusCommon {
	return &t.Status.TemplateStatusCommon
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=svctmpl
// +kubebuilder:printcolumn:name="valid",type="boolean",JSONPath=".status.valid",description="Valid",priority=0
// +kubebuilder:printcolumn:name="validationError",type="string",JSONPath=".status.validationError",description="Validation Error",priority=1
// +kubebuilder:printcolumn:name="description",type="string",JSONPath=".status.description",description="Description",priority=1

// ServiceTemplate is the Schema for the servicetemplates API
type ServiceTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Spec is immutable"

	Spec   ServiceTemplateSpec   `json:"spec,omitempty"`
	Status ServiceTemplateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceTemplateList contains a list of ServiceTemplate
type ServiceTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceTemplate{}, &ServiceTemplateList{})
}
