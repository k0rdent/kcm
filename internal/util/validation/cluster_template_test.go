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

package validation

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func TestValidateCompatibilityAttrs(t *testing.T) {
	t.Run("all providers exposed and contracts satisfied: no error", func(t *testing.T) {
		clusterTemplate := &kcmv1.ClusterTemplate{
			Status: kcmv1.ClusterTemplateStatus{
				Providers:         kcmv1.Providers{"aws"},
				ProviderContracts: kcmv1.CompatibilityContracts{"aws": "v1beta1"},
			},
		}
		parent := &kcmv1.Management{
			Status: kcmv1.ManagementStatus{
				ComponentsCommonStatus: kcmv1.ComponentsCommonStatus{
					AvailableProviders: kcmv1.Providers{"aws"},
					CAPIContracts: map[string]kcmv1.CompatibilityContracts{
						"aws": {"v1beta2": "v1beta1_v1beta2"},
					},
				},
			},
		}

		if err := validateCompatibilityAttrs(context.Background(), clusterTemplate, parent); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing provider returns error", func(t *testing.T) {
		clusterTemplate := &kcmv1.ClusterTemplate{
			Status: kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"aws"}},
		}
		parent := &kcmv1.Management{}

		err := validateCompatibilityAttrs(context.Background(), clusterTemplate, parent)
		if err == nil || !strings.Contains(err.Error(), "one or more required providers are not deployed yet") {
			t.Fatalf("err = %v, want missing-providers error", err)
		}
	})

	t.Run("provider exposed but contract not satisfied returns error", func(t *testing.T) {
		clusterTemplate := &kcmv1.ClusterTemplate{
			Status: kcmv1.ClusterTemplateStatus{
				Providers:         kcmv1.Providers{"aws"},
				ProviderContracts: kcmv1.CompatibilityContracts{"aws": "v1beta3"},
			},
		}
		parent := &kcmv1.Management{
			Status: kcmv1.ManagementStatus{
				ComponentsCommonStatus: kcmv1.ComponentsCommonStatus{
					AvailableProviders: kcmv1.Providers{"aws"},
					CAPIContracts: map[string]kcmv1.CompatibilityContracts{
						"aws": {"v1beta2": "v1beta1_v1beta2"},
					},
				},
			},
		}

		err := validateCompatibilityAttrs(context.Background(), clusterTemplate, parent)
		if err == nil || !strings.Contains(err.Error(), "does not satisfy deployed") {
			t.Fatalf("err = %v, want contract-not-satisfied error", err)
		}
	})

	t.Run("provider without CAPI contracts entry is skipped (no validation)", func(t *testing.T) {
		clusterTemplate := &kcmv1.ClusterTemplate{
			Status: kcmv1.ClusterTemplateStatus{
				Providers:         kcmv1.Providers{"aws"},
				ProviderContracts: kcmv1.CompatibilityContracts{"aws": "v1beta3"},
			},
		}
		parent := &kcmv1.Management{
			Status: kcmv1.ManagementStatus{
				ComponentsCommonStatus: kcmv1.ComponentsCommonStatus{
					AvailableProviders: kcmv1.Providers{"aws"},
					// no CAPIContracts entry for "aws"
				},
			},
		}

		if err := validateCompatibilityAttrs(context.Background(), clusterTemplate, parent); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestClusterTemplateProviders(t *testing.T) {
	t.Run("Credential not found returns error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()
		cd := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"},
			Spec:       kcmv1.ClusterDeploymentSpec{Credential: "missing-cred"},
		}

		err := ClusterTemplateProviders(context.Background(), c, &kcmv1.ClusterTemplate{}, cd)
		if err == nil || !strings.Contains(err.Error(), "failed to get") {
			t.Fatalf("err = %v, want get Credential error", err)
		}
	})

	t.Run("all providers exposed and satisfied: no error", func(t *testing.T) {
		cred := &kcmv1.Credential{ObjectMeta: metav1.ObjectMeta{Name: "cred1", Namespace: "ns1"}}
		mgmt := &kcmv1.Management{
			ObjectMeta: metav1.ObjectMeta{Name: kcmv1.ManagementName},
			Status: kcmv1.ManagementStatus{
				ComponentsCommonStatus: kcmv1.ComponentsCommonStatus{
					AvailableProviders: kcmv1.Providers{"aws"},
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(cred, mgmt).Build()

		cd := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"},
			Spec:       kcmv1.ClusterDeploymentSpec{Credential: "cred1"},
		}
		clusterTemplate := &kcmv1.ClusterTemplate{
			Status: kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"aws"}},
		}

		if err := ClusterTemplateProviders(context.Background(), c, clusterTemplate, cd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("incompatible providers returns wrapped error naming the parent kind", func(t *testing.T) {
		cred := &kcmv1.Credential{ObjectMeta: metav1.ObjectMeta{Name: "cred1", Namespace: "ns1"}}
		mgmt := &kcmv1.Management{ObjectMeta: metav1.ObjectMeta{Name: kcmv1.ManagementName}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(cred, mgmt).Build()

		cd := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"},
			Spec:       kcmv1.ClusterDeploymentSpec{Credential: "cred1"},
		}
		clusterTemplate := &kcmv1.ClusterTemplate{
			Status: kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"aws"}},
		}

		err := ClusterTemplateProviders(context.Background(), c, clusterTemplate, cd)
		if err == nil || !strings.Contains(err.Error(), "incompatible providers in Management") {
			t.Fatalf("err = %v, want incompatible providers error naming Management", err)
		}
	})
}

func TestClusterTemplateK8sCompatibility(t *testing.T) {
	t.Run("no services and no k8s version: nothing to do", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		err := ClusterTemplateK8sCompatibility(context.Background(), c, &kcmv1.ClusterTemplate{}, &kcmv1.ClusterDeployment{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("disabled service is skipped", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{KubernetesVersion: "v1.30.0"}}
		cd := &kcmv1.ClusterDeployment{
			Spec: kcmv1.ClusterDeploymentSpec{
				ServiceSpec: kcmv1.ServiceSpec{
					Services: []kcmv1.Service{{Template: "missing-svc-tpl", Disable: true}},
				},
			},
		}

		if err := ClusterTemplateK8sCompatibility(context.Background(), c, clusterTemplate, cd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("ServiceTemplate not found returns error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{KubernetesVersion: "v1.30.0"}}
		cd := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
			Spec: kcmv1.ClusterDeploymentSpec{
				ServiceSpec: kcmv1.ServiceSpec{
					Services: []kcmv1.Service{{Template: "missing-svc-tpl"}},
				},
			},
		}

		err := ClusterTemplateK8sCompatibility(context.Background(), c, clusterTemplate, cd)
		if err == nil || !strings.Contains(err.Error(), "failed to get ServiceTemplate") {
			t.Fatalf("err = %v, want get ServiceTemplate error", err)
		}
	})

	t.Run("no constraint on ServiceTemplate: skipped", func(t *testing.T) {
		svcTpl := &kcmv1.ServiceTemplate{ObjectMeta: metav1.ObjectMeta{Name: "svc-tpl", Namespace: "ns1"}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(svcTpl).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{KubernetesVersion: "v1.30.0"}}
		cd := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
			Spec: kcmv1.ClusterDeploymentSpec{
				ServiceSpec: kcmv1.ServiceSpec{
					Services: []kcmv1.Service{{Template: "svc-tpl"}},
				},
			},
		}

		if err := ClusterTemplateK8sCompatibility(context.Background(), c, clusterTemplate, cd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("k8s version satisfies constraint: no error", func(t *testing.T) {
		svcTpl := &kcmv1.ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-tpl", Namespace: "ns1"},
			Status:     kcmv1.ServiceTemplateStatus{KubernetesConstraint: ">= 1.29.0"},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(svcTpl).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{KubernetesVersion: "v1.30.0"}}
		cd := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
			Spec: kcmv1.ClusterDeploymentSpec{
				ServiceSpec: kcmv1.ServiceSpec{
					Services: []kcmv1.Service{{Template: "svc-tpl"}},
				},
			},
		}

		if err := ClusterTemplateK8sCompatibility(context.Background(), c, clusterTemplate, cd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("k8s version violates constraint: error", func(t *testing.T) {
		svcTpl := &kcmv1.ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-tpl", Namespace: "ns1"},
			Status:     kcmv1.ServiceTemplateStatus{KubernetesConstraint: ">= 1.31.0"},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(svcTpl).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{KubernetesVersion: "v1.30.0"}}
		cd := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
			Spec: kcmv1.ClusterDeploymentSpec{
				ServiceSpec: kcmv1.ServiceSpec{
					Services: []kcmv1.Service{{Template: "svc-tpl"}},
				},
			},
		}

		err := ClusterTemplateK8sCompatibility(context.Background(), c, clusterTemplate, cd)
		if err == nil || !strings.Contains(err.Error(), "does not satisfy k8s constraint") {
			t.Fatalf("err = %v, want constraint-violation error", err)
		}
	})
}
