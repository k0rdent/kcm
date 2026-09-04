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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReleaseProviderTemplate(t *testing.T) {
	release := &Release{Spec: ReleaseSpec{Providers: []NamedProviderTemplate{
		{Name: "aws", CoreProviderTemplate: CoreProviderTemplate{Template: "aws-tpl"}},
	}}}

	if got := release.ProviderTemplate("aws"); got != "aws-tpl" {
		t.Errorf("got %q, want %q", got, "aws-tpl")
	}
	if got := release.ProviderTemplate("missing"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestReleaseProviders(t *testing.T) {
	release := &Release{Spec: ReleaseSpec{Providers: []NamedProviderTemplate{
		{Name: "aws", CoreProviderTemplate: CoreProviderTemplate{Template: "aws-tpl"}},
		{Name: "azure", CoreProviderTemplate: CoreProviderTemplate{Template: "azure-tpl"}},
	}}}

	got := release.Providers()
	want := []Provider{{Name: "aws"}, {Name: "azure"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestReleaseTemplates(t *testing.T) {
	t.Run("core + provider templates, no regional", func(t *testing.T) {
		release := &Release{Spec: ReleaseSpec{
			KCM:       CoreProviderTemplate{Template: "kcm-tpl"},
			CAPI:      CoreProviderTemplate{Template: "capi-tpl"},
			Providers: []NamedProviderTemplate{{Name: "aws", CoreProviderTemplate: CoreProviderTemplate{Template: "aws-tpl"}}},
		}}
		want := []string{"kcm-tpl", "capi-tpl", "aws-tpl"}
		if got := release.Templates(); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("regional template from spec included", func(t *testing.T) {
		release := &Release{Spec: ReleaseSpec{
			KCM:      CoreProviderTemplate{Template: "kcm-tpl"},
			CAPI:     CoreProviderTemplate{Template: "capi-tpl"},
			Regional: CoreProviderTemplate{Template: "regional-tpl"},
		}}
		want := []string{"kcm-tpl", "capi-tpl", "regional-tpl"}
		if got := release.Templates(); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("regional template falls back to annotation", func(t *testing.T) {
		release := &Release{
			ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{KCMRegionalTemplateAnnotation: "annotated-regional-tpl"}},
			Spec: ReleaseSpec{
				KCM:  CoreProviderTemplate{Template: "kcm-tpl"},
				CAPI: CoreProviderTemplate{Template: "capi-tpl"},
			},
		}
		want := []string{"kcm-tpl", "capi-tpl", "annotated-regional-tpl"}
		if got := release.Templates(); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("no regional template at all: omitted", func(t *testing.T) {
		release := &Release{Spec: ReleaseSpec{
			KCM:  CoreProviderTemplate{Template: "kcm-tpl"},
			CAPI: CoreProviderTemplate{Template: "capi-tpl"},
		}}
		want := []string{"kcm-tpl", "capi-tpl"}
		if got := release.Templates(); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestReleaseGetConditions(t *testing.T) {
	release := &Release{Status: ReleaseStatus{Conditions: []metav1.Condition{{Type: "Ready"}}}}
	got := release.GetConditions()
	if got != &release.Status.Conditions {
		t.Error("GetConditions() did not return a pointer to Status.Conditions")
	}
}
