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
	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ServiceTemplateKind denotes the servicetemplate resource Kind.
	ServiceTemplateKind = "ServiceTemplate"
	// ChartAnnotationKubernetesConstraint is an annotation containing the Kubernetes constrained version in the SemVer format associated with a ServiceTemplate.
	ChartAnnotationKubernetesConstraint = "k0rdent.mirantis.com/k8s-version-constraint"

	SecretKind    = "Secret"
	ConfigMapKind = "ConfigMap"
)

// +kubebuilder:validation:XValidation:rule="has(self.helm) ? (!has(self.kustomize) && !has(self.resources)): true",message="Helm, Kustomize and Resources are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.kustomize) ? (!has(self.helm) && !has(self.resources)): true",message="Helm, Kustomize and Resources are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.resources) ? (!has(self.kustomize) && !has(self.helm)): true",message="Helm, Kustomize and Resources are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.helm) || has(self.kustomize) || has(self.resources)",message="One of Helm, Kustomize, or Resources must be specified."

// ServiceTemplateSpec defines the desired state of ServiceTemplate
type ServiceTemplateSpec struct {
	// HelmOptions are the global options to use when installing or updating the helm chart.
	HelmOptions *ServiceHelmOptions `json:"helmOptions,omitempty"`

	// Helm contains the Helm chart information for the template.
	Helm *HelmSpec `json:"helm,omitempty"`

	// Kustomize contains the Kustomize configuration for the template.
	Kustomize *SourceSpec `json:"kustomize,omitempty"`

	// Resources contains the resource configuration for the template.
	Resources *SourceSpec `json:"resources,omitempty"`

	// Version is the semantic version of the application backed by template.
	Version string `json:"version,omitempty"`

	// Constraint describing compatible K8S versions of the cluster set in the SemVer format.
	KubernetesConstraint string `json:"k8sConstraint,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.localSourceRef) ? !has(self.remoteSourceSpec): true",message="LocalSource and RemoteSource are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.remoteSourceSpec) ? !has(self.localSourceRef): true",message="LocalSource and RemoteSource are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.localSourceRef) || has(self.remoteSourceSpec)",message="One of LocalSource or RemoteSource must be specified."

// SourceSpec defines the desired state of the source.
type SourceSpec struct {
	// LocalSourceRef is the local source of the kustomize manifest.
	LocalSourceRef *LocalSourceRef `json:"localSourceRef,omitempty"`

	// RemoteSourceSpec is the remote source of the kustomize manifest.
	RemoteSourceSpec *RemoteSourceSpec `json:"remoteSourceSpec,omitempty"`

	// +kubebuilder:validation:Enum=Local;Remote
	// +kubebuilder:default=Remote

	// DeploymentType is the type of the deployment. This field is ignored,
	// when ResourceSpec is used as part of Helm chart configuration.
	DeploymentType string `json:"deploymentType"`

	// Path to the directory containing the resource manifest.
	Path string `json:"path"`
}

// LocalSourceRef defines the reference to the local resource to be used as the source.
type LocalSourceRef struct {
	// +kubebuilder:validation:Enum=ConfigMap;Secret;GitRepository;Bucket;OCIRepository

	// Kind is the kind of the local source.
	Kind string `json:"kind"`

	// Name is the name of the local source.
	Name string `json:"name"`

	// Namespace is the namespace of the local source. Cross-namespace references
	// are only allowed when the Kind is one of [github.com/fluxcd/source-controller/api/v1.GitRepository],
	// [github.com/fluxcd/source-controller/api/v1.Bucket] or [github.com/fluxcd/source-controller/api/v1.OCIRepository].
	// If the Kind is ConfigMap or Secret, the namespace will be ignored.
	Namespace string `json:"namespace,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.git) ? (!has(self.bucket) && !has(self.oci)) : true",message="Git, Bucket and OCI are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.bucket) ? (!has(self.git) && !has(self.oci)) : true",message="Git, Bucket and OCI are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.oci) ? (!has(self.git) && !has(self.bucket)) : true",message="Git, Bucket and OCI are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.git) || has(self.bucket) || has(self.oci)",message="One of Git, Bucket or OCI must be specified."

// RemoteSourceSpec defines the desired state of the remote source (Git, Bucket, OCI).
type RemoteSourceSpec struct {
	// Git is the definition of git repository source.
	Git *EmbeddedGitRepositorySpec `json:"git,omitempty"`

	// Bucket is the definition of bucket source.
	Bucket *EmbeddedBucketSpec `json:"bucket,omitempty"`

	// OCI is the definition of OCI repository source.
	OCI *EmbeddedOCIRepositorySpec `json:"oci,omitempty"`
}

// EmbeddedGitRepositorySpec is the embedded [github.com/fluxcd/source-controller/api/v1.GitRepositorySpec].
type EmbeddedGitRepositorySpec struct {
	sourcev1.GitRepositorySpec `json:",inline"`
}

// EmbeddedBucketSpec is the embedded [github.com/fluxcd/source-controller/api/v1.BucketSpec].
type EmbeddedBucketSpec struct {
	sourcev1.BucketSpec `json:",inline"`
}

// EmbeddedOCIRepositorySpec is the embedded [github.com/fluxcd/source-controller/api/v1.OCIRepositorySpec].
type EmbeddedOCIRepositorySpec struct {
	sourcev1.OCIRepositorySpec `json:",inline"`
}

// ServiceTemplateStatus defines the observed state of ServiceTemplate
type ServiceTemplateStatus struct {
	// Constraint describing compatible K8S versions of the cluster set in the SemVer format.
	KubernetesConstraint string `json:"k8sConstraint,omitempty"`

	// SourceStatus reflects the status of the source.
	SourceStatus *SourceStatus `json:"sourceStatus,omitempty"`

	TemplateStatusCommon `json:",inline"`
}

// SourceStatus reflects the status of the source.
type SourceStatus struct {
	// Kind is the kind of the remote source.
	Kind string `json:"kind"`

	// Name is the name of the remote source.
	Name string `json:"name"`

	// Namespace is the namespace of the remote source.
	Namespace string `json:"namespace"`

	// Artifact is the artifact that was generated from the template source.
	Artifact *fluxmeta.Artifact `json:"artifact,omitempty"`
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type

	// Conditions reflects the conditions of the remote source object.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the latest source generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
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
	return t.Spec.Helm
}

// GetCommonStatus returns common status of the Template.
func (t *ServiceTemplate) GetCommonStatus() *TemplateStatusCommon {
	return &t.Status.TemplateStatusCommon
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
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

// HelmChartSpec returns the ChartSpec of the ServiceTemplate if defined,
// otherwise returns nil.
func (t *ServiceTemplate) HelmChartSpec() *sourcev1.HelmChartSpec {
	switch {
	case t.Spec.Helm != nil:
		return t.Spec.Helm.ChartSpec
	default:
		return nil
	}
}

// HelmChartRef returns the ChartRef of the ServiceTemplate if defined,
// otherwise returns nil.
func (t *ServiceTemplate) HelmChartRef() *helmcontrollerv2.CrossNamespaceSourceReference {
	switch {
	case t.Spec.Helm != nil:
		return t.Spec.Helm.ChartRef
	default:
		return nil
	}
}

// LocalSourceRef returns the LocalSourceRef of the ServiceTemplate if defined,
// otherwise returns nil.
func (t *ServiceTemplate) LocalSourceRef() *LocalSourceRef {
	switch {
	case t.Spec.Helm != nil && t.Spec.Helm.ChartSource != nil:
		return t.Spec.Helm.ChartSource.LocalSourceRef
	case t.Spec.Kustomize != nil:
		return t.Spec.Kustomize.LocalSourceRef
	case t.Spec.Resources != nil:
		return t.Spec.Resources.LocalSourceRef
	default:
		return nil
	}
}

// RemoteSourceSpec returns the RemoteSourceSpec of the ServiceTemplate if defined,
// otherwise returns nil.
func (t *ServiceTemplate) RemoteSourceSpec() *RemoteSourceSpec {
	switch {
	case t.Spec.Helm != nil && t.Spec.Helm.ChartSource != nil:
		return t.Spec.Helm.ChartSource.RemoteSourceSpec
	case t.Spec.Kustomize != nil:
		return t.Spec.Kustomize.RemoteSourceSpec
	case t.Spec.Resources != nil:
		return t.Spec.Resources.RemoteSourceSpec
	default:
		return nil
	}
}

// LocalSourceObject returns the client.Object and kind of the defined local source.
// If the ServiceTemplate does not reference a local source, it returns nil.
func (t *ServiceTemplate) LocalSourceObject() (client.Object, string) {
	localSourceRef := t.LocalSourceRef()
	if localSourceRef == nil {
		return nil, ""
	}
	namespace := localSourceRef.Namespace
	switch {
	case localSourceRef.Kind == SecretKind:
		fallthrough
	case localSourceRef.Kind == ConfigMapKind:
		fallthrough
	case namespace == "":
		namespace = t.Namespace
	}
	localSourceMeta := metav1.ObjectMeta{
		Name:      localSourceRef.Name,
		Namespace: namespace,
	}

	switch localSourceRef.Kind {
	case SecretKind:
		return &corev1.Secret{
			ObjectMeta: localSourceMeta,
		}, SecretKind
	case ConfigMapKind:
		return &corev1.ConfigMap{
			ObjectMeta: localSourceMeta,
		}, ConfigMapKind
	case sourcev1.GitRepositoryKind:
		return &sourcev1.GitRepository{
			ObjectMeta: localSourceMeta,
		}, sourcev1.GitRepositoryKind
	case sourcev1.BucketKind:
		return &sourcev1.Bucket{
			ObjectMeta: localSourceMeta,
		}, sourcev1.BucketKind
	case sourcev1.OCIRepositoryKind:
		return &sourcev1.OCIRepository{
			ObjectMeta: localSourceMeta,
		}, sourcev1.OCIRepositoryKind
	default:
		return nil, ""
	}
}

// RemoteSourceObject returns the client.Object and kind of the defined remote source.
// If the ServiceTemplate does not define a remote source, returns nil and empty string.
func (t *ServiceTemplate) RemoteSourceObject() (client.Object, string) {
	remoteSourceSpec := t.RemoteSourceSpec()
	if remoteSourceSpec == nil {
		return nil, ""
	}

	fluxSourceMeta := metav1.ObjectMeta{
		Name:      t.Name,
		Namespace: t.Namespace,
		Labels: map[string]string{
			KCMManagedLabelKey: KCMManagedLabelValue,
		},
	}

	switch {
	case remoteSourceSpec.Git != nil:
		return &sourcev1.GitRepository{
			ObjectMeta: fluxSourceMeta,
			Spec:       remoteSourceSpec.Git.GitRepositorySpec,
		}, sourcev1.GitRepositoryKind
	case remoteSourceSpec.Bucket != nil:
		return &sourcev1.Bucket{
			ObjectMeta: fluxSourceMeta,
			Spec:       remoteSourceSpec.Bucket.BucketSpec,
		}, sourcev1.BucketKind
	case remoteSourceSpec.OCI != nil:
		return &sourcev1.OCIRepository{
			ObjectMeta: fluxSourceMeta,
			Spec:       remoteSourceSpec.OCI.OCIRepositorySpec,
		}, sourcev1.OCIRepositoryKind
	default:
		return nil, ""
	}
}
