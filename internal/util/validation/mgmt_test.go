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
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func newTestManagement(providers ...kcmv1.Provider) *kcmv1.Management {
	mgmt := &kcmv1.Management{
		ObjectMeta: metav1.ObjectMeta{Name: kcmv1.ManagementName},
		Spec: kcmv1.ManagementSpec{
			ComponentsCommonSpec: kcmv1.ComponentsCommonSpec{Providers: providers},
		},
	}
	mgmt.SetGroupVersionKind(kcmv1.GroupVersion.WithKind(kcmv1.ManagementKind))
	return mgmt
}

func TestFindCAPITemplateName(t *testing.T) {
	release := &kcmv1.Release{Spec: kcmv1.ReleaseSpec{CAPI: kcmv1.CoreProviderTemplate{Template: "capi-from-release"}}}

	t.Run("uses object's Core.CAPI.Template when set", func(t *testing.T) {
		mgmt := newTestManagement()
		mgmt.Spec.Core = &kcmv1.Core{CAPI: kcmv1.Component{Template: "capi-from-obj"}}

		if got := findCAPITemplateName(release, mgmt); got != "capi-from-obj" {
			t.Errorf("got %q, want %q", got, "capi-from-obj")
		}
	})

	t.Run("falls back to release CAPI template", func(t *testing.T) {
		mgmt := newTestManagement()

		if got := findCAPITemplateName(release, mgmt); got != "capi-from-release" {
			t.Errorf("got %q, want %q", got, "capi-from-release")
		}
	})
}

func TestFindProviderTemplateName(t *testing.T) {
	release := &kcmv1.Release{Spec: kcmv1.ReleaseSpec{Providers: []kcmv1.NamedProviderTemplate{
		{Name: "aws", CoreProviderTemplate: kcmv1.CoreProviderTemplate{Template: "aws-from-release"}},
	}}}

	t.Run("uses provider's own Template when set", func(t *testing.T) {
		p := kcmv1.Provider{Name: "aws", Component: kcmv1.Component{Template: "aws-explicit"}}
		if got := findProviderTemplateName(release, p); got != "aws-explicit" {
			t.Errorf("got %q, want %q", got, "aws-explicit")
		}
	})

	t.Run("falls back to release provider template", func(t *testing.T) {
		p := kcmv1.Provider{Name: "aws"}
		if got := findProviderTemplateName(release, p); got != "aws-from-release" {
			t.Errorf("got %q, want %q", got, "aws-from-release")
		}
	})

	t.Run("unknown provider name with no explicit template: empty", func(t *testing.T) {
		p := kcmv1.Provider{Name: "unknown"}
		if got := findProviderTemplateName(release, p); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestValidateProviderContracts(t *testing.T) {
	release := &kcmv1.Release{Spec: kcmv1.ReleaseSpec{CAPI: kcmv1.CoreProviderTemplate{Template: "capi-tpl"}}}

	t.Run("capi ProviderTemplate not found: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()
		mgmt := newTestManagement()

		_, err := ValidateProviderContracts(context.Background(), c, release, mgmt)
		if err == nil || !strings.Contains(err.Error(), "failed to get ProviderTemplate") {
			t.Fatalf("err = %v, want get ProviderTemplate error", err)
		}
	})

	t.Run("capi ProviderTemplate not valid but has contracts: ErrProviderIsNotReady", func(t *testing.T) {
		capiTpl := &kcmv1.ProviderTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "capi-tpl"},
			Status: kcmv1.ProviderTemplateStatus{
				CAPIContracts:        kcmv1.CompatibilityContracts{"v1beta1": "v1beta1"},
				TemplateStatusCommon: kcmv1.TemplateStatusCommon{TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: false}},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(capiTpl).Build()
		mgmt := newTestManagement()

		_, err := ValidateProviderContracts(context.Background(), c, release, mgmt)
		if !errors.Is(err, ErrProviderIsNotReady) {
			t.Fatalf("err = %v, want ErrProviderIsNotReady", err)
		}
	})

	t.Run("no other providers: no error, empty result", func(t *testing.T) {
		capiTpl := &kcmv1.ProviderTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "capi-tpl"},
			Status:     kcmv1.ProviderTemplateStatus{TemplateStatusCommon: kcmv1.TemplateStatusCommon{TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: true}}},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(capiTpl).Build()
		mgmt := newTestManagement()

		got, err := ValidateProviderContracts(context.Background(), c, release, mgmt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("provider template matching capi template name is skipped", func(t *testing.T) {
		capiTpl := &kcmv1.ProviderTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "capi-tpl"},
			Status:     kcmv1.ProviderTemplateStatus{TemplateStatusCommon: kcmv1.TemplateStatusCommon{TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: true}}},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(capiTpl).Build()
		mgmt := newTestManagement(kcmv1.Provider{Name: "capi", Component: kcmv1.Component{Template: "capi-tpl"}})

		got, err := ValidateProviderContracts(context.Background(), c, release, mgmt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("provider ProviderTemplate not found: error propagated", func(t *testing.T) {
		capiTpl := &kcmv1.ProviderTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "capi-tpl"},
			Status:     kcmv1.ProviderTemplateStatus{TemplateStatusCommon: kcmv1.TemplateStatusCommon{TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: true}}},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(capiTpl).Build()
		mgmt := newTestManagement(kcmv1.Provider{Name: "aws", Component: kcmv1.Component{Template: "aws-tpl"}})

		_, err := ValidateProviderContracts(context.Background(), c, release, mgmt)
		if err == nil || !strings.Contains(err.Error(), "failed to get ProviderTemplate aws-tpl") {
			t.Fatalf("err = %v, want get ProviderTemplate aws-tpl error", err)
		}
	})
}

func TestValidateChangedProviderContracts(t *testing.T) {
	release := &kcmv1.Release{Spec: kcmv1.ReleaseSpec{CAPI: kcmv1.CoreProviderTemplate{Template: "capi-tpl"}}}

	t.Run("new capi ProviderTemplate not found: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()
		oldObj, newObj := newTestManagement(), newTestManagement()

		_, err := ValidateChangedProviderContracts(context.Background(), c, release, oldObj, newObj)
		if err == nil || !strings.Contains(err.Error(), "failed to get ProviderTemplate") {
			t.Fatalf("err = %v, want get ProviderTemplate error", err)
		}
	})

	t.Run("capi template changed and new one invalid: ErrProviderIsNotReady", func(t *testing.T) {
		capiTpl := &kcmv1.ProviderTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "new-capi-tpl"},
			Status:     kcmv1.ProviderTemplateStatus{TemplateStatusCommon: kcmv1.TemplateStatusCommon{TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: false}}},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(capiTpl).Build()

		oldObj := newTestManagement()
		newObj := newTestManagement()
		newObj.Spec.Core = &kcmv1.Core{CAPI: kcmv1.Component{Template: "new-capi-tpl"}}

		_, err := ValidateChangedProviderContracts(context.Background(), c, release, oldObj, newObj)
		if !errors.Is(err, ErrProviderIsNotReady) {
			t.Fatalf("err = %v, want ErrProviderIsNotReady", err)
		}
	})

	t.Run("capi template unchanged: validity of the (invalid) capi template is not checked", func(t *testing.T) {
		capiTpl := &kcmv1.ProviderTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "capi-tpl"},
			Status:     kcmv1.ProviderTemplateStatus{TemplateStatusCommon: kcmv1.TemplateStatusCommon{TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: false}}},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(capiTpl).Build()

		oldObj, newObj := newTestManagement(), newTestManagement()

		got, err := ValidateChangedProviderContracts(context.Background(), c, release, oldObj, newObj)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("unchanged provider template is not re-validated", func(t *testing.T) {
		capiTpl := &kcmv1.ProviderTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "capi-tpl"},
			Status:     kcmv1.ProviderTemplateStatus{TemplateStatusCommon: kcmv1.TemplateStatusCommon{TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: true}}},
		}
		// Deliberately no "aws-tpl" object in the client: if this were (re)validated,
		// the Get would fail and the test would catch it.
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(capiTpl).Build()

		provider := kcmv1.Provider{Name: "aws", Component: kcmv1.Component{Template: "aws-tpl"}}
		oldObj := newTestManagement(provider)
		newObj := newTestManagement(provider)

		got, err := ValidateChangedProviderContracts(context.Background(), c, release, oldObj, newObj)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("changed provider template is validated and missing: error", func(t *testing.T) {
		capiTpl := &kcmv1.ProviderTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "capi-tpl"},
			Status:     kcmv1.ProviderTemplateStatus{TemplateStatusCommon: kcmv1.TemplateStatusCommon{TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: true}}},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(capiTpl).Build()

		oldObj := newTestManagement(kcmv1.Provider{Name: "aws", Component: kcmv1.Component{Template: "aws-tpl-old"}})
		newObj := newTestManagement(kcmv1.Provider{Name: "aws", Component: kcmv1.Component{Template: "aws-tpl-new"}})

		_, err := ValidateChangedProviderContracts(context.Background(), c, release, oldObj, newObj)
		if err == nil || !strings.Contains(err.Error(), "failed to get ProviderTemplate aws-tpl-new") {
			t.Fatalf("err = %v, want get ProviderTemplate aws-tpl-new error", err)
		}
	})
}

func TestGetIncompatibleContractsForProviderTemplates(t *testing.T) {
	mgmt := newTestManagement()
	capiTpl := &kcmv1.ProviderTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "capi-tpl"},
		Status: kcmv1.ProviderTemplateStatus{
			CAPIContracts: kcmv1.CompatibilityContracts{"v1beta1": "v1beta1_v1beta2"},
		},
	}

	t.Run("template not found: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		_, err := getIncompatibleContractsForProviderTemplates(context.Background(), c, mgmt, capiTpl, []string{"missing-tpl"})
		if err == nil || !strings.Contains(err.Error(), "failed to get ProviderTemplate missing-tpl") {
			t.Fatalf("err = %v, want get ProviderTemplate error", err)
		}
	})

	t.Run("template with no CAPIContracts is skipped", func(t *testing.T) {
		pTpl := &kcmv1.ProviderTemplate{ObjectMeta: metav1.ObjectMeta{Name: "aws-tpl"}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(pTpl).Build()

		got, err := getIncompatibleContractsForProviderTemplates(context.Background(), c, mgmt, capiTpl, []string{"aws-tpl"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("template with contracts but not valid: ErrProviderIsNotReady", func(t *testing.T) {
		pTpl := &kcmv1.ProviderTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-tpl"},
			Status: kcmv1.ProviderTemplateStatus{
				CAPIContracts:        kcmv1.CompatibilityContracts{"v1beta1": "v1beta1"},
				TemplateStatusCommon: kcmv1.TemplateStatusCommon{TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: false}},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(pTpl).Build()

		_, err := getIncompatibleContractsForProviderTemplates(context.Background(), c, mgmt, capiTpl, []string{"aws-tpl"})
		if !errors.Is(err, ErrProviderIsNotReady) {
			t.Fatalf("err = %v, want ErrProviderIsNotReady", err)
		}
	})

	t.Run("capi contract does not support provider's required capi version: reported", func(t *testing.T) {
		pTpl := &kcmv1.ProviderTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-tpl"},
			Status: kcmv1.ProviderTemplateStatus{
				CAPIContracts:        kcmv1.CompatibilityContracts{"v1beta9": "v1beta1"},
				TemplateStatusCommon: kcmv1.TemplateStatusCommon{TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: true}},
			},
		}
		c := fake.NewClientBuilder().
			WithScheme(testscheme.Scheme).
			WithIndex(&kcmv1.ClusterTemplate{}, kcmv1.ClusterTemplateProvidersIndexKey, kcmv1.ExtractProvidersFromClusterTemplate).
			WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment).
			WithObjects(pTpl).
			Build()

		got, err := getIncompatibleContractsForProviderTemplates(context.Background(), c, mgmt, capiTpl, []string{"aws-tpl"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "core CAPI contract versions does not support v1beta9") {
			t.Errorf("got %q, want a core-CAPI-mismatch message", got)
		}
	})

	t.Run("in-use provider missing required contract: reported", func(t *testing.T) {
		pTpl := &kcmv1.ProviderTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-tpl"},
			Status: kcmv1.ProviderTemplateStatus{
				Providers:            kcmv1.Providers{"aws"},
				CAPIContracts:        kcmv1.CompatibilityContracts{"v1beta1": "v1beta1"},
				TemplateStatusCommon: kcmv1.TemplateStatusCommon{TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: true}},
			},
		}
		ct := &kcmv1.ClusterTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "ct1", Namespace: "ns1"},
			Status: kcmv1.ClusterTemplateStatus{
				Providers:         kcmv1.Providers{"aws"},
				ProviderContracts: kcmv1.CompatibilityContracts{"aws": "v1beta3"}, // not exposed by pTpl (only v1beta1)
			},
		}
		cd := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"},
			Spec:       kcmv1.ClusterDeploymentSpec{Template: "ct1"},
		}
		c := fake.NewClientBuilder().
			WithScheme(testscheme.Scheme).
			WithIndex(&kcmv1.ClusterTemplate{}, kcmv1.ClusterTemplateProvidersIndexKey, kcmv1.ExtractProvidersFromClusterTemplate).
			WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment).
			WithObjects(pTpl, ct, cd).
			Build()

		got, err := getIncompatibleContractsForProviderTemplates(context.Background(), c, mgmt, capiTpl, []string{"aws-tpl"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "missing contract version v1beta3 for aws provider") {
			t.Errorf("got %q, want a missing-contract message", got)
		}
	})
}

func TestManagementDeletionAllowed(t *testing.T) {
	t.Run("no Regions or ClusterDeployments: allowed", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		if err := ManagementDeletionAllowed(context.Background(), c); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("a Region exists: not allowed", func(t *testing.T) {
		rgn := &kcmv1.Region{ObjectMeta: metav1.ObjectMeta{Name: "region1"}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(rgn).Build()

		err := ManagementDeletionAllowed(context.Background(), c)
		if err == nil || !strings.Contains(err.Error(), "Region objects still exist") {
			t.Fatalf("err = %v, want Region-exists error", err)
		}
	})

	t.Run("a ClusterDeployment exists: not allowed", func(t *testing.T) {
		cd := &kcmv1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(cd).Build()

		err := ManagementDeletionAllowed(context.Background(), c)
		if err == nil || !strings.Contains(err.Error(), "ClusterDeployment objects still exist") {
			t.Fatalf("err = %v, want ClusterDeployment-exists error", err)
		}
	})
}
