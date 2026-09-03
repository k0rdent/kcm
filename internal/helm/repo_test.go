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

package helm

import (
	"context"
	"testing"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func TestHelmRepositorySpec(t *testing.T) {
	t.Run("no secret refs set", func(t *testing.T) {
		cfg := &DefaultRegistryConfig{RepoType: "oci", URL: "oci://example.com/charts"}

		spec := cfg.HelmRepositorySpec()
		if spec.Type != "oci" || spec.URL != "oci://example.com/charts" {
			t.Errorf("spec = %+v", spec)
		}
		if spec.SecretRef != nil {
			t.Errorf("SecretRef = %+v, want nil", spec.SecretRef)
		}
		if spec.CertSecretRef != nil {
			t.Errorf("CertSecretRef = %+v, want nil", spec.CertSecretRef)
		}
		if spec.Interval.Duration != DefaultReconcileInterval {
			t.Errorf("Interval = %v, want %v", spec.Interval.Duration, DefaultReconcileInterval)
		}
	})

	t.Run("secret refs set when configured", func(t *testing.T) {
		cfg := &DefaultRegistryConfig{
			RepoType:              "default",
			URL:                   "https://example.com/charts",
			CredentialsSecretName: "creds-secret",
			CertSecretName:        "cert-secret",
			Insecure:              true,
		}

		spec := cfg.HelmRepositorySpec()
		if spec.SecretRef == nil || spec.SecretRef.Name != "creds-secret" {
			t.Errorf("SecretRef = %+v, want creds-secret", spec.SecretRef)
		}
		if spec.CertSecretRef == nil || spec.CertSecretRef.Name != "cert-secret" {
			t.Errorf("CertSecretRef = %+v, want cert-secret", spec.CertSecretRef)
		}
		if !spec.Insecure {
			t.Error("Insecure = false, want true")
		}
	})
}

func TestReconcileHelmRepository(t *testing.T) {
	spec := sourcev1.HelmRepositorySpec{Type: "oci", URL: "oci://example.com/charts"}

	t.Run("creates a new HelmRepository", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		if err := ReconcileHelmRepository(context.Background(), c, "repo1", "ns1", spec); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := &sourcev1.HelmRepository{}
		if err := c.Get(context.Background(), client.ObjectKey{Name: "repo1", Namespace: "ns1"}, got); err != nil {
			t.Fatalf("expected HelmRepository to exist: %v", err)
		}
		if got.Spec.URL != spec.URL {
			t.Errorf("Spec.URL = %q, want %q", got.Spec.URL, spec.URL)
		}
		if got.Labels[kcmv1.KCMManagedLabelKey] != kcmv1.KCMManagedLabelValue {
			t.Errorf("managed label not set: %+v", got.Labels)
		}
	})

	t.Run("updates an existing HelmRepository", func(t *testing.T) {
		existing := &sourcev1.HelmRepository{
			ObjectMeta: metav1.ObjectMeta{Name: "repo2", Namespace: "ns1"},
			Spec:       sourcev1.HelmRepositorySpec{Type: "default", URL: "https://old.example.com"},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(existing).Build()

		if err := ReconcileHelmRepository(context.Background(), c, "repo2", "ns1", spec); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := &sourcev1.HelmRepository{}
		if err := c.Get(context.Background(), client.ObjectKey{Name: "repo2", Namespace: "ns1"}, got); err != nil {
			t.Fatalf("expected HelmRepository to exist: %v", err)
		}
		if got.Spec.URL != spec.URL {
			t.Errorf("Spec.URL = %q, want %q (should have been updated)", got.Spec.URL, spec.URL)
		}
	})
}
