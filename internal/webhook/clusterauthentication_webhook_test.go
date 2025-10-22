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

package webhook

import (
	"encoding/base64"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apiserverv1beta1 "k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/authentication"
	"github.com/K0rdent/kcm/test/objects/clusterauthentication"
	"github.com/K0rdent/kcm/test/objects/clusterdeployment"
	"github.com/K0rdent/kcm/test/scheme"
)

const (
	namespace    = "test"
	caSecretName = "test-ca"
)

var (
	caCert = `
-----BEGIN CERTIFICATE-----
MIICoDCCAYgCCQDs06U1hPyxzjANBgkqhkiG9w0BAQsFADASMRAwDgYDVQQDDAdr
dWJlLWNhMB4XDTI1MTAxNTA5MjIzOVoXDTI1MTAyNTA5MjIzOVowEjEQMA4GA1UE
AwwHa3ViZS1jYTCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBAL6VfEG0
ZxIGqf/PwWGuqo99cf63/Q/l6tOeB3SwCrgG8Ar6uLQzT45BQFs3arH3WEZWfWwM
RODFIHwxAAbS0P0wVgBzcK9i7eJrJuWhkZtBxBCnZPjjVFBwKyfuNyd9Md3IvagY
ca1W2nDZrD/KVo6bgSP1t8X0ohoqiPOJlY6wPZxhLL9vSlarB3GaHEPWEEWFEF4c
EDTmJ6J7ON4XQJQj/zPK956CU8B8Y/icuThDcKGvXWgYoCO85QnlJAKPgecxMFtC
UPumx/5ZHyNbM75xNwMViKhFMihltgw0vxoYEnYgfUgYW547e9jsqItKnWD71FYM
oF1W4EkeBIlW4CMCAwEAATANBgkqhkiG9w0BAQsFAAOCAQEAIDcGoRCWTrMimPCG
pWJGENMfG/chSwZh3IMA8pck7EmlAPLeFENXfgjaY0/UHz0O/ehy9lg1qP7U4LMq
UMOmvsZf0dEfm4Kh7kxeb7q9lPSptrNzsQNXIoV9CNsEphSJAwkoD2Xl/hmlLZVa
rsWvBgQLxufUp1tmEjcifbr6GmFNAZhvuV/sUTVewOwtHVkVDWzk6gUYbeSGgZ3S
Ks1LPYA4v56Ii+Ba9ruz6UOW9jBOfQnJrXro/7YXL6y+8QPm0kaXSv7rypFvAHNk
rrguRgz44tzfrJeqhLLJJJnRN3YydhdLZRHcTuzGmzSV3q2M6fqlX0mYsZuIxisg
OybpVQ==
-----END CERTIFICATE-----
`
	validAuthConfig = &kcmv1.AuthenticationConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apiserver.config.k8s.io/v1beta1",
			Kind:       "AuthenticationConfiguration",
		},
		JWT: []apiserverv1beta1.JWTAuthenticator{
			{
				Issuer: apiserverv1beta1.Issuer{
					URL:       "https://dex.example.com:5556",
					Audiences: []string{"example-app"},
				},
				ClaimMappings: apiserverv1beta1.ClaimMappings{
					Username: apiserverv1beta1.PrefixedClaimOrExpression{
						Claim:  "email",
						Prefix: ptr.To(""),
					},
					Groups: apiserverv1beta1.PrefixedClaimOrExpression{
						Claim:  "groups",
						Prefix: ptr.To(""),
					},
				},
				UserValidationRules: []apiserverv1beta1.UserValidationRule{
					{
						Expression: "!user.username.startsWith('system:')",
						Message:    "username cannot use reserved system: prefix",
					},
				},
			},
		},
	}

	invalidAuthConfig = &kcmv1.AuthenticationConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apiserver.config.k8s.io/v1beta1",
			Kind:       "AuthenticationConfiguration",
		},
		JWT: []apiserverv1beta1.JWTAuthenticator{
			{
				Issuer: apiserverv1beta1.Issuer{
					URL:       "invalidURL",
					Audiences: []string{"example-app"},
				},
				ClaimMappings: apiserverv1beta1.ClaimMappings{
					Username: apiserverv1beta1.PrefixedClaimOrExpression{
						Claim:  "email",
						Prefix: ptr.To(""),
					},
					Groups: apiserverv1beta1.PrefixedClaimOrExpression{
						Claim:  "groups",
						Prefix: ptr.To(""),
					},
				},
				UserValidationRules: []apiserverv1beta1.UserValidationRule{
					{
						Expression: "!user.username.startsWith('system:')",
						Message:    "username cannot use reserved system: prefix",
					},
				},
			},
		},
	}

	caSecret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      caSecretName,
		},
		Data: map[string][]byte{
			authentication.CACertificateSecretKey: []byte(caCert),
		},
	}

	invalidCASecret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      caSecretName,
		},
		Data: map[string][]byte{
			"wrong-key": []byte(base64.StdEncoding.EncodeToString([]byte(caCert))),
		},
	}
)

func TestClusterAuthenticationValidateCreate(t *testing.T) {
	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
		},
	})

	tests := []struct {
		name            string
		clAuth          *kcmv1.ClusterAuthentication
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name: "should fail if the AuthenticationConfiguration is invalid",
			clAuth: clusterauthentication.New(
				clusterauthentication.WithNamespace(namespace),
				clusterauthentication.WithAuthenticationConfiguration(invalidAuthConfig),
			),
			err: "the ClusterAuthentication is invalid: invalid AuthenticationConfiguration provided: jwt[0].issuer.url: Invalid value: \"invalidURL\": URL scheme must be https",
		},
		{
			name: "should fail if the CA certificate secret does not exist",
			clAuth: clusterauthentication.New(
				clusterauthentication.WithNamespace(namespace),
				clusterauthentication.WithAuthenticationConfiguration(validAuthConfig),
				clusterauthentication.WithCASecretRef(kcmv1.CASecretReference{Name: caSecretName})),
			existingObjects: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "another-namespace",
						Name:      caSecretName,
					},
					Data: map[string][]byte{
						authentication.CACertificateSecretKey: []byte(base64.StdEncoding.EncodeToString([]byte(caCert))),
					},
				},
			},
			err: fmt.Sprintf("the ClusterAuthentication is invalid: failed to get ClusterAuthentication CA secret %s/%s: secrets %q not found", namespace, caSecretName, caSecretName),
		},
		{
			name: "should fail if the CA certificate secret is invalid",
			clAuth: clusterauthentication.New(
				clusterauthentication.WithNamespace(namespace),
				clusterauthentication.WithAuthenticationConfiguration(validAuthConfig),
				clusterauthentication.WithCASecretRef(kcmv1.CASecretReference{Name: caSecretName})),
			existingObjects: []runtime.Object{invalidCASecret},
			err:             fmt.Sprintf("secret %s/%s does not contain %s key", namespace, caSecretName, authentication.CACertificateSecretKey),
		},
		{
			name: "should succeed",
			clAuth: clusterauthentication.New(
				clusterauthentication.WithNamespace(namespace),
				clusterauthentication.WithAuthenticationConfiguration(validAuthConfig),
				clusterauthentication.WithCASecretRef(kcmv1.CASecretReference{Name: caSecretName})),
			existingObjects: []runtime.Object{caSecret},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				Build()
			validator := &ClusterAuthenticationValidator{Client: c}
			warn, err := validator.ValidateCreate(ctx, tt.clAuth)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}

			g.Expect(warn).To(Equal(tt.warnings))
		})
	}
}

func TestClusterAuthenticationValidateUpdate(t *testing.T) {
	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
		},
	})

	const (
		namespace    = "test"
		caSecretName = "test-ca"
	)

	tests := []struct {
		name                      string
		newClAuth                 *kcmv1.ClusterAuthentication
		existingObjects           []runtime.Object
		skipUpgradePathValidation bool
		err                       string
		warnings                  admission.Warnings
	}{
		{
			name: "should fail if the AuthenticationConfiguration is invalid",
			newClAuth: clusterauthentication.New(
				clusterauthentication.WithNamespace(namespace),
				clusterauthentication.WithAuthenticationConfiguration(invalidAuthConfig),
			),
			err: "the ClusterAuthentication is invalid: invalid AuthenticationConfiguration provided: jwt[0].issuer.url: Invalid value: \"invalidURL\": URL scheme must be https",
		},
		{
			name: "should fail if the CA certificate secret does not exist",
			newClAuth: clusterauthentication.New(
				clusterauthentication.WithNamespace(namespace),
				clusterauthentication.WithAuthenticationConfiguration(validAuthConfig),
				clusterauthentication.WithCASecretRef(kcmv1.CASecretReference{Name: caSecretName})),
			existingObjects: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "another-namespace",
						Name:      caSecretName,
					},
					Data: map[string][]byte{
						authentication.CACertificateSecretKey: []byte(base64.StdEncoding.EncodeToString([]byte(caCert))),
					},
				},
			},
			err: fmt.Sprintf("the ClusterAuthentication is invalid: failed to get ClusterAuthentication CA secret %s/%s: secrets %q not found", namespace, caSecretName, caSecretName),
		},
		{
			name: "should fail if the CA certificate secret is invalid",
			newClAuth: clusterauthentication.New(
				clusterauthentication.WithNamespace(namespace),
				clusterauthentication.WithAuthenticationConfiguration(validAuthConfig),
				clusterauthentication.WithCASecretRef(kcmv1.CASecretReference{Name: caSecretName})),
			existingObjects: []runtime.Object{invalidCASecret},
			err:             fmt.Sprintf("secret %s/%s does not contain %s key", namespace, caSecretName, authentication.CACertificateSecretKey),
		},
		{
			name: "should succeed",
			newClAuth: clusterauthentication.New(
				clusterauthentication.WithNamespace(namespace),
				clusterauthentication.WithAuthenticationConfiguration(validAuthConfig),
				clusterauthentication.WithCASecretRef(kcmv1.CASecretReference{Name: caSecretName})),
			existingObjects: []runtime.Object{caSecret},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				Build()
			validator := &ClusterAuthenticationValidator{Client: c}
			warn, err := validator.ValidateUpdate(ctx, nil, tt.newClAuth)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}

			g.Expect(warn).To(Equal(tt.warnings))
		})
	}
}

func TestClusterAuthenticationDelete(t *testing.T) {
	g := NewWithT(t)

	ctx := t.Context()

	const (
		namespace  = "test-ns"
		clAuthName = "test-cl-auth"
	)

	tests := []struct {
		name            string
		clAuth          *kcmv1.ClusterAuthentication
		existingObjects []runtime.Object
		err             string
	}{
		{
			name: "deletion is not allowed, ClusterAuthentication is referenced in the ClusterDeployment",
			clAuth: clusterauthentication.New(
				clusterauthentication.WithNamespace(namespace),
				clusterauthentication.WithName(clAuthName),
			),
			existingObjects: []runtime.Object{
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithNamespace(namespace),
					clusterdeployment.WithClusterAuthentication(clAuthName),
				),
			},
			err: fmt.Sprintf("cannot delete ClusterAuthentication %s/%s: it is still referenced by one or more ClusterDeployments", namespace, clAuthName),
		},
		{
			name: "deletion is allowed",
			clAuth: clusterauthentication.New(
				clusterauthentication.WithNamespace(namespace),
				clusterauthentication.WithName(clAuthName),
			),
			existingObjects: []runtime.Object{
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithNamespace("another-namespace"),
					clusterdeployment.WithClusterAuthentication(clAuthName),
				),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentAuthenticationIndexKey, kcmv1.ExtractClusterAuthenticationNameFromClusterDeployment).
				Build()
			validator := &ClusterAuthenticationValidator{Client: c}
			_, err := validator.ValidateDelete(ctx, tt.clAuth)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				if err.Error() != tt.err {
					t.Fatalf("expected error '%s', got error: %s", tt.err, err.Error())
				}
			} else {
				g.Expect(err).To(Succeed())
			}
		})
	}
}
