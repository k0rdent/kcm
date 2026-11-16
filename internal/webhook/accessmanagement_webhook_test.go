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
	"slices"
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
	"github.com/K0rdent/kcm/test/objects/clusterdeployment"
	"github.com/K0rdent/kcm/test/objects/management"
	"github.com/K0rdent/kcm/test/objects/template"
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

func TestAccessManagementWarnsOnSystemNamespaceTarget(t *testing.T) {
	ctx := t.Context()

	const systemNamespace = "kcm-system"

	tests := []struct {
		name     string
		rules    []kcmv1.AccessRule
		warnings admission.Warnings
	}{
		{
			name:  "no rules yield no warning",
			rules: nil,
		},
		{
			name: "a list without the system namespace yields no warning",
			rules: []kcmv1.AccessRule{
				{TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"team-a"}}},
			},
		},
		{
			name: "an empty list yields no warning",
			rules: []kcmv1.AccessRule{
				{TargetNamespaces: kcmv1.TargetNamespaces{StringSelector: "kcm=true"}},
			},
		},
		{
			name: "the system namespace in the list is warned about",
			rules: []kcmv1.AccessRule{
				{TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"team-a", systemNamespace}}},
			},
			warnings: admission.Warnings{
				"accessRules[0].targetNamespaces.list: kcm-system is the KCM system namespace and will be skipped; objects are never distributed into the namespace they are read from",
			},
		},
		{
			name: "every offending rule is warned about, by index",
			rules: []kcmv1.AccessRule{
				{TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"team-a"}}},
				{TargetNamespaces: kcmv1.TargetNamespaces{List: []string{systemNamespace}}},
				{TargetNamespaces: kcmv1.TargetNamespaces{List: []string{systemNamespace, "team-b"}}},
			},
			warnings: admission.Warnings{
				"accessRules[1].targetNamespaces.list: kcm-system is the KCM system namespace and will be skipped; objects are never distributed into the namespace they are read from",
				"accessRules[2].targetNamespaces.list: kcm-system is the KCM system namespace and will be skipped; objects are never distributed into the namespace they are read from",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
			validator := &AccessManagementValidator{Client: c, SystemNamespace: systemNamespace}

			obj := am.NewAccessManagement(am.WithName(kcmv1.AccessManagementName), am.WithAccessRules(tt.rules))

			createWarn, err := validator.ValidateCreate(ctx, obj)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(createWarn).To(Equal(tt.warnings))

			updateWarn, err := validator.ValidateUpdate(ctx, obj, obj)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(updateWarn).To(Equal(tt.warnings))
		})
	}
}

func TestAccessManagementValidateClusterTemplateRemovals(t *testing.T) {
	const systemNamespace = "kcm-system"

	amOwnerRef := metav1.OwnerReference{
		APIVersion: kcmv1.GroupVersion.String(),
		Kind:       kcmv1.AccessManagementKind,
		Name:       kcmv1.AccessManagementName,
		UID:        "access-management-uid",
	}
	chainOwnerRef := metav1.OwnerReference{
		APIVersion: kcmv1.GroupVersion.String(),
		Kind:       kcmv1.ClusterTemplateChainKind,
		Name:       "chain",
		UID:        "chain-uid",
	}
	distributedCopy := func(namespace, name string, owners ...metav1.OwnerReference) *kcmv1.ClusterTemplate {
		ct := template.NewClusterTemplate(template.WithName(name), template.WithNamespace(namespace), template.WithOwnerReference(owners))
		ct.Labels = map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue}
		return ct
	}
	usedBy := func(namespace, templateName string) *kcmv1.ClusterDeployment {
		return clusterdeployment.NewClusterDeployment(
			clusterdeployment.WithName("cd-"+namespace+"-"+templateName),
			clusterdeployment.WithNamespace(namespace),
			clusterdeployment.WithClusterTemplate(templateName),
		)
	}
	templateRule := func(namespaces []string, templates ...string) kcmv1.AccessRule {
		return kcmv1.AccessRule{
			TargetNamespaces: kcmv1.TargetNamespaces{List: namespaces},
			Resources:        []kcmv1.ResourceRule{{APIGroup: kcmv1.GroupVersion.Group, Kind: kcmv1.ClusterTemplateKind, Names: templates}},
		}
	}
	credentialRule := kcmv1.AccessRule{
		TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"team-a"}},
		Resources:        []kcmv1.ResourceRule{{APIGroup: kcmv1.GroupVersion.Group, Kind: kcmv1.CredentialKind, Names: []string{"cred"}}},
	}

	// what the controller has distributed so far, per oldAM
	oldAM := am.NewAccessManagement(
		am.WithName(kcmv1.AccessManagementName),
		am.WithAccessRules([]kcmv1.AccessRule{templateRule([]string{"team-a", "team-b"}, "ct-1", "ct-2")}),
	)
	oldAM.Status.Resources = []kcmv1.ResourceKindStatus{{APIGroup: kcmv1.GroupVersion.Group, Kind: kcmv1.ClusterTemplateKind}}
	distributed := []runtime.Object{
		template.NewClusterTemplate(template.WithName("ct-1"), template.WithNamespace(systemNamespace)),
		template.NewClusterTemplate(template.WithName("ct-2"), template.WithNamespace(systemNamespace)),
		distributedCopy("team-a", "ct-1", amOwnerRef),
		distributedCopy("team-b", "ct-1", amOwnerRef),
		distributedCopy("team-a", "ct-2", chainOwnerRef, amOwnerRef),
		distributedCopy("team-b", "ct-2", amOwnerRef),
	}

	const inUseWarning = "team-a/ct-1: The ClusterTemplate object can't be removed if ClusterDeployment objects referencing it still exist"

	oldWith := func(rules ...kcmv1.AccessRule) *kcmv1.AccessManagement {
		old := oldAM.DeepCopy()
		old.Spec.AccessRules = rules
		return old
	}

	tests := []struct {
		name     string
		old      *kcmv1.AccessManagement
		rules    []kcmv1.AccessRule
		existing []runtime.Object
		err      string
		warnings admission.Warnings
	}{
		{
			name:     "removing a template no ClusterDeployment uses is accepted",
			rules:    []kcmv1.AccessRule{templateRule([]string{"team-a", "team-b"}, "ct-2")},
			existing: []runtime.Object{usedBy("team-a", "ct-2")},
		},
		{
			name:     "removing a template a ClusterDeployment uses is rejected",
			rules:    []kcmv1.AccessRule{templateRule([]string{"team-a", "team-b"}, "ct-2")},
			existing: []runtime.Object{usedBy("team-a", "ct-1")},
			err:      "the update would remove the ClusterTemplate team-a/ct-1: template deletion is forbidden",
			warnings: admission.Warnings{inUseWarning},
		},
		{
			name:     "dropping the namespace of a copy in use is rejected",
			rules:    []kcmv1.AccessRule{templateRule([]string{"team-b"}, "ct-1", "ct-2")},
			existing: []runtime.Object{usedBy("team-a", "ct-1")},
			err:      "the update would remove the ClusterTemplate team-a/ct-1: template deletion is forbidden",
			warnings: admission.Warnings{inUseWarning},
		},
		{
			name:     "dropping ClusterTemplates altogether while a copy is in use is rejected",
			rules:    []kcmv1.AccessRule{credentialRule},
			existing: []runtime.Object{usedBy("team-a", "ct-1")},
			err:      "the update would remove the ClusterTemplate team-a/ct-1: template deletion is forbidden",
			warnings: admission.Warnings{inUseWarning},
		},
		{
			name:  "every copy in use is reported",
			rules: []kcmv1.AccessRule{credentialRule},
			existing: []runtime.Object{
				usedBy("team-a", "ct-1"),
				usedBy("team-b", "ct-2"),
			},
			err: "the update would remove the ClusterTemplate team-a/ct-1: template deletion is forbidden\n" +
				"the update would remove the ClusterTemplate team-b/ct-2: template deletion is forbidden",
			warnings: admission.Warnings{
				inUseWarning,
				"team-b/ct-2: The ClusterTemplate object can't be removed if ClusterDeployment objects referencing it still exist",
			},
		},
		{
			name:     "a copy something else owns too is only released, so it may be in use",
			rules:    []kcmv1.AccessRule{templateRule([]string{"team-a", "team-b"}, "ct-1")},
			existing: []runtime.Object{usedBy("team-a", "ct-2")},
		},
		{
			name:     "a removal already pending before the update is not blamed on it",
			old:      oldWith(templateRule([]string{"team-b"}, "ct-1", "ct-2")),
			rules:    []kcmv1.AccessRule{templateRule([]string{"team-b"}, "ct-1", "ct-2"), credentialRule},
			existing: []runtime.Object{usedBy("team-a", "ct-1")},
		},
		{
			name: "every removal is validated when the stored spec cannot be planned",
			old: oldWith(kcmv1.AccessRule{
				TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"team-a", "team-b"}},
				Resources:        []kcmv1.ResourceRule{{APIGroup: kcmv1.GroupVersion.Group, Kind: kcmv1.ClusterTemplateKind, StringSelector: "tier in (prod"}},
			}),
			rules:    []kcmv1.AccessRule{templateRule([]string{"team-a", "team-b"}, "ct-2")},
			existing: []runtime.Object{usedBy("team-a", "ct-1")},
			err:      "the update would remove the ClusterTemplate team-a/ct-1: template deletion is forbidden",
			warnings: admission.Warnings{inUseWarning},
		},
		{
			name: "an update leaving the rules as they are is accepted even if they cannot be planned",
			old: oldWith(kcmv1.AccessRule{
				TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"team-a", "team-b"}},
				Resources:        []kcmv1.ResourceRule{{APIGroup: kcmv1.GroupVersion.Group, Kind: kcmv1.ClusterTemplateKind, StringSelector: "tier in (prod"}},
			}),
			rules: []kcmv1.AccessRule{{
				TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"team-a", "team-b"}},
				Resources:        []kcmv1.ResourceRule{{APIGroup: kcmv1.GroupVersion.Group, Kind: kcmv1.ClusterTemplateKind, StringSelector: "tier in (prod"}},
			}},
			existing: []runtime.Object{usedBy("team-a", "ct-1")},
		},
		{
			name:     "keeping every copy is accepted while they are in use",
			rules:    []kcmv1.AccessRule{templateRule([]string{"team-a", "team-b"}, "ct-1", "ct-2")},
			existing: []runtime.Object{usedBy("team-a", "ct-1"), usedBy("team-b", "ct-2")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(append(slices.Clone(distributed), tt.existing...)...).
				WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment).
				Build()
			validator := &AccessManagementValidator{Client: c, SystemNamespace: systemNamespace}

			// the update carries no status: what the controller still tracks comes from the stored object
			newAM := am.NewAccessManagement(am.WithName(kcmv1.AccessManagementName), am.WithAccessRules(tt.rules))

			old := oldAM
			if tt.old != nil {
				old = tt.old
			}

			warnings, err := validator.ValidateUpdate(t.Context(), old, newAM)
			if tt.err != "" {
				g.Expect(err).To(MatchError(tt.err))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			g.Expect(warnings).To(Equal(tt.warnings))
		})
	}
}

func TestAccessManagementValidateClusterTemplateRemovalsCheckFailure(t *testing.T) {
	g := NewWithT(t)

	const systemNamespace = "kcm-system"

	removed := template.NewClusterTemplate(
		template.WithName("ct-1"),
		template.WithNamespace("team-a"),
		template.WithOwnerReference([]metav1.OwnerReference{{
			APIVersion: kcmv1.GroupVersion.String(),
			Kind:       kcmv1.AccessManagementKind,
			Name:       kcmv1.AccessManagementName,
			UID:        "access-management-uid",
		}}),
	)
	removed.Labels = map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue}

	oldAM := am.NewAccessManagement(
		am.WithName(kcmv1.AccessManagementName),
		am.WithAccessRules([]kcmv1.AccessRule{{
			TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"team-a"}},
			Resources:        []kcmv1.ResourceRule{{APIGroup: kcmv1.GroupVersion.Group, Kind: kcmv1.ClusterTemplateKind, Names: []string{"ct-1"}}},
		}}),
	)
	oldAM.Status.Resources = []kcmv1.ResourceKindStatus{{APIGroup: kcmv1.GroupVersion.Group, Kind: kcmv1.ClusterTemplateKind}}
	newAM := am.NewAccessManagement(am.WithName(kcmv1.AccessManagementName))

	// without the ClusterDeployment index the in-use lookup of the ClusterTemplate check fails
	c := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithRuntimeObjects(template.NewClusterTemplate(template.WithName("ct-1"), template.WithNamespace(systemNamespace)), removed).
		Build()
	validator := &AccessManagementValidator{Client: c, SystemNamespace: systemNamespace}

	warnings, err := validator.ValidateUpdate(t.Context(), oldAM, newAM)
	g.Expect(err).To(MatchError(ContainSubstring("failed to check whether the ClusterTemplate team-a/ct-1 can be removed")))
	g.Expect(err).NotTo(MatchError(errTemplateDeletionForbidden), "a failed check is not a verdict on the removal")
	g.Expect(warnings).To(BeEmpty())
}

func Test_accessRulesUnchanged(t *testing.T) {
	g := NewWithT(t)

	deprecated := am.NewAccessManagement(am.WithAccessRules([]kcmv1.AccessRule{{
		TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"team-a"}},
		Credentials:      []string{"cred"},
	}}))
	migrated := am.NewAccessManagement(am.WithAccessRules([]kcmv1.AccessRule{{
		TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"team-a"}},
		Resources:        []kcmv1.ResourceRule{{Kind: kcmv1.CredentialKind, Names: []string{"cred"}}},
	}}))
	g.Expect(accessRulesUnchanged(deprecated, migrated)).To(BeTrue(), "rules equal once migrated and defaulted are unchanged")
	g.Expect(deprecated.Spec.AccessRules[0].Credentials).To(Equal([]string{"cred"}), "the objects compared are left as they are") //nolint:staticcheck // SA1019: asserting the deprecated field was not migrated in place

	changed := migrated.DeepCopy()
	changed.Spec.AccessRules[0].TargetNamespaces.List = []string{"team-b"}
	g.Expect(accessRulesUnchanged(migrated, changed)).To(BeFalse())

	g.Expect(accessRulesUnchanged(am.NewAccessManagement(), am.NewAccessManagement(am.WithAccessRules([]kcmv1.AccessRule{})))).To(BeTrue(), "no rules and an empty list of rules are the same")
}
