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

package v1beta1

import (
	"reflect"
	"testing"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestClusterTemplateFillStatusWithProviders(t *testing.T) {
	t.Run("providers and valid k8s version from spec", func(t *testing.T) {
		ct := &ClusterTemplate{
			TypeMeta: metav1.TypeMeta{Kind: ClusterTemplateKind},
			Spec: ClusterTemplateSpec{
				Providers:         Providers{"aws"},
				ProviderContracts: CompatibilityContracts{"aws": "v1beta1"},
				KubernetesVersion: "v1.30.0",
			},
		}

		if err := ct.FillStatusWithProviders(nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(ct.Status.Providers, Providers{"aws"}) {
			t.Errorf("Status.Providers = %v", ct.Status.Providers)
		}
		if ct.Status.ProviderContracts["aws"] != "v1beta1" {
			t.Errorf("Status.ProviderContracts = %+v", ct.Status.ProviderContracts)
		}
		if ct.Status.KubernetesVersion != "v1.30.0" {
			t.Errorf("Status.KubernetesVersion = %q", ct.Status.KubernetesVersion)
		}
	})

	t.Run("k8s version from annotation when spec is unset", func(t *testing.T) {
		ct := &ClusterTemplate{TypeMeta: metav1.TypeMeta{Kind: ClusterTemplateKind}}
		if err := ct.FillStatusWithProviders(map[string]string{ChartAnnotationKubernetesVersion: "v1.29.5"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ct.Status.KubernetesVersion != "v1.29.5" {
			t.Errorf("Status.KubernetesVersion = %q, want v1.29.5", ct.Status.KubernetesVersion)
		}
	})

	t.Run("no k8s version at all: left empty, no error", func(t *testing.T) {
		ct := &ClusterTemplate{TypeMeta: metav1.TypeMeta{Kind: ClusterTemplateKind}}
		if err := ct.FillStatusWithProviders(nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ct.Status.KubernetesVersion != "" {
			t.Errorf("Status.KubernetesVersion = %q, want empty", ct.Status.KubernetesVersion)
		}
	})

	t.Run("invalid k8s version returns error", func(t *testing.T) {
		ct := &ClusterTemplate{
			TypeMeta: metav1.TypeMeta{Kind: ClusterTemplateKind},
			Spec:     ClusterTemplateSpec{KubernetesVersion: "not-a-semver"},
		}
		if err := ct.FillStatusWithProviders(nil); err == nil {
			t.Fatal("expected error for invalid k8s version, got nil")
		}
	})

	t.Run("invalid provider contracts returns error", func(t *testing.T) {
		ct := &ClusterTemplate{
			TypeMeta: metav1.TypeMeta{Kind: ClusterTemplateKind},
			Spec:     ClusterTemplateSpec{ProviderContracts: CompatibilityContracts{"aws": "not-a-version"}},
		}
		if err := ct.FillStatusWithProviders(nil); err == nil {
			t.Fatal("expected error for invalid provider contract, got nil")
		}
	})
}

func TestClusterTemplateGetSpecProviders(t *testing.T) {
	ct := &ClusterTemplate{Spec: ClusterTemplateSpec{Providers: Providers{"aws", "azure"}}}
	if got := ct.GetSpecProviders(); !reflect.DeepEqual(got, Providers{"aws", "azure"}) {
		t.Errorf("got %v", got)
	}
}

func TestClusterTemplateGetHelmSpec(t *testing.T) {
	ct := &ClusterTemplate{Spec: ClusterTemplateSpec{Helm: HelmSpec{ChartSpec: &sourcev1.HelmChartSpec{Chart: "mychart"}}}}
	got := ct.GetHelmSpec()
	if got != &ct.Spec.Helm {
		t.Error("GetHelmSpec() did not return a pointer to Spec.Helm")
	}
}

func TestClusterTemplateGetCommonStatus(t *testing.T) {
	ct := &ClusterTemplate{Status: ClusterTemplateStatus{
		TemplateStatusCommon: TemplateStatusCommon{ChartVersion: "1.0.0"},
	}}
	got := ct.GetCommonStatus()
	if got != &ct.Status.TemplateStatusCommon {
		t.Error("GetCommonStatus() did not return a pointer to Status.TemplateStatusCommon")
	}
	if got.ChartVersion != "1.0.0" {
		t.Errorf("ChartVersion = %q", got.ChartVersion)
	}
}
