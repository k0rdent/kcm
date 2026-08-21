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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

const (
	ClusterAuthenticationKind = "ClusterAuthentication"

	authConfigAPIVersion = "apiserver.config.k8s.io/v1"
	authConfigKind       = "AuthenticationConfiguration"
)

// +kubebuilder:validation:MinProperties=0

// ClusterAuthenticationSpec defines the desired state of ClusterAuthentication
type ClusterAuthenticationSpec struct {
	// +optional

	// authenticationConfiguration contains the full content of an [AuthenticationConfiguration] object,
	// which defines how the API server should perform request authentication.
	//
	// For more details, see: https://kubernetes.io/docs/reference/access-authn-authz/authentication/#using-authentication-configuration
	AuthenticationConfiguration AuthenticationConfiguration `json:"authenticationConfiguration,omitempty,omitzero"`
	// +optional

	// caSecret is the reference to the secret containing the CA certificates used to validate the connection
	// to the issuers endpoints.
	CASecret SecretKeyReference `json:"caSecret,omitzero"`
}

// +kubebuilder:pruning:PreserveUnknownFields

// AuthenticationConfiguration defines the structure of the kubernetes AuthenticationConfiguration object
// used to configure API server authentication.
//
// This type is derived from the upstream Kubernetes implementation of [k8s.io/apiserver/pkg/apis/apiserver/v1.AuthenticationConfiguration]
type AuthenticationConfiguration struct { //nolint:govet
	// +listType=atomic
	// +required
	// +kubebuilder:validation:MinItems=0

	// jwt is a list of authenticator to authenticate Kubernetes users using
	// JWT compliant tokens. The authenticator will attempt to parse a raw ID token,
	// verify it's been signed by the configured issuer. The public key to verify the
	// signature is discovered from the issuer's public endpoint using OIDC discovery.
	// For an incoming token, each JWT authenticator will be attempted in
	// the order in which it is specified in this list.  Note however that
	// other authenticators may run before or after the JWT authenticators.
	// The specific position of JWT authenticators in relation to other
	// authenticators is neither defined nor stable across releases.  Since
	// each JWT authenticator must have a unique issuer URL, at most one
	// JWT authenticator will attempt to cryptographically validate the token.
	//
	// The minimum valid JWT payload must contain the following claims:
	// {
	//		"iss": "https://issuer.example.com",
	//		"aud": ["audience"],
	//		"exp": 1234567890,
	//		"<username claim>": "username"
	// }
	JWT []apiserverv1.JWTAuthenticator `json:"jwt,omitempty"`
	// +optional

	// anonymous configures anonymous authentication; when present, --anonymous-auth must not be set
	Anonymous *apiserverv1.AnonymousAuthConfig `json:"anonymous,omitempty"`
}

func (s *ClusterAuthenticationSpec) GetAuthConfig() *apiserverv1.AuthenticationConfiguration {
	if !s.HasAuthenticationConfiguration() {
		return &apiserverv1.AuthenticationConfiguration{}
	}
	return &apiserverv1.AuthenticationConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: authConfigAPIVersion,
			Kind:       authConfigKind,
		},
		JWT:       s.AuthenticationConfiguration.JWT,
		Anonymous: s.AuthenticationConfiguration.Anonymous,
	}
}

func (s *ClusterAuthenticationSpec) HasAuthenticationConfiguration() bool {
	return s != nil && (s.AuthenticationConfiguration.JWT != nil || s.AuthenticationConfiguration.Anonymous != nil)
}

func (s *ClusterAuthenticationSpec) HasCASecret() bool {
	return s != nil && s.CASecret.Key != ""
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=clauth

// ClusterAuthentication is the Schema for the cluster authentication configuration API
type ClusterAuthentication struct { //nolint:govet // false-positive
	metav1.TypeMeta `json:",inline"`
	// +optional

	// metadata contains the object metadata
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional

	// spec defines the desired state
	Spec ClusterAuthenticationSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterAuthenticationList contains a list of ClusterAuthentication
type ClusterAuthenticationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterAuthentication `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterAuthentication{}, &ClusterAuthenticationList{})
}
