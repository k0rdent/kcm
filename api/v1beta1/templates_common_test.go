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

package v1beta1

import (
	"reflect"
	"testing"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
)

func TestHelmSpecString(t *testing.T) {
	t.Run("ChartRef without namespace", func(t *testing.T) {
		s := &HelmSpec{ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{Name: "chart1", Kind: "HelmChart"}}
		if got := s.String(); got != "chart1, Kind=HelmChart" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("ChartRef with namespace", func(t *testing.T) {
		s := &HelmSpec{ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{Namespace: "ns1", Name: "chart1", Kind: "HelmChart"}}
		if got := s.String(); got != "ns1/chart1, Kind=HelmChart" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("ChartSpec with version", func(t *testing.T) {
		s := &HelmSpec{ChartSpec: &sourcev1.HelmChartSpec{Chart: "mychart", Version: "1.2.3"}}
		if got := s.String(); got != "mychart: 1.2.3" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("ChartSpec without version", func(t *testing.T) {
		s := &HelmSpec{ChartSpec: &sourcev1.HelmChartSpec{Chart: "mychart"}}
		if got := s.String(); got != "mychart" {
			t.Errorf("got %q", got)
		}
	})
}

func TestGetProvidersList(t *testing.T) {
	t.Run("explicit providers are sorted and deduplicated", func(t *testing.T) {
		got := getProvidersList(Providers{"azure", "aws", "aws"}, nil)
		want := Providers{"aws", "azure"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("falls back to annotation when spec providers are empty", func(t *testing.T) {
		got := getProvidersList(nil, map[string]string{"cluster.x-k8s.io/provider": "azure, aws ,, aws"})
		want := Providers{"aws", "azure"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("no providers and no annotation: empty", func(t *testing.T) {
		got := getProvidersList(nil, nil)
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})
}

func TestGetCAPIContracts(t *testing.T) {
	t.Run("ClusterTemplate: valid spec contracts", func(t *testing.T) {
		got, err := getCAPIContracts(ClusterTemplateKind, CompatibilityContracts{"aws": "v1beta1"}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["aws"] != "v1beta1" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("ClusterTemplate: invalid spec contract version returns error", func(t *testing.T) {
		_, err := getCAPIContracts(ClusterTemplateKind, CompatibilityContracts{"aws": "not-a-version"}, nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("ClusterTemplate: falls back to annotations", func(t *testing.T) {
		got, err := getCAPIContracts(ClusterTemplateKind, nil, map[string]string{
			"cluster.x-k8s.io/infrastructure-aws": "v1beta1",
			"unrelated-annotation":                "ignored",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["infrastructure-aws"] != "v1beta1" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("ClusterTemplate: invalid annotation contract version is an error", func(t *testing.T) {
		_, err := getCAPIContracts(ClusterTemplateKind, nil, map[string]string{
			"cluster.x-k8s.io/infrastructure-aws": "not-a-version",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("ProviderTemplate: valid spec contracts, multi-version allowed", func(t *testing.T) {
		got, err := getCAPIContracts(ProviderTemplateKind, CompatibilityContracts{"v1beta1": "v1beta1_v1beta2"}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["v1beta1"] != "v1beta1_v1beta2" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("ProviderTemplate: invalid spec key (not a CAPI contract version) is an error", func(t *testing.T) {
		_, err := getCAPIContracts(ProviderTemplateKind, CompatibilityContracts{"not-a-version": "v1beta1"}, nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("ProviderTemplate: falls back to annotations, empty value is the core CAPI special case", func(t *testing.T) {
		got, err := getCAPIContracts(ProviderTemplateKind, nil, map[string]string{
			"cluster.x-k8s.io/v1beta1": "",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v, ok := got["v1beta1"]; !ok || v != "" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("no contracts, no annotations: empty result", func(t *testing.T) {
		got, err := getCAPIContracts(ClusterTemplateKind, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %+v, want empty", got)
		}
	})
}
