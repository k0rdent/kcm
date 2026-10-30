// Copyright 2026
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
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterCertificateAuthorityKind is the string representation of a ClusterCertificateAuthority.
const ClusterCertificateAuthorityKind = "ClusterCertificateAuthority"

// CertificateAuthorityRef specifies the source of a single CA keypair: either a pre-existing
// Secret to copy as-is, or a cert-manager Issuer/ClusterIssuer used to mint one. Either way, the
// resulting Secret kcm materializes for it is a standard kubernetes.io/tls Secret (tls.crt/tls.key
// keys), matching what k0smotron's certificateRefs expects.
// Exactly one of secretRef or issuerRef must be set.
// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:XValidation:rule="(has(self.secretRef) ? 1 : 0) + (has(self.issuerRef) ? 1 : 0) == 1",message="exactly one of secretRef or issuerRef must be set"
type CertificateAuthorityRef struct {
	// +optional

	// secretRef references an existing Secret, in the same namespace as the
	// ClusterCertificateAuthority, holding the CA keypair as tls.crt/tls.key. It is copied,
	// unmodified, into each referencing cluster's namespace under the name k0smotron requires.
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`
	// +optional

	// issuerRef references a cert-manager Issuer or ClusterIssuer used to mint this CA as a
	// cert-manager Certificate with isCA: true. The resulting Secret is reconciled directly into
	// each referencing cluster's namespace, and re-synced as cert-manager renews it.
	IssuerRef *cmmeta.IssuerReference `json:"issuerRef,omitempty"`
}

// IsZero reports whether ref has neither secretRef nor issuerRef set, i.e. this role is not
// configured.
func (r *CertificateAuthorityRef) IsZero() bool {
	return r.SecretRef == nil && r.IssuerRef == nil
}

// CertificateAuthorityRotation configures rotation behavior for CAs minted via issuerRef.
// cert-manager renews and re-syncs those Secrets on its own regardless of this field; it only
// overrides when that renewal is requested. It has no effect on CAs sourced via secretRef — those
// rotate only when the admin updates the referenced Secret.
// +kubebuilder:validation:MinProperties=1
type CertificateAuthorityRotation struct {
	// +optional
	// +kubebuilder:validation:Minimum=0

	// renewBeforeSeconds overrides the cert-manager Certificate's renewBefore, in seconds. If unset,
	// cert-manager's own default (1/3 of duration) applies.
	RenewBeforeSeconds *int32 `json:"renewBeforeSeconds,omitempty"`
}

// ClusterCertificateAuthoritySpec defines the desired state of ClusterCertificateAuthority.
//
// Each populated field corresponds to one of the certificateRefs entries k0smotron's
// K0smotronControlPlane expects to find pre-created in a cluster's namespace before the control
// plane is provisioned, following the same convention CABPK uses for custom certificates:
// https://cluster-api.sigs.k8s.io/tasks/certs/using-custom-certificates.
// +kubebuilder:validation:MinProperties=1
type ClusterCertificateAuthoritySpec struct {
	// +optional

	// ca is the Kubernetes API server cluster CA (k0smotron certificateRefs type "ca").
	CA CertificateAuthorityRef `json:"ca,omitempty,omitzero"`
	// +optional

	// etcd is the etcd CA (k0smotron certificateRefs type "etcd").
	Etcd CertificateAuthorityRef `json:"etcd,omitempty,omitzero"`
	// +optional

	// frontProxy is the front-proxy CA (k0smotron certificateRefs type "proxy").
	FrontProxy CertificateAuthorityRef `json:"frontProxy,omitempty,omitzero"`
	// +optional

	// serviceAccount is the service account signing keypair (k0smotron certificateRefs type "sa").
	// Only secretRef is meaningful here in practice — cert-manager issuers mint certificates, not
	// bare signing keypairs — but the type is shared with the other refs for consistency.
	ServiceAccount CertificateAuthorityRef `json:"serviceAccount,omitempty,omitzero"`
	// +optional

	// apiServerEtcdClient is the client certificate the API server uses to talk to etcd
	// (k0smotron certificateRefs type "apiserver-etcd-client").
	APIServerEtcdClient CertificateAuthorityRef `json:"apiServerEtcdClient,omitempty,omitzero"`
	// +optional

	// rotation configures rotation behavior for any of the above refs sourced via issuerRef.
	Rotation CertificateAuthorityRotation `json:"rotation,omitempty,omitzero"`
}

// CertificateAuthorityRole pairs one of the spec's named refs with the string k0smotron's
// certificateRefs expects in its "type" field.
type CertificateAuthorityRole struct {
	// +optional

	// ref is the resolved CertificateAuthorityRef for this role.
	Ref CertificateAuthorityRef `json:"ref,omitempty,omitzero"`
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256

	// k0smotronType is the value to use for this ref's entry in K0smotronControlPlane's
	// spec.certificateRefs[].type.
	K0smotronType string `json:"k0smotronType,omitempty"`
}

// Roles returns the spec's configured refs paired with their k0smotron certificateRefs type,
// omitting any that are unset. Order is stable across calls.
func (s *ClusterCertificateAuthoritySpec) Roles() []CertificateAuthorityRole {
	all := []CertificateAuthorityRole{
		{K0smotronType: "ca", Ref: s.CA},
		{K0smotronType: "etcd", Ref: s.Etcd},
		{K0smotronType: "proxy", Ref: s.FrontProxy},
		{K0smotronType: "sa", Ref: s.ServiceAccount},
		{K0smotronType: "apiserver-etcd-client", Ref: s.APIServerEtcdClient},
	}

	roles := make([]CertificateAuthorityRole, 0, len(all))
	for _, r := range all {
		if !r.Ref.IsZero() {
			roles = append(roles, r)
		}
	}
	return roles
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=clusterca

// ClusterCertificateAuthority is the Schema for the cluster certificate authority configuration API
type ClusterCertificateAuthority struct { //nolint:govet
	metav1.TypeMeta `json:",inline"`
	// +optional

	// metadata contains the object metadata
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +required

	// spec defines the desired state
	Spec ClusterCertificateAuthoritySpec `json:"spec,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ClusterCertificateAuthorityList contains a list of ClusterCertificateAuthority
type ClusterCertificateAuthorityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterCertificateAuthority `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterCertificateAuthority{}, &ClusterCertificateAuthorityList{})
}
