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

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestProviderString(t *testing.T) {
	p := Provider{Name: "aws"}
	if got := p.String(); got != "aws" {
		t.Errorf("got %q, want %q", got, "aws")
	}
}

func TestManagementTemplates(t *testing.T) {
	t.Run("no core, no providers: empty", func(t *testing.T) {
		mgmt := &Management{}
		if got := mgmt.Templates(); len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})

	t.Run("core and provider templates collected", func(t *testing.T) {
		mgmt := &Management{
			Spec: ManagementSpec{
				ComponentsCommonSpec: ComponentsCommonSpec{
					Core: &Core{
						CAPI: Component{Template: "capi-tpl"},
						KCM:  Component{Template: "kcm-tpl"},
					},
					Providers: []Provider{
						{Name: "aws", Component: Component{Template: "aws-tpl"}},
						{Name: "azure"}, // no template: skipped
					},
				},
			},
		}
		want := []string{"capi-tpl", "kcm-tpl", "aws-tpl"}
		if got := mgmt.Templates(); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestManagementGetConditions(t *testing.T) {
	mgmt := &Management{Status: ManagementStatus{Conditions: []metav1.Condition{{Type: "Ready"}}}}
	got := mgmt.GetConditions()
	if got != &mgmt.Status.Conditions {
		t.Error("GetConditions() did not return a pointer to Status.Conditions")
	}
	if len(*got) != 1 || (*got)[0].Type != "Ready" {
		t.Errorf("got %+v", *got)
	}
}

func TestManagementComponents(t *testing.T) {
	mgmt := &Management{Spec: ManagementSpec{
		ComponentsCommonSpec: ComponentsCommonSpec{Providers: []Provider{{Name: "aws"}}},
	}}
	got := mgmt.Components()
	if len(got.Providers) != 1 || got.Providers[0].Name != "aws" {
		t.Errorf("got %+v", got)
	}
}

func TestManagementKCMComponentInfo(t *testing.T) {
	mgmt := &Management{}
	release := &Release{Spec: ReleaseSpec{KCM: CoreProviderTemplate{Template: "kcm-default-tpl"}}}

	got := mgmt.KCMComponentInfo(release, "my-release-name")
	want := KCMComponentInfo{ChartName: CoreKCMName, DefaultTemplate: "kcm-default-tpl", ReleaseName: "my-release-name"}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestManagementHelmReleasePrefix(t *testing.T) {
	mgmt := &Management{}
	if got := mgmt.HelmReleasePrefix(); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestManagementGetComponentsStatus(t *testing.T) {
	mgmt := &Management{Status: ManagementStatus{
		ComponentsCommonStatus: ComponentsCommonStatus{AvailableProviders: Providers{"aws"}},
	}}
	got := mgmt.GetComponentsStatus()
	if got != &mgmt.Status.ComponentsCommonStatus {
		t.Error("GetComponentsStatus() did not return a pointer to Status.ComponentsCommonStatus")
	}
	if len(got.AvailableProviders) != 1 || got.AvailableProviders[0] != "aws" {
		t.Errorf("got %+v", got)
	}
}

func TestComponentHelmValues(t *testing.T) {
	t.Run("no config: nil values, no error", func(t *testing.T) {
		c := &Component{}
		got, err := c.HelmValues()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("valid config: parsed values", func(t *testing.T) {
		c := &Component{Config: &apiextv1.JSON{Raw: []byte(`{"key":"value"}`)}}
		got, err := c.HelmValues()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["key"] != "value" {
			t.Errorf("got %+v, want key=value", got)
		}
	})

	t.Run("invalid config: error", func(t *testing.T) {
		c := &Component{Config: &apiextv1.JSON{Raw: []byte(`not-valid-yaml: [`)}}
		_, err := c.HelmValues()
		if err == nil {
			t.Fatal("expected error for invalid config, got nil")
		}
	})
}
