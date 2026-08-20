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

package webhook

import (
	"testing"

	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	am "github.com/K0rdent/kcm/test/objects/accessmanagement"
	"github.com/K0rdent/kcm/test/objects/management"
	"github.com/K0rdent/kcm/test/scheme"
)

var widgetGVK = schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

func newTestRESTMapper() apimeta.RESTMapper {
	mapper := apimeta.NewDefaultRESTMapper([]schema.GroupVersion{
		widgetGVK.GroupVersion(),
		kcmv1.GroupVersion,
		{Group: "rbac.authorization.k8s.io", Version: "v1"},
	})
	mapper.Add(widgetGVK, apimeta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ClusterWidget"}, apimeta.RESTScopeRoot)
	mapper.Add(kcmv1.GroupVersion.WithKind(kcmv1.CredentialKind), apimeta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}, apimeta.RESTScopeRoot)
	return mapper
}

func TestAccessManagementValidateCreate(t *testing.T) {
	g := NewWithT(t)

	ctx := t.Context()

	tests := []struct {
		name            string
		am              *kcmv1.AccessManagement
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name:            "should fail if the AccessManagement object already exists",
			am:              am.NewAccessManagement(am.WithName("new")),
			existingObjects: []runtime.Object{am.NewAccessManagement(am.WithName(kcmv1.AccessManagementName))},
			err:             "AccessManagement object already exists",
		},
		{
			name: "should succeed",
			am:   am.NewAccessManagement(am.WithName("new")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment).
				Build()
			validator := &AccessManagementValidator{Client: c, SystemNamespace: kubeutil.DefaultSystemNamespace}
			warn, err := validator.ValidateCreate(ctx, tt.am)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				if err.Error() != tt.err {
					t.Fatalf("expected error '%s', got error: %s", tt.err, err.Error())
				}
			} else {
				g.Expect(err).To(Succeed())
			}
			if len(tt.warnings) > 0 {
				g.Expect(warn).To(Equal(tt.warnings))
			} else {
				g.Expect(warn).To(BeEmpty())
			}
		})
	}
}

func TestAccessManagementDefault(t *testing.T) {
	ctx := t.Context()

	validator := &AccessManagementValidator{}

	t.Run("migrates deprecated fields into Resources", func(t *testing.T) {
		g := NewWithT(t)
		obj := am.NewAccessManagement(am.WithAccessRules([]kcmv1.AccessRule{
			{Credentials: []string{"cred-1"}},
		}))

		g.Expect(validator.Default(ctx, obj)).To(Succeed())

		g.Expect(obj.Spec.AccessRules[0].Credentials).To(BeEmpty()) //nolint:staticcheck // SA1019: asserting the deprecated field was cleared by migration
		g.Expect(obj.Spec.AccessRules[0].Resources).To(Equal([]kcmv1.ResourceRule{
			{APIGroup: kcmv1.GroupVersion.Group, Kind: kcmv1.CredentialKind, Names: []string{"cred-1"}},
		}))
	})

	t.Run("is idempotent on an already-migrated object", func(t *testing.T) {
		g := NewWithT(t)
		obj := am.NewAccessManagement(am.WithAccessRules([]kcmv1.AccessRule{
			{Resources: []kcmv1.ResourceRule{{APIGroup: kcmv1.GroupVersion.Group, Kind: kcmv1.CredentialKind, Names: []string{"cred-1"}}}},
		}))
		before := obj.DeepCopy()

		g.Expect(validator.Default(ctx, obj)).To(Succeed())
		g.Expect(obj.Spec.AccessRules).To(Equal(before.Spec.AccessRules))
	})

	t.Run("no rules is a no-op", func(t *testing.T) {
		g := NewWithT(t)
		obj := am.NewAccessManagement()

		g.Expect(validator.Default(ctx, obj)).To(Succeed())
		g.Expect(obj.Spec.AccessRules).To(BeEmpty())
	})
}

func TestAccessManagementValidateResources(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name string
		res  kcmv1.ResourceRule
		err  string
	}{
		{
			name: "a custom namespaced CRD is permitted",
			res:  kcmv1.ResourceRule{APIGroup: "example.com", Kind: "Widget", Names: []string{"w1"}},
		},
		{
			name: "a built-in Kind defaulting APIGroup is permitted",
			res:  kcmv1.ResourceRule{Kind: kcmv1.CredentialKind, Names: []string{"c1"}},
		},
		{
			name: "ClusterRole is rejected",
			res:  kcmv1.ResourceRule{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Names: []string{"cr1"}},
			err:  "cluster-scoped",
		},
		{
			name: "a cluster-scoped custom Kind is rejected",
			res:  kcmv1.ResourceRule{APIGroup: "example.com", Kind: "ClusterWidget", Names: []string{"cw1"}},
			err:  "cluster-scoped",
		},
		{
			name: "an unresolvable Kind is rejected",
			res:  kcmv1.ResourceRule{APIGroup: "example.com", Kind: "DoesNotExist", Names: []string{"x1"}},
			err:  "failed to resolve",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
			validator := &AccessManagementValidator{Client: c, RESTMapper: newTestRESTMapper()}

			obj := am.NewAccessManagement(am.WithName("new"), am.WithAccessRules([]kcmv1.AccessRule{
				{Resources: []kcmv1.ResourceRule{tt.res}},
			}))

			_, createErr := validator.ValidateCreate(ctx, obj)
			_, updateErr := validator.ValidateUpdate(ctx, obj, obj)

			if tt.err == "" {
				g.Expect(createErr).NotTo(HaveOccurred())
				g.Expect(updateErr).NotTo(HaveOccurred())
				return
			}

			g.Expect(createErr).To(HaveOccurred())
			g.Expect(createErr.Error()).To(ContainSubstring(tt.err))
			g.Expect(updateErr).To(HaveOccurred())
			g.Expect(updateErr.Error()).To(ContainSubstring(tt.err))
		})
	}

	t.Run("deprecated-field-only rules need no resolution and are always accepted", func(t *testing.T) {
		g := NewWithT(t)

		c := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
		validator := &AccessManagementValidator{Client: c} // no RESTMapper wired

		obj := am.NewAccessManagement(am.WithName("new"), am.WithAccessRules([]kcmv1.AccessRule{
			{Credentials: []string{"cred-1"}},
		}))

		_, err := validator.ValidateCreate(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
	})
}

func TestAccessManagementValidateDelete(t *testing.T) {
	g := NewWithT(t)

	ctx := t.Context()

	amName := "test"

	tests := []struct {
		name            string
		am              *kcmv1.AccessManagement
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name:            "should fail if Management object exists and was not deleted",
			am:              am.NewAccessManagement(am.WithName(amName)),
			existingObjects: []runtime.Object{management.NewManagement()},
			err:             "AccessManagement deletion is forbidden",
		},
		{
			name: "should succeed if Management object is not found",
			am:   am.NewAccessManagement(am.WithName(amName)),
		},
		{
			name:            "should succeed if Management object was deleted",
			am:              am.NewAccessManagement(am.WithName(amName)),
			existingObjects: []runtime.Object{management.NewManagement(management.WithDeletionTimestamp(metav1.Now()))},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment).
				Build()
			validator := &AccessManagementValidator{Client: c, SystemNamespace: kubeutil.DefaultSystemNamespace}
			warn, err := validator.ValidateDelete(ctx, tt.am)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				if err.Error() != tt.err {
					t.Fatalf("expected error '%s', got error: %s", tt.err, err.Error())
				}
			} else {
				g.Expect(err).To(Succeed())
			}
			if len(tt.warnings) > 0 {
				g.Expect(warn).To(Equal(tt.warnings))
			} else {
				g.Expect(warn).To(BeEmpty())
			}
		})
	}
}
