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

	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func TestRegionClusterReference(t *testing.T) {
	t.Run("no kubeConfig or clusterDeployment set: no-op", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		if err := RegionClusterReference(context.Background(), c, "kcm-system", &kcmv1.Region{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("kubeConfig Secret missing: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		rgn := &kcmv1.Region{Spec: kcmv1.RegionSpec{
			KubeConfig: &fluxmeta.SecretKeyReference{Name: "kubeconfig-secret", Key: "value"},
		}}

		err := RegionClusterReference(context.Background(), c, "kcm-system", rgn)
		if err == nil || !strings.Contains(err.Error(), "failed to get Secret") {
			t.Fatalf("err = %v, want get Secret error", err)
		}
	})

	t.Run("kubeConfig Secret missing the configured key: error", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "kubeconfig-secret", Namespace: "kcm-system"},
			Data:       map[string][]byte{"other-key": []byte("x")},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(secret).Build()

		rgn := &kcmv1.Region{Spec: kcmv1.RegionSpec{
			KubeConfig: &fluxmeta.SecretKeyReference{Name: "kubeconfig-secret", Key: "value"},
		}}

		err := RegionClusterReference(context.Background(), c, "kcm-system", rgn)
		if err == nil || !strings.Contains(err.Error(), "does not have value key defined") {
			t.Fatalf("err = %v, want missing key error", err)
		}
	})

	t.Run("kubeConfig Secret found with key: success", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "kubeconfig-secret", Namespace: "kcm-system"},
			Data:       map[string][]byte{"value": []byte("kubeconfig-bytes")},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(secret).Build()

		rgn := &kcmv1.Region{Spec: kcmv1.RegionSpec{
			KubeConfig: &fluxmeta.SecretKeyReference{Name: "kubeconfig-secret", Key: "value"},
		}}

		if err := RegionClusterReference(context.Background(), c, "kcm-system", rgn); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("clusterDeployment reference missing: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		rgn := &kcmv1.Region{Spec: kcmv1.RegionSpec{
			ClusterDeployment: &kcmv1.ClusterDeploymentRef{Namespace: "ns1", Name: "cd1"},
		}}

		err := RegionClusterReference(context.Background(), c, "kcm-system", rgn)
		if err == nil || !strings.Contains(err.Error(), "failed to get ClusterDeployment") {
			t.Fatalf("err = %v, want get ClusterDeployment error", err)
		}
	})

	t.Run("clusterDeployment reference found: success", func(t *testing.T) {
		cd := &kcmv1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(cd).Build()

		rgn := &kcmv1.Region{Spec: kcmv1.RegionSpec{
			ClusterDeployment: &kcmv1.ClusterDeploymentRef{Namespace: "ns1", Name: "cd1"},
		}}

		if err := RegionClusterReference(context.Background(), c, "kcm-system", rgn); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestRegionDeletionAllowed(t *testing.T) {
	rgn := &kcmv1.Region{ObjectMeta: metav1.ObjectMeta{Name: "region1"}}

	t.Run("no Credentials for region: allowed", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithScheme(testscheme.Scheme).
			WithIndex(&kcmv1.Credential{}, kcmv1.CredentialRegionIndexKey, kcmv1.ExtractCredentialRegion).
			WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentCredentialIndexKey, kcmv1.ExtractCredentialNameFromClusterDeployment).
			Build()

		if err := RegionDeletionAllowed(context.Background(), c, rgn); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("Credential exists for region but no ClusterDeployments use it: allowed", func(t *testing.T) {
		cred := &kcmv1.Credential{
			ObjectMeta: metav1.ObjectMeta{Name: "cred1", Namespace: "ns1"},
			Spec:       kcmv1.CredentialSpec{Region: "region1"},
		}
		c := fake.NewClientBuilder().
			WithScheme(testscheme.Scheme).
			WithIndex(&kcmv1.Credential{}, kcmv1.CredentialRegionIndexKey, kcmv1.ExtractCredentialRegion).
			WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentCredentialIndexKey, kcmv1.ExtractCredentialNameFromClusterDeployment).
			WithObjects(cred).
			Build()

		if err := RegionDeletionAllowed(context.Background(), c, rgn); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("ClusterDeployment uses a Credential of the region: not allowed", func(t *testing.T) {
		cred := &kcmv1.Credential{
			ObjectMeta: metav1.ObjectMeta{Name: "cred1", Namespace: "ns1"},
			Spec:       kcmv1.CredentialSpec{Region: "region1"},
		}
		cd := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"},
			Spec:       kcmv1.ClusterDeploymentSpec{Credential: "cred1"},
		}
		c := fake.NewClientBuilder().
			WithScheme(testscheme.Scheme).
			WithIndex(&kcmv1.Credential{}, kcmv1.CredentialRegionIndexKey, kcmv1.ExtractCredentialRegion).
			WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentCredentialIndexKey, kcmv1.ExtractCredentialNameFromClusterDeployment).
			WithObjects(cred, cd).
			Build()

		err := RegionDeletionAllowed(context.Background(), c, rgn)
		if err == nil || !strings.Contains(err.Error(), "can't be removed while any ClusterDeployment") {
			t.Fatalf("err = %v, want deletion-blocked error", err)
		}
	})
}
