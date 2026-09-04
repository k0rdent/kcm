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

package validation

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func TestToAPIServerAuthConfig(t *testing.T) {
	t.Run("nil input returns empty config", func(t *testing.T) {
		got, err := toAPIServerAuthConfig(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.JWT) != 0 {
			t.Errorf("got %+v, want empty config", got)
		}
	})

	t.Run("converts JWT issuer fields", func(t *testing.T) {
		authConf := &apiserverv1.AuthenticationConfiguration{
			JWT: []apiserverv1.JWTAuthenticator{
				{Issuer: apiserverv1.Issuer{URL: "https://issuer.example.com", Audiences: []string{"aud1"}}},
			},
		}

		got, err := toAPIServerAuthConfig(authConf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.JWT) != 1 || got.JWT[0].Issuer.URL != "https://issuer.example.com" {
			t.Errorf("got %+v", got)
		}
	})
}

func TestValidateClusterAuthentication(t *testing.T) {
	t.Run("error getting authentication configuration is wrapped", func(t *testing.T) {
		clAuth := &kcmv1.ClusterAuthentication{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
			Spec: kcmv1.ClusterAuthenticationSpec{
				AuthenticationConfiguration: &kcmv1.AuthenticationConfiguration{},
				CASecret: &kcmv1.SecretKeyReference{
					SecretReference: corev1.SecretReference{Name: "missing-secret"},
					Key:             "ca.crt",
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		err := ValidateClusterAuthentication(context.Background(), c, clAuth)
		if err == nil || !strings.Contains(err.Error(), "failed to get AuthenticationConfiguration") {
			t.Fatalf("err = %v, want get AuthenticationConfiguration error", err)
		}
	})

	t.Run("nil AuthenticationConfiguration is valid (nothing to check)", func(t *testing.T) {
		clAuth := &kcmv1.ClusterAuthentication{}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		if err := ValidateClusterAuthentication(context.Background(), c, clAuth); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid JWT authenticator (missing issuer URL) fails validation", func(t *testing.T) {
		clAuth := &kcmv1.ClusterAuthentication{
			Spec: kcmv1.ClusterAuthenticationSpec{
				AuthenticationConfiguration: &kcmv1.AuthenticationConfiguration{
					JWT: []apiserverv1.JWTAuthenticator{
						{
							Issuer: apiserverv1.Issuer{}, // missing required URL
							ClaimMappings: apiserverv1.ClaimMappings{
								Username: apiserverv1.PrefixedClaimOrExpression{Claim: "sub"},
							},
						},
					},
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		err := ValidateClusterAuthentication(context.Background(), c, clAuth)
		if err == nil || !strings.Contains(err.Error(), "invalid AuthenticationConfiguration provided") {
			t.Fatalf("err = %v, want invalid AuthenticationConfiguration error", err)
		}
	})
}

func TestClusterAuthenticationDeletionAllowed(t *testing.T) {
	clAuth := &kcmv1.ClusterAuthentication{ObjectMeta: metav1.ObjectMeta{Name: "auth1", Namespace: "ns1"}}

	testDeletionAllowedByClusterDeploymentRef(
		t, clAuth, ClusterAuthenticationDeletionAllowed,
		kcmv1.ClusterDeploymentAuthenticationIndexKey, kcmv1.ExtractClusterAuthenticationNameFromClusterDeployment,
		kcmv1.ClusterDeploymentSpec{ClusterAuth: "auth1"},
	)
}
