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

func validServiceTemplateStatus() kcmv1.ServiceTemplateStatus {
	return kcmv1.ServiceTemplateStatus{
		TemplateStatusCommon: kcmv1.TemplateStatusCommon{
			TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: true},
		},
	}
}

func TestServicesHaveValidTemplates(t *testing.T) {
	t.Run("no services: valid", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		if err := ServicesHaveValidTemplates(context.Background(), c, nil, "ns1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid namespace/name in the service entry: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()
		svcTpl := &kcmv1.ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-tpl", Namespace: "ns1"},
			Status:     validServiceTemplateStatus(),
		}
		c = fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(svcTpl).Build()

		services := []kcmv1.Service{{Name: "Invalid Name!", Namespace: "Invalid Namespace!", Template: "svc-tpl"}}
		err := ServicesHaveValidTemplates(context.Background(), c, services, "ns1")
		if err == nil || !strings.Contains(err.Error(), "some services have invalid templates") {
			t.Fatalf("err = %v, want invalid-templates error", err)
		}
	})

	t.Run("ServiceTemplate not found: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		services := []kcmv1.Service{{Name: "svc1", Template: "missing-tpl"}}
		err := ServicesHaveValidTemplates(context.Background(), c, services, "ns1")
		if err == nil || !strings.Contains(err.Error(), "failed to get ServiceTemplate") {
			t.Fatalf("err = %v, want get ServiceTemplate error", err)
		}
	})

	t.Run("ServiceTemplate invalid: error", func(t *testing.T) {
		svcTpl := &kcmv1.ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-tpl", Namespace: "ns1"},
			Status: kcmv1.ServiceTemplateStatus{
				TemplateStatusCommon: kcmv1.TemplateStatusCommon{
					TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: false, ValidationError: "bad chart"},
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(svcTpl).Build()

		services := []kcmv1.Service{{Name: "svc1", Template: "svc-tpl"}}
		err := ServicesHaveValidTemplates(context.Background(), c, services, "ns1")
		if err == nil || !strings.Contains(err.Error(), "bad chart") {
			t.Fatalf("err = %v, want validation-error message", err)
		}
	})

	t.Run("valid ServiceTemplate, no TemplateChain: valid", func(t *testing.T) {
		svcTpl := &kcmv1.ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-tpl", Namespace: "ns1"},
			Status:     validServiceTemplateStatus(),
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(svcTpl).Build()

		services := []kcmv1.Service{{Name: "svc1", Template: "svc-tpl"}}
		if err := ServicesHaveValidTemplates(context.Background(), c, services, "ns1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("TemplateChain not found: error", func(t *testing.T) {
		svcTpl := &kcmv1.ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-tpl", Namespace: "ns1"},
			Status:     validServiceTemplateStatus(),
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(svcTpl).Build()

		services := []kcmv1.Service{{Name: "svc1", Template: "svc-tpl", TemplateChain: "missing-chain"}}
		err := ServicesHaveValidTemplates(context.Background(), c, services, "ns1")
		if err == nil || !strings.Contains(err.Error(), "failed to get ServiceTemplateChain") {
			t.Fatalf("err = %v, want get ServiceTemplateChain error", err)
		}
	})

	t.Run("TemplateChain invalid: error", func(t *testing.T) {
		svcTpl := &kcmv1.ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-tpl", Namespace: "ns1"},
			Status:     validServiceTemplateStatus(),
		}
		chain := &kcmv1.ServiceTemplateChain{
			ObjectMeta: metav1.ObjectMeta{Name: "chain1", Namespace: "ns1"},
			Status:     kcmv1.TemplateChainStatus{Valid: false, ValidationError: "broken chain"},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(svcTpl, chain).Build()

		services := []kcmv1.Service{{Name: "svc1", Template: "svc-tpl", TemplateChain: "chain1"}}
		err := ServicesHaveValidTemplates(context.Background(), c, services, "ns1")
		if err == nil || !strings.Contains(err.Error(), "broken chain") {
			t.Fatalf("err = %v, want broken-chain message", err)
		}
	})

	t.Run("TemplateChain valid but does not support the requested template: error", func(t *testing.T) {
		svcTpl := &kcmv1.ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-tpl", Namespace: "ns1"},
			Status:     validServiceTemplateStatus(),
		}
		chain := &kcmv1.ServiceTemplateChain{
			ObjectMeta: metav1.ObjectMeta{Name: "chain1", Namespace: "ns1"},
			Status:     kcmv1.TemplateChainStatus{Valid: true},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(svcTpl, chain).Build()

		services := []kcmv1.Service{{Name: "svc1", Template: "svc-tpl", TemplateChain: "chain1"}}
		err := ServicesHaveValidTemplates(context.Background(), c, services, "ns1")
		if err == nil || !strings.Contains(err.Error(), "does not support ServiceTemplate") {
			t.Fatalf("err = %v, want does-not-support message", err)
		}
	})

	t.Run("TemplateChain valid, supports the template, template found and valid: valid", func(t *testing.T) {
		svcTpl := &kcmv1.ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "svc-tpl", Namespace: "ns1"},
			Status:     validServiceTemplateStatus(),
		}
		chain := &kcmv1.ServiceTemplateChain{
			ObjectMeta: metav1.ObjectMeta{Name: "chain1", Namespace: "ns1"},
			Spec:       kcmv1.TemplateChainSpec{SupportedTemplates: []kcmv1.SupportedTemplate{{Name: "svc-tpl"}}},
			Status:     kcmv1.TemplateChainStatus{Valid: true},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(svcTpl, chain).Build()

		services := []kcmv1.Service{{Name: "svc1", Template: "svc-tpl", TemplateChain: "chain1"}}
		if err := ServicesHaveValidTemplates(context.Background(), c, services, "ns1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("TemplateChain supports the template but the ServiceTemplate itself is missing: error", func(t *testing.T) {
		chain := &kcmv1.ServiceTemplateChain{
			ObjectMeta: metav1.ObjectMeta{Name: "chain1", Namespace: "ns1"},
			Spec:       kcmv1.TemplateChainSpec{SupportedTemplates: []kcmv1.SupportedTemplate{{Name: "missing-svc-tpl"}}},
			Status:     kcmv1.TemplateChainStatus{Valid: true},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(chain).Build()

		services := []kcmv1.Service{{Name: "svc1", Template: "missing-svc-tpl", TemplateChain: "chain1"}}
		err := ServicesHaveValidTemplates(context.Background(), c, services, "ns1")
		if err == nil || !strings.Contains(err.Error(), "failed to get ServiceTemplate ns1/missing-svc-tpl") {
			t.Fatalf("err = %v, want get ServiceTemplate error", err)
		}
	})
}

func TestValidateServiceDependencyOverall(t *testing.T) {
	t.Run("no services: valid", func(t *testing.T) {
		if err := ValidateServiceDependencyOverall(nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("dependency not defined: error", func(t *testing.T) {
		services := []kcmv1.Service{
			{Name: "svc1", DependsOn: []kcmv1.ServiceDependsOn{{Name: "missing"}}},
		}
		err := ValidateServiceDependencyOverall(services)
		if err == nil || !strings.Contains(err.Error(), "failed service dependency validation") {
			t.Fatalf("err = %v, want dependency validation error", err)
		}
	})

	t.Run("dependency cycle: error", func(t *testing.T) {
		services := []kcmv1.Service{
			{Name: "svc1", DependsOn: []kcmv1.ServiceDependsOn{{Name: "svc2"}}},
			{Name: "svc2", DependsOn: []kcmv1.ServiceDependsOn{{Name: "svc1"}}},
		}
		err := ValidateServiceDependencyOverall(services)
		if err == nil || !strings.Contains(err.Error(), "failed service dependency cycle validation") {
			t.Fatalf("err = %v, want dependency cycle validation error", err)
		}
	})

	t.Run("valid dependency chain: no error", func(t *testing.T) {
		services := []kcmv1.Service{
			{Name: "svc1", DependsOn: []kcmv1.ServiceDependsOn{{Name: "svc2"}}},
			{Name: "svc2"},
		}
		if err := ValidateServiceDependencyOverall(services); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
