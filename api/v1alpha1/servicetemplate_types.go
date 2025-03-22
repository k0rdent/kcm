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
	fluxmetav1 "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Denotes the servicetemplate resource Kind.
	ServiceTemplateKind = "ServiceTemplate"
	// ChartAnnotationKubernetesConstraint is an annotation containing the Kubernetes constrained version in the SemVer format associated with a ServiceTemplate.
	ChartAnnotationKubernetesConstraint = "k0rdent.mirantis.com/k8s-version-constraint"
)

// +kubebuilder:validation:XValidation:rule="has(self.helm) ? (!has(self.kustomize) && !has(self.resources)): true",message="Helm, Kustomize and Resources are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.kustomize) ? (!has(self.helm) && !has(self.resources)): true",message="Helm, Kustomize and Resources are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.resources) ? (!has(self.kustomize) && !has(self.helm)): true",message="Helm, Kustomize and Resources are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.helm) || has(self.kustomize) || has(self.resources)",message="One of Helm, Kustomize, or Resources must be specified."

// ServiceTemplateSpec defines the desired state of ServiceTemplate
type ServiceTemplateSpec struct {
	// Helm contains the Helm chart information for the template.
	Helm *HelmSpec `json:"helm,omitempty"`

	// Kustomize contains the Kustomize configuration for the template.
	Kustomize *SourceSpec `json:"kustomize,omitempty"`

	// Resources contains the resource configuration for the template.
	Resources *SourceSpec `json:"resources,omitempty"`

	// Authorization is the authorization configuration for the source. Applicable for Git repositories,
	// Helm repositories, and OCI registries.
	Authorization *AuthorizationSpec `json:"authorization,omitempty"`

	// Constraint describing compatible K8S versions of the cluster set in the SemVer format.
	KubernetesConstraint string `json:"k8sConstraint,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.localSourceRef) ? !has(self.remoteSourceSpec): true",message="LocalSource and RemoteSource are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.remoteSourceSpec) ? !has(self.localSourceRef): true",message="LocalSource and RemoteSource are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.localSourceRef) || has(self.remoteSourceSpec)",message="One of LocalSource or RemoteSource must be specified."

// SourceSpec defines the desired state of the source.
type SourceSpec struct {
	// +required

	// Path to the directory containing the resource manifest.
	Path string `json:"path"`

	// +kubebuilder:validation:Enum=Local;Remote
	// +kubebuilder:default=Remote
	// +required

	// DeploymentType is the type of the deployment.
	DeploymentType string `json:"deploymentType"`

	// LocalSourceRef is the local source of the kustomize manifest.
	LocalSourceRef *LocalSourceRef `json:"localSourceRef,omitempty"`

	// RemoteSourceSpec is the remote source of the kustomize manifest.
	RemoteSourceSpec *RemoteSourceSpec `json:"remoteSourceSpec,omitempty"`
}

// AuthorizationSpec defines the authorization configuration for the source.
type AuthorizationSpec struct {
	// SecretRef specifies the Secret containing authentication credentials for
	// the source.
	//
	// For Git repositories:
	// For HTTPS repositories the Secret must contain 'username' and 'password'
	// fields for basic auth or 'bearerToken' field for token auth.
	// For SSH repositories the Secret must contain 'identity'
	// and 'known_hosts' fields.
	//
	// For Buckets:
	// specifies the Secret containing authentication credentials for the Bucket.
	//
	// For OCI repositories:
	// secret must contain the registry login credentials to resolve image metadata.
	// The secret must be of type kubernetes.io/dockerconfigjson.
	SecretRef *fluxmetav1.LocalObjectReference `json:"secretRef,omitempty"`

	// CertSecretRef can be given the name of a Secret containing
	// either or both of
	//
	// - a PEM-encoded client certificate (`tls.crt`) and private
	// key (`tls.key`);
	// - a PEM-encoded CA certificate (`ca.crt`)
	//
	// and whichever are supplied, will be used for connecting to the
	// bucket. The client cert and key are useful if you are
	// authenticating with a certificate; the CA cert is useful if
	// you are using a self-signed server certificate. The Secret must
	// be of type `Opaque` or `kubernetes.io/tls`.
	//
	// This field is only supported for Bucket and OCIRepository sources.
	// For Bucket this field is only supported for the `generic` provider.
	CertSecretRef *fluxmetav1.LocalObjectReference `json:"certSecretRef,omitempty"`

	// ProxySecretRef specifies the Secret containing the proxy configuration
	// to use while communicating with the source.
	ProxySecretRef *fluxmetav1.LocalObjectReference `json:"proxySecretRef,omitempty"`

	// Insecure allows connecting to a non-TLS HTTP source endpoint.
	// This field is only supported for Bucket and OCIRepository sources.
	Insecure bool `json:"insecure,omitempty"`
}

// LocalSourceRef defines the reference to the local resource to be used as the source.
type LocalSourceRef struct {
	// +kubebuilder:validation:Enum=ConfigMap;Secret;GitRepository;Bucket;OCIRepository

	// Kind is the kind of the local source.
	Kind string `json:"kind"`

	// Name is the name of the local source.
	Name string `json:"name"`

	// Namespace is the namespace of the local source.
	Namespace string `json:"namespace"`
}

// +kubebuilder:validation:XValidation:rule="has(self.git) ? (!has(self.bucket) && !has(self.oci)) : true",message="Git, Bucket and OCI are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.bucket) ? (!has(self.git) && !has(self.oci)) : true",message="Git, Bucket and OCI are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.oci) ? (!has(self.git) && !has(self.bucket)) : true",message="Git, Bucket and OCI are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.git) || has(self.bucket) || has(self.oci)",message="One of Git, Bucket or OCI must be specified."
// +kubebuilder:validation:XValidation:rule="self.provider == 'github' ? has(self.git) : true",message="Github provider is only supported for Git."
// +kubebuilder:validation:XValidation:rule="self.provider == 'aws' ? !has(self.git) : true",message="AWS provider is not supported for Git."
// +kubebuilder:validation:XValidation:rule="self.provider == 'gcp' ? !has(self.git) : true",message="GCP Provider is not supported for Git."

// RemoteSourceSpec defines the desired state of the remote source (Git, Bucket, OCI).
type RemoteSourceSpec struct {
	// +kubebuilder:validation:Enum=generic;github;aws;azure;gcp
	// +kubebuilder:default:=generic

	// The provider used for authentication, can be 'aws', 'azure', 'gcp', 'github' or 'generic'.
	// When not specified, defaults to 'generic'.
	//
	// This field could be only 'generic', 'github' or 'azure' for Git.
	// This field could be 'github' only for Git.
	Provider string `json:"provider,omitempty"`

	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$"
	// +kubebuilder:default="5m"

	// Interval at which the defined source is checked for updates.
	// This interval is approximate and may be subject to jitter to ensure
	// efficient use of resources.
	Interval metav1.Duration `json:"interval"`

	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m))+$"
	// +kubebuilder:default="60s"

	// Timeout for Git operations like cloning, defaults to 60s.
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Ignore overrides the set of excluded patterns in the .sourceignore format
	// (which is the same as .gitignore). If not provided, a default will be used,
	// consult the fluxcd/source-controller documentation for your version to
	// find out what those are.
	Ignore *string `json:"ignore,omitempty"`

	// Suspend tells the controller to suspend the reconciliation of the
	// defined source.
	Suspend bool `json:"suspend,omitempty"`

	// Git is the definition of git repository source.
	Git *EmbeddedGitRepositorySpec `json:"git,omitempty"`

	// Bucket is the definition of bucket source.
	Bucket *EmbeddedBucketSpec `json:"bucket,omitempty"`

	// OCI is the definition of OCI repository source.
	OCI *EmbeddedOCIRepositorySpec `json:"oci,omitempty"`
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
	Artifact *sourcev1.Artifact `json:"artifact,omitempty"`

	// ObservedGeneration is the latest generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions reflects the conditions of the remote source object.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
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

func (t *ServiceTemplate) LocalSourceRef() *LocalSourceRef {
	switch {
	case t.Spec.Kustomize != nil:
		return t.Spec.Kustomize.LocalSourceRef
	case t.Spec.Resources != nil:
		return t.Spec.Resources.LocalSourceRef
	default:
		return nil
	}
}

func (t *ServiceTemplate) RemoteSourceSpec() *RemoteSourceSpec {
	switch {
	case t.Spec.Kustomize != nil:
		return t.Spec.Kustomize.RemoteSourceSpec
	case t.Spec.Resources != nil:
		return t.Spec.Resources.RemoteSourceSpec
	default:
		return nil
	}
}
