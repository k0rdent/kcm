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
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/apis/apiserver"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

const ClusterAuthenticationKind = "ClusterAuthentication"

// ClusterAuthenticationSpec defines the desired state of ClusterAuthentication
type ClusterAuthenticationSpec struct {
	// AuthenticationConfiguration contains the full content of an [AuthenticationConfiguration] object,
	// which defines how the API server should perform request authentication.
	//
	// For more details, see: https://kubernetes.io/docs/reference/access-authn-authz/authentication/#using-authentication-configuration
	AuthenticationConfiguration *AuthenticationConfiguration `json:"authenticationConfiguration,omitempty"`
	// CASecret is the reference to the secret containing the CA certificates used to validate the connection
	// to the issuers endpoints.
	CASecret *SecretKeyReference `json:"caSecret,omitempty"`
}

// AuthenticationConfiguration defines the structure of the kubernetes AuthenticationConfiguration object
// used to configure API server authentication.
//
// This type is derived from the upstream Kubernetes implementation of [k8s.io/apiserver/pkg/apis/apiserver/v1.AuthenticationConfiguration],
// with a modified JSON tag on the TypeMeta field.
type AuthenticationConfiguration struct { //nolint:govet
	metav1.TypeMeta `json:",inline"`

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
	JWT []apiserverv1.JWTAuthenticator `json:"jwt"`

	// If present --anonymous-auth must not be set
	Anonymous *apiserverv1.AnonymousAuthConfig `json:"anonymous,omitempty"`
}

// ToAPIServerAuthConfig converts the AuthenticationConfiguration object to the original struct from the
// apiserver package for further validation
func (c *AuthenticationConfiguration) ToAPIServerAuthConfig() (*apiserver.AuthenticationConfiguration, error) {
	if c == nil {
		return &apiserver.AuthenticationConfiguration{}, nil
	}

	outBytes, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("error marshaling auth config to JSON: %w", err)
	}

	apiserverAuthConfig := &apiserver.AuthenticationConfiguration{}
	if err := json.Unmarshal(outBytes, apiserverAuthConfig); err != nil {
		return nil, fmt.Errorf("error unmarshalling auth config JSON to apiserver auth config: %w", err)
	}

	return apiserverAuthConfig, nil
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=clauth

// ClusterAuthentication is the Schema for the cluster authentication configuration API
type ClusterAuthentication struct { //nolint:govet // false-positive
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

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
