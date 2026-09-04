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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRegionGetConditions(t *testing.T) {
	rgn := &Region{Status: RegionStatus{Conditions: []metav1.Condition{{Type: "Ready"}}}}
	got := rgn.GetConditions()
	if got != &rgn.Status.Conditions {
		t.Error("GetConditions() did not return a pointer to Status.Conditions")
	}
}

func TestRegionComponents(t *testing.T) {
	rgn := &Region{Spec: RegionSpec{
		ComponentsCommonSpec: ComponentsCommonSpec{Providers: []Provider{{Name: "aws"}}},
	}}
	got := rgn.Components()
	if len(got.Providers) != 1 || got.Providers[0].Name != "aws" {
		t.Errorf("got %+v", got)
	}
}

func TestRegionKCMComponentInfo(t *testing.T) {
	rgn := &Region{}
	release := &Release{Spec: ReleaseSpec{Regional: CoreProviderTemplate{Template: "regional-tpl"}}}

	got := rgn.KCMComponentInfo(release, "ignored-param")
	want := KCMComponentInfo{ChartName: CoreKCMRegionalName, DefaultTemplate: "regional-tpl", ReleaseName: CoreKCMRegionalName}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestRegionHelmReleasePrefix(t *testing.T) {
	rgn := &Region{ObjectMeta: metav1.ObjectMeta{Name: "region1"}}
	if got := rgn.HelmReleasePrefix(); got != "region1" {
		t.Errorf("got %q, want %q", got, "region1")
	}
}

func TestRegionGetComponentsStatus(t *testing.T) {
	rgn := &Region{Status: RegionStatus{
		ComponentsCommonStatus: ComponentsCommonStatus{AvailableProviders: Providers{"aws"}},
	}}
	got := rgn.GetComponentsStatus()
	if got != &rgn.Status.ComponentsCommonStatus {
		t.Error("GetComponentsStatus() did not return a pointer to Status.ComponentsCommonStatus")
	}
	if len(got.AvailableProviders) != 1 || got.AvailableProviders[0] != "aws" {
		t.Errorf("got %+v", got)
	}
}
