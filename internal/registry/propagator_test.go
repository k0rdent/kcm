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

package registry

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	schemeutil "github.com/K0rdent/kcm/internal/util/scheme"
)

func TestPropagator_ListManagedRegistrySecrets(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = kcmv1.AddToScheme(scheme)

	// Create test secrets
	managedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "managed-secret",
			Namespace: "test-ns",
			Labels: map[string]string{
				RegistrySecretLabel: RegistrySecretLabelValue,
			},
		},
	}

	unmanagedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unmanaged-secret",
			Namespace: "test-ns",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(managedSecret, unmanagedSecret).
		Build()

	p := NewPropagator(fakeClient)

	secrets, err := p.ListManagedRegistrySecrets(context.Background(), "test-ns")
	if err != nil {
		t.Fatalf("ListManagedRegistrySecrets() error = %v", err)
	}

	if len(secrets) != 1 {
		t.Errorf("Expected 1 managed secret, got %d", len(secrets))
	}

	if len(secrets) > 0 && secrets[0].Name != "managed-secret" {
		t.Errorf("Expected managed-secret, got %s", secrets[0].Name)
	}
}

func TestPropagator_GetNamespacesRequiringCredentials(t *testing.T) {
	scheme := schemeutil.MustGetManagementScheme()

	// Create test ClusterDeployments
	cd1 := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster1",
			Namespace: "ns1",
		},
		Spec: kcmv1.ClusterDeploymentSpec{
			InheritRegistryCredentials: true,
		},
	}

	cd2 := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster2",
			Namespace: "ns2",
		},
		Spec: kcmv1.ClusterDeploymentSpec{
			InheritRegistryCredentials: false,
		},
	}

	cd3 := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster3",
			Namespace: "ns1", // Same namespace as cd1
		},
		Spec: kcmv1.ClusterDeploymentSpec{
			InheritRegistryCredentials: true,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cd1, cd2, cd3).
		Build()

	p := NewPropagator(fakeClient)

	namespaces, err := p.GetNamespacesRequiringCredentials(context.Background(), "kcm-system")
	if err != nil {
		t.Fatalf("GetNamespacesRequiringCredentials() error = %v", err)
	}

	// Should include: kcm-system, ns1 (but not ns2 since cd2 doesn't inherit)
	// ns1 appears only once even though there are 2 CDs in it
	expectedNamespaces := map[string]bool{
		"kcm-system": true,
		"ns1":        true,
	}

	if len(namespaces) != len(expectedNamespaces) {
		t.Errorf("Expected %d namespaces, got %d: %v", len(expectedNamespaces), len(namespaces), namespaces)
	}

	for _, ns := range namespaces {
		if !expectedNamespaces[ns] {
			t.Errorf("Unexpected namespace in result: %s", ns)
		}
	}
}

