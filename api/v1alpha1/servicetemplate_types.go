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

// +kubebuilder:validation:XValidation:rule="has(self.helm) ? (!has(self.kustomize) && !has(self.resources)): true",message="Helm, Kustomize and Resources are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.kustomize) ? (!has(self.helm) && !has(self.resources)): true",message="Helm, Kustomize and Resources are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.resources) ? (!has(self.kustomize) && !has(self.helm)): true",message="Helm, Kustomize and Resources are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.helm) || has(self.kustomize) || has(self.resources)",message="One of Helm, Kustomize, or Resources must be specified."

// ServiceTemplateSpec defines the desired state of ServiceTemplate
type ServiceTemplateSpec struct {
	// Helm contains the Helm chart information for the template.
	// +optional
	Helm HelmSpec `json:"helm,omitempty"`

	// Kustomize contains the Kustomize configuration for the template.
	// +optional
	Kustomize *KustomizeSpec `json:"kustomize,omitempty"`

	// Resources contains the resource configuration for the template.
	// +optional
	Resources *ResourceSpec `json:"resources,omitempty"`

	// Authorization is the authorization configuration for the source. Applicable for Git repositories,
	// Helm repositories, and OCI registries.
	// +optional
	Authorization *AuthorizationSpec `json:"authorization,omitempty"`

	// Constraint describing compatible K8S versions of the cluster set in the SemVer format.
	KubernetesConstraint string `json:"k8sConstraint,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.localSourceRef) ? !has(self.remoteSourceSpec): true",message="LocalSource and RemoteSource are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.remoteSourceSpec) ? !has(self.localSourceRef): true",message="LocalSource and RemoteSource are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.localSourceRef) || has(self.remoteSourceSpec)",message="One of LocalSource or RemoteSource must be specified."

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

// +kubebuilder:validation:XValidation:rule="has(self.localSourceRef) ? !has(self.remoteSourceSpec): true",message="LocalSource and RemoteSource are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.remoteSourceSpec) ? !has(self.localSourceRef): true",message="LocalSource and RemoteSource are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.localSourceRef) || has(self.remoteSourceSpec)",message="One of LocalSource or RemoteSource must be specified."

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
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`

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
	// +optional
	CertSecretRef *corev1.LocalObjectReference `json:"certSecretRef,omitempty"`

	// ProxySecretRef specifies the Secret containing the proxy configuration
	// to use while communicating with the source.
	// +optional
	ProxySecretRef *corev1.LocalObjectReference `json:"proxySecretRef,omitempty"`

	// Insecure allows connecting to a non-TLS HTTP source endpoint.
	// This field is only supported for Bucket and OCIRepository sources.
	// +optional
	Insecure bool `json:"insecure,omitempty"`
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

// +kubebuilder:validation:XValidation:rule="has(self.git) ? (!has(self.bucket) && !has(self.oci)) : true",message="Git, Bucket and OCI are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.bucket) ? (!has(self.git) && !has(self.oci)) : true",message="Git, Bucket and OCI are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.oci) ? (!has(self.git) && !has(self.bucket)) : true",message="Git, Bucket and OCI are mutually exclusive."
// +kubebuilder:validation:XValidation:rule="has(self.git) || has(self.bucket) || has(self.oci)",message="One of Git, Bucket or OCI must be specified."
// +kubebuilder:validation:XValidation:rule="self.provider == 'github' ? has(self.git) : true",message="Github provider is only supported for Git."
// +kubebuilder:validation:XValidation:rule="self.provider == 'aws' ? !has(self.git) : true",message="Azure provider is not supported for Git."
// +kubebuilder:validation:XValidation:rule="self.provider == 'gcp' ? !has(self.git) : true",message="GCP Provider is not supported for Git."

type RemoteSourceSpec struct {
	// The provider used for authentication, can be 'aws', 'azure', 'gcp', 'github' or 'generic'.
	// When not specified, defaults to 'generic'.
	//
	// This field could be only 'generic', 'github' or 'azure' for Git.
	// This field could be 'github' only for Git.
	// +kubebuilder:validation:Enum=generic;github;aws;azure;gcp
	// +kubebuilder:default:=generic
	// +optional
	Provider string `json:"provider,omitempty"`

	// Interval at which the defined source is checked for updates.
	// This interval is approximate and may be subject to jitter to ensure
	// efficient use of resources.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$"
	// +required
	Interval metav1.Duration `json:"interval"`

	// Timeout for Git operations like cloning, defaults to 60s.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m))+$"
	// +kubebuilder:default="60s"
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Ignore overrides the set of excluded patterns in the .sourceignore format
	// (which is the same as .gitignore). If not provided, a default will be used,
	// consult the fluxcd/source-controller documentation for your version to
	// find out what those are.
	// +optional
	Ignore *string `json:"ignore,omitempty"`

	// Suspend tells the controller to suspend the reconciliation of the
	// defined source.
	// +optional
	Suspend bool `json:"suspend,omitempty"`

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
