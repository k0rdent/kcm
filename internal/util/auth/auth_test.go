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

package auth

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

func TestGetAuthenticationConfiguration(t *testing.T) {
	t.Run("nil AuthenticationConfiguration returns empty config", func(t *testing.T) {
		clAuth := &kcmv1.ClusterAuthentication{}

		got, err := GetAuthenticationConfiguration(context.Background(), fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build(), clAuth)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || len(got.JWT) != 0 {
			t.Errorf("got %+v, want empty AuthenticationConfiguration", got)
		}
	})

	t.Run("no CASecret returns config as-is", func(t *testing.T) {
		clAuth := &kcmv1.ClusterAuthentication{
			Spec: kcmv1.ClusterAuthenticationSpec{
				AuthenticationConfiguration: &kcmv1.AuthenticationConfiguration{
					JWT: []apiserverv1.JWTAuthenticator{
						{Issuer: apiserverv1.Issuer{URL: "https://issuer.example.com"}},
					},
				},
			},
		}

		got, err := GetAuthenticationConfiguration(context.Background(), fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build(), clAuth)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.JWT) != 1 || got.JWT[0].Issuer.URL != "https://issuer.example.com" {
			t.Errorf("got %+v, want JWT issuer preserved unmodified", got)
		}
		if got.JWT[0].Issuer.CertificateAuthority != "" {
			t.Errorf("got CertificateAuthority %q, want empty (no CASecret)", got.JWT[0].Issuer.CertificateAuthority)
		}
	})

	t.Run("missing CASecret returns error", func(t *testing.T) {
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

		_, err := GetAuthenticationConfiguration(context.Background(), fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build(), clAuth)
		if err == nil {
			t.Fatal("expected error for missing CA secret, got nil")
		}
		if !strings.Contains(err.Error(), "failed to get ClusterAuthentication CA secret") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("CASecret missing the configured key returns error", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ca-secret", Namespace: "ns1"},
			Data:       map[string][]byte{"other-key": []byte("irrelevant")},
		}
		clAuth := &kcmv1.ClusterAuthentication{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
			Spec: kcmv1.ClusterAuthenticationSpec{
				AuthenticationConfiguration: &kcmv1.AuthenticationConfiguration{},
				CASecret: &kcmv1.SecretKeyReference{
					SecretReference: corev1.SecretReference{Name: "ca-secret"},
					Key:             "ca.crt",
				},
			},
		}

		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(secret).Build()
		_, err := GetAuthenticationConfiguration(context.Background(), c, clAuth)
		if err == nil {
			t.Fatal("expected error for missing key in CA secret, got nil")
		}
		if !strings.Contains(err.Error(), "does not contain") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("injects CA cert into every JWT issuer, using CASecret namespace override", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ca-secret", Namespace: "other-ns"},
			Data:       map[string][]byte{"ca.crt": []byte("---CA CERT---")},
		}
		clAuth := &kcmv1.ClusterAuthentication{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
			Spec: kcmv1.ClusterAuthenticationSpec{
				AuthenticationConfiguration: &kcmv1.AuthenticationConfiguration{
					JWT: []apiserverv1.JWTAuthenticator{
						{Issuer: apiserverv1.Issuer{URL: "https://a.example.com"}},
						{Issuer: apiserverv1.Issuer{URL: "https://b.example.com"}},
					},
				},
				CASecret: &kcmv1.SecretKeyReference{
					SecretReference: corev1.SecretReference{Name: "ca-secret", Namespace: "other-ns"},
					Key:             "ca.crt",
				},
			},
		}

		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(secret).Build()
		got, err := GetAuthenticationConfiguration(context.Background(), c, clAuth)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for i, jwt := range got.JWT {
			if jwt.Issuer.CertificateAuthority != "---CA CERT---" {
				t.Errorf("JWT[%d].Issuer.CertificateAuthority = %q, want %q", i, jwt.Issuer.CertificateAuthority, "---CA CERT---")
			}
		}
	})

	t.Run("empty CA cert value leaves JWT issuers untouched", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ca-secret", Namespace: "ns1"},
			Data:       map[string][]byte{"ca.crt": {}},
		}
		clAuth := &kcmv1.ClusterAuthentication{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
			Spec: kcmv1.ClusterAuthenticationSpec{
				AuthenticationConfiguration: &kcmv1.AuthenticationConfiguration{
					JWT: []apiserverv1.JWTAuthenticator{
						{Issuer: apiserverv1.Issuer{URL: "https://a.example.com"}},
					},
				},
				CASecret: &kcmv1.SecretKeyReference{
					SecretReference: corev1.SecretReference{Name: "ca-secret"},
					Key:             "ca.crt",
				},
			},
		}

		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(secret).Build()
		got, err := GetAuthenticationConfiguration(context.Background(), c, clAuth)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.JWT[0].Issuer.CertificateAuthority != "" {
			t.Errorf("CertificateAuthority = %q, want empty", got.JWT[0].Issuer.CertificateAuthority)
		}
	})
}
