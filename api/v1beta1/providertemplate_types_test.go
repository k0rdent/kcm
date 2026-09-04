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

func TestProviderTemplateFillStatusWithProviders(t *testing.T) {
	t.Run("providers and contracts from spec", func(t *testing.T) {
		pt := &ProviderTemplate{
			TypeMeta: metav1.TypeMeta{Kind: ProviderTemplateKind},
			Spec: ProviderTemplateSpec{
				Providers:     Providers{"aws"},
				CAPIContracts: CompatibilityContracts{"v1beta1": "v1beta1_v1beta2"},
			},
		}

		if err := pt.FillStatusWithProviders(nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(pt.Status.Providers, Providers{"aws"}) {
			t.Errorf("Status.Providers = %v", pt.Status.Providers)
		}
		if pt.Status.CAPIContracts["v1beta1"] != "v1beta1_v1beta2" {
			t.Errorf("Status.CAPIContracts = %+v", pt.Status.CAPIContracts)
		}
	})

	t.Run("invalid contracts returns error", func(t *testing.T) {
		pt := &ProviderTemplate{
			TypeMeta:   metav1.TypeMeta{Kind: ProviderTemplateKind},
			ObjectMeta: metav1.ObjectMeta{Name: "pt1"},
			Spec:       ProviderTemplateSpec{CAPIContracts: CompatibilityContracts{"not-a-version": "v1beta1"}},
		}
		if err := pt.FillStatusWithProviders(nil); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestProviderTemplateGetHelmSpec(t *testing.T) {
	pt := &ProviderTemplate{Spec: ProviderTemplateSpec{Helm: HelmSpec{ChartSpec: &sourcev1.HelmChartSpec{Chart: "mychart"}}}}
	got := pt.GetHelmSpec()
	if got != &pt.Spec.Helm {
		t.Error("GetHelmSpec() did not return a pointer to Spec.Helm")
	}
}

func TestProviderTemplateGetCommonStatus(t *testing.T) {
	pt := &ProviderTemplate{Status: ProviderTemplateStatus{
		TemplateStatusCommon: TemplateStatusCommon{ChartVersion: "1.0.0"},
	}}
	got := pt.GetCommonStatus()
	if got != &pt.Status.TemplateStatusCommon {
		t.Error("GetCommonStatus() did not return a pointer to Status.TemplateStatusCommon")
	}
}
