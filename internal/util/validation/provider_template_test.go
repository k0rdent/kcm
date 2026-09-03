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

func TestGetInUseProvidersWithContracts(t *testing.T) {
	pTpl := &kcmv1.ProviderTemplate{
		Status: kcmv1.ProviderTemplateStatus{Providers: kcmv1.Providers{"aws"}},
	}

	t.Run("no ClusterTemplates reference the provider: empty result", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithScheme(testscheme.Scheme).
			WithIndex(&kcmv1.ClusterTemplate{}, kcmv1.ClusterTemplateProvidersIndexKey, kcmv1.ExtractProvidersFromClusterTemplate).
			WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment).
			Build()

		got, err := getInUseProvidersWithContracts(context.Background(), c, pTpl)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %+v, want empty", got)
		}
	})

	t.Run("ClusterTemplate exists but no ClusterDeployments use it: entry with no regions/contracts", func(t *testing.T) {
		ct := &kcmv1.ClusterTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "ct1", Namespace: "ns1"},
			Status:     kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"aws"}},
		}
		c := fake.NewClientBuilder().
			WithScheme(testscheme.Scheme).
			WithIndex(&kcmv1.ClusterTemplate{}, kcmv1.ClusterTemplateProvidersIndexKey, kcmv1.ExtractProvidersFromClusterTemplate).
			WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment).
			WithObjects(ct).
			Build()

		got, err := getInUseProvidersWithContracts(context.Background(), c, pTpl)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		params, ok := got["aws"]
		if !ok {
			t.Fatalf("got %+v, want an entry for aws (found via the ClusterTemplate, even with no deployments)", got)
		}
		if len(params.Regions) != 0 || len(params.ProviderContracts) != 0 {
			t.Errorf("params = %+v, want no regions/contracts", params)
		}
	})

	t.Run("ClusterDeployment uses the ClusterTemplate: provider is in use", func(t *testing.T) {
		ct := &kcmv1.ClusterTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "ct1", Namespace: "ns1"},
			Status: kcmv1.ClusterTemplateStatus{
				Providers:         kcmv1.Providers{"aws"},
				ProviderContracts: kcmv1.CompatibilityContracts{"aws": "v1beta1"},
			},
		}
		cd := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"},
			Spec:       kcmv1.ClusterDeploymentSpec{Template: "ct1"},
			Status:     kcmv1.ClusterDeploymentStatus{Region: "region1"},
		}
		c := fake.NewClientBuilder().
			WithScheme(testscheme.Scheme).
			WithIndex(&kcmv1.ClusterTemplate{}, kcmv1.ClusterTemplateProvidersIndexKey, kcmv1.ExtractProvidersFromClusterTemplate).
			WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment).
			WithObjects(ct, cd).
			Build()

		got, err := getInUseProvidersWithContracts(context.Background(), c, pTpl)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		params, ok := got["aws"]
		if !ok {
			t.Fatalf("got %+v, want an entry for aws", got)
		}
		if !params.Regions["region1"] {
			t.Errorf("Regions = %+v, want region1 to be true", params.Regions)
		}
		if len(params.ProviderContracts) != 1 || params.ProviderContracts[0] != "v1beta1" {
			t.Errorf("ProviderContracts = %+v, want [v1beta1]", params.ProviderContracts)
		}
	})
}

func TestProvidersInUseFor(t *testing.T) {
	pTpl := &kcmv1.ProviderTemplate{
		Status: kcmv1.ProviderTemplateStatus{Providers: kcmv1.Providers{"aws"}},
	}

	t.Run("unsupported object kind returns error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		// ComponentsManager is only implemented by Management and Region; kind is
		// read from the object's GVK rather than its Go type, so a Management
		// value with an unrelated Kind exercises the "unsupported" default case.
		obj := &kcmv1.Management{}
		obj.SetGroupVersionKind(kcmv1.GroupVersion.WithKind(kcmv1.AccessManagementKind))

		_, err := ProvidersInUseFor(context.Background(), c, pTpl, obj)
		if err == nil || !strings.Contains(err.Error(), "unsupported object kind") {
			t.Fatalf("err = %v, want unsupported object kind error", err)
		}
	})

	t.Run("Management: providers in use with empty region name", func(t *testing.T) {
		ct := &kcmv1.ClusterTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "ct1", Namespace: "ns1"},
			Status: kcmv1.ClusterTemplateStatus{
				Providers:         kcmv1.Providers{"aws"},
				ProviderContracts: kcmv1.CompatibilityContracts{"aws": "v1beta1"},
			},
		}
		cd := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"},
			Spec:       kcmv1.ClusterDeploymentSpec{Template: "ct1"},
			// Status.Region left empty: management-level (non-regional) deployment
		}
		c := fake.NewClientBuilder().
			WithScheme(testscheme.Scheme).
			WithIndex(&kcmv1.ClusterTemplate{}, kcmv1.ClusterTemplateProvidersIndexKey, kcmv1.ExtractProvidersFromClusterTemplate).
			WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment).
			WithObjects(ct, cd).
			Build()

		mgmt := &kcmv1.Management{}
		mgmt.SetGroupVersionKind(kcmv1.GroupVersion.WithKind(kcmv1.ManagementKind))

		got, err := ProvidersInUseFor(context.Background(), c, pTpl, mgmt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if contracts, ok := got["aws"]; !ok || len(contracts) != 1 || contracts[0] != "v1beta1" {
			t.Errorf("got %+v, want aws: [v1beta1]", got)
		}
	})

	t.Run("Region: providers in use for the named region only", func(t *testing.T) {
		ct := &kcmv1.ClusterTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "ct1", Namespace: "ns1"},
			Status: kcmv1.ClusterTemplateStatus{
				Providers:         kcmv1.Providers{"aws"},
				ProviderContracts: kcmv1.CompatibilityContracts{"aws": "v1beta1"},
			},
		}
		cd := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"},
			Spec:       kcmv1.ClusterDeploymentSpec{Template: "ct1"},
			Status:     kcmv1.ClusterDeploymentStatus{Region: "other-region"},
		}
		c := fake.NewClientBuilder().
			WithScheme(testscheme.Scheme).
			WithIndex(&kcmv1.ClusterTemplate{}, kcmv1.ClusterTemplateProvidersIndexKey, kcmv1.ExtractProvidersFromClusterTemplate).
			WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment).
			WithObjects(ct, cd).
			Build()

		rgn := &kcmv1.Region{ObjectMeta: metav1.ObjectMeta{Name: "region1"}}
		rgn.SetGroupVersionKind(kcmv1.GroupVersion.WithKind(kcmv1.RegionKind))

		got, err := ProvidersInUseFor(context.Background(), c, pTpl, rgn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %+v, want empty (provider is used in a different region)", got)
		}
	})
}
