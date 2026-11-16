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

package accessmanagement

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	am "github.com/K0rdent/kcm/test/objects/accessmanagement"
	"github.com/K0rdent/kcm/test/objects/template"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

const (
	testSystemNamespace = "kcm-system"
	testTargetNamespace = "team-a"
)

var clusterTemplateGK = kcmv1.GroupVersion.WithKind(kcmv1.ClusterTemplateKind).GroupKind()

func newTestAccessManagement(rules ...kcmv1.AccessRule) *kcmv1.AccessManagement {
	return am.NewAccessManagement(am.WithName(kcmv1.AccessManagementName), am.WithAccessRules(rules))
}

func accessManagementOwnerReference() metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: kcmv1.GroupVersion.String(),
		Kind:       kcmv1.AccessManagementKind,
		Name:       kcmv1.AccessManagementName,
		UID:        "access-management-uid",
	}
}

func chainOwnerReference() metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: kcmv1.GroupVersion.String(),
		Kind:       kcmv1.ClusterTemplateChainKind,
		Name:       "chain",
		UID:        "chain-uid",
	}
}

func newManagedCopy(namespace, name string, owners ...metav1.OwnerReference) *metav1.PartialObjectMetadata {
	return &metav1.PartialObjectMetadata{
		Namespace:       namespace,
		Name:            name,
		Labels:          map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue},
		OwnerReferences: owners,
	}
}

func newSystemTemplate(name string, labels map[string]string) *kcmv1.ClusterTemplate {
	ct := template.NewClusterTemplate(template.WithName(name), template.WithNamespace(testSystemNamespace))
	ct.Labels = labels
	return ct
}

func names[M metav1.Object](objs []M) []string {
	result := make([]string, 0, len(objs))
	for _, obj := range objs {
		result = append(result, NamespacedName(obj.GetNamespace(), obj.GetName()))
	}
	return result
}

func TestPlanGroupKind(t *testing.T) {
	system := map[string]*kcmv1.ClusterTemplate{
		"ct-1": newSystemTemplate("ct-1", map[string]string{"tier": "prod"}),
		"ct-2": newSystemTemplate("ct-2", map[string]string{"tier": "dev"}),
	}
	accessMgmt := newTestAccessManagement()

	tests := []struct {
		name        string
		rules       []Rule
		managed     []*metav1.PartialObjectMetadata
		wantDesired []Target
		wantRemove  []string
		wantRelease []string
		wantErr     string
	}{
		{
			name: "copies are desired per namespace and name, in rule order, once",
			rules: []Rule{
				{
					Namespaces: []string{"team-a", "team-b"},
					Resources:  []kcmv1.ResourceRule{{Kind: kcmv1.ClusterTemplateKind, Names: []string{"ct-2", "ct-1"}}},
				},
				{
					Namespaces: []string{"team-b", "team-c"},
					Resources: []kcmv1.ResourceRule{
						{Kind: kcmv1.ClusterTemplateKind, StringSelector: "tier=dev"},
						{Kind: kcmv1.CredentialKind, Names: []string{"cred"}},
					},
				},
			},
			wantDesired: []Target{
				{Namespace: "team-a", Name: "ct-2"},
				{Namespace: "team-a", Name: "ct-1"},
				{Namespace: "team-b", Name: "ct-2"},
				{Namespace: "team-b", Name: "ct-1"},
				{Namespace: "team-c", Name: "ct-2"},
			},
		},
		{
			name: "a name missing from the system namespace is still desired, so its copy is kept",
			rules: []Rule{{
				Namespaces: []string{"team-a"},
				Resources:  []kcmv1.ResourceRule{{Kind: kcmv1.ClusterTemplateKind, Names: []string{"missing"}}},
			}},
			managed:     []*metav1.PartialObjectMetadata{newManagedCopy("team-a", "missing", accessManagementOwnerReference())},
			wantDesired: []Target{{Namespace: "team-a", Name: "missing"}},
		},
		{
			name: "copies no rule distributes are removed unless something else owns them too",
			rules: []Rule{{
				Namespaces: []string{"team-a"},
				Resources:  []kcmv1.ResourceRule{{Kind: kcmv1.ClusterTemplateKind, Names: []string{"ct-1"}}},
			}},
			managed: []*metav1.PartialObjectMetadata{
				newManagedCopy("team-a", "ct-1", accessManagementOwnerReference()),
				newManagedCopy("team-b", "ct-1", accessManagementOwnerReference()),
				newManagedCopy("team-a", "ct-2"),
				newManagedCopy("team-a", "ct-3", chainOwnerReference(), accessManagementOwnerReference()),
				newManagedCopy("team-b", "ct-3", chainOwnerReference()),
			},
			wantDesired: []Target{{Namespace: "team-a", Name: "ct-1"}},
			wantRemove:  []string{"team-b/ct-1", "team-a/ct-2"},
			wantRelease: []string{"team-a/ct-3", "team-b/ct-3"},
		},
		{
			name:    "every copy of a Kind no rule references is removed",
			rules:   []Rule{{Namespaces: []string{"team-a"}}},
			managed: []*metav1.PartialObjectMetadata{newManagedCopy("team-a", "ct-1", accessManagementOwnerReference())},
			wantRemove: []string{
				"team-a/ct-1",
			},
		},
		{
			name: "an invalid selector fails the plan",
			rules: []Rule{{
				Namespaces: []string{"team-a"},
				Resources:  []kcmv1.ResourceRule{{Kind: kcmv1.ClusterTemplateKind, StringSelector: "tier in (prod"}},
			}},
			managed: []*metav1.PartialObjectMetadata{newManagedCopy("team-a", "ct-1", accessManagementOwnerReference())},
			wantErr: "failed to resolve names",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			plan, err := PlanGroupKind(tt.rules, clusterTemplateGK, system, tt.managed, accessMgmt)
			if tt.wantErr != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tt.wantErr)))
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(plan.Desired).To(Equal(tt.wantDesired))
			g.Expect(names(plan.Remove)).To(Equal(append([]string{}, tt.wantRemove...)))
			g.Expect(names(plan.Release)).To(Equal(append([]string{}, tt.wantRelease...)))
		})
	}
}

func TestTracksGroupKind(t *testing.T) {
	g := NewWithT(t)

	referencing := newTestAccessManagement(kcmv1.AccessRule{
		Resources: []kcmv1.ResourceRule{{Kind: kcmv1.ClusterTemplateKind, Names: []string{"ct-1"}}},
	})
	g.Expect(TracksGroupKind(referencing, clusterTemplateGK)).To(BeTrue(), "a Kind a rule references is tracked")

	stale := newTestAccessManagement()
	stale.Status.Resources = []kcmv1.ResourceKindStatus{{APIGroup: kcmv1.GroupVersion.Group, Kind: kcmv1.ClusterTemplateKind}}
	g.Expect(TracksGroupKind(stale, clusterTemplateGK)).To(BeTrue(), "a Kind still recorded in status is tracked until its copies are cleaned up")

	g.Expect(TracksGroupKind(newTestAccessManagement(), clusterTemplateGK)).To(BeFalse())
}

func TestClusterTemplatesToRemove(t *testing.T) {
	ownedCopy := func(namespace, name string, owners ...metav1.OwnerReference) *kcmv1.ClusterTemplate {
		ct := template.NewClusterTemplate(template.WithName(name), template.WithNamespace(namespace), template.WithOwnerReference(owners))
		ct.Labels = map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue}
		return ct
	}
	rule := func(namespaces []string, templates ...string) kcmv1.AccessRule {
		return kcmv1.AccessRule{
			TargetNamespaces: kcmv1.TargetNamespaces{List: namespaces},
			Resources:        []kcmv1.ResourceRule{{Kind: kcmv1.ClusterTemplateKind, Names: templates}},
		}
	}
	// already being deleted, so never one to remove whatever the rules say
	terminatingCopy := ownedCopy("team-c", "ct-1", accessManagementOwnerReference())
	terminatingCopy.Finalizers = []string{"test/finalizer"}
	terminatingCopy.DeletionTimestamp = &metav1.Time{Time: time.Now()}

	existing := []client.Object{
		newSystemTemplate("ct-1", nil),
		newSystemTemplate("ct-2", nil),
		ownedCopy("team-a", "ct-1", accessManagementOwnerReference()),
		ownedCopy("team-b", "ct-1", accessManagementOwnerReference()),
		ownedCopy("team-a", "ct-2", chainOwnerReference(), accessManagementOwnerReference()),
		terminatingCopy,
	}

	tests := []struct {
		name       string
		accessMgmt *kcmv1.AccessManagement
		want       []string
	}{
		{
			name:       "nothing is removed while the rules still distribute every copy",
			accessMgmt: newTestAccessManagement(rule([]string{"team-a", "team-b"}, "ct-1", "ct-2")),
		},
		{
			name:       "a template dropped from the rules is removed from every namespace",
			accessMgmt: newTestAccessManagement(rule([]string{"team-a", "team-b"}, "ct-2")),
			want:       []string{"team-a/ct-1", "team-b/ct-1"},
		},
		{
			name:       "a namespace dropped from the targets loses its copies",
			accessMgmt: newTestAccessManagement(rule([]string{"team-b"}, "ct-1", "ct-2")),
			want:       []string{"team-a/ct-1"},
		},
		{
			name: "every copy goes once the Kind is dropped, as long as status still tracks it",
			accessMgmt: func() *kcmv1.AccessManagement {
				accessMgmt := newTestAccessManagement(kcmv1.AccessRule{
					TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"team-a"}},
					Resources:        []kcmv1.ResourceRule{{Kind: kcmv1.CredentialKind, Names: []string{"cred"}}},
				})
				accessMgmt.Status.Resources = []kcmv1.ResourceKindStatus{{APIGroup: kcmv1.GroupVersion.Group, Kind: kcmv1.ClusterTemplateKind}}
				return accessMgmt
			}(),
			want: []string{"team-a/ct-1", "team-b/ct-1"},
		},
		{
			name:       "nothing is removed for a Kind the AccessManagement does not track",
			accessMgmt: newTestAccessManagement(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(existing...).Build()

			toRemove, err := ClusterTemplatesToRemove(t.Context(), c, testSystemNamespace, tt.accessMgmt)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(names(toRemove)).To(ConsistOf(tt.want))
		})
	}
}

func TestResolveNames(t *testing.T) {
	system := map[string]*kcmv1.ClusterTemplate{
		"a": newSystemTemplate("a", map[string]string{"tier": "prod"}),
		"b": newSystemTemplate("b", map[string]string{"tier": "dev"}),
	}

	tests := []struct {
		name    string
		rule    kcmv1.ResourceRule
		want    []string
		wantErr bool
	}{
		{
			name: "explicit names are returned verbatim, even if not present in system",
			rule: kcmv1.ResourceRule{Names: []string{"a", "missing"}},
			want: []string{"a", "missing"},
		},
		{
			name: "explicitly empty names list selects nothing, and must not fall back to matching everything",
			rule: kcmv1.ResourceRule{Names: []string{}},
			want: []string{},
		},
		{
			name: "string selector matches by label",
			rule: kcmv1.ResourceRule{StringSelector: "tier=prod"},
			want: []string{"a"},
		},
		{
			name: "structured selector matches by label",
			rule: kcmv1.ResourceRule{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "dev"}}},
			want: []string{"b"},
		},
		{
			name: "empty structured selector matches everything, same convention as TargetNamespaces",
			rule: kcmv1.ResourceRule{Selector: &metav1.LabelSelector{}},
			want: []string{"a", "b"},
		},
		{
			name: "no selector at all matches everything, same convention as TargetNamespaces",
			rule: kcmv1.ResourceRule{},
			want: []string{"a", "b"},
		},
		{
			name:    "invalid string selector errors",
			rule:    kcmv1.ResourceRule{StringSelector: "tier in (prod"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			got, err := ResolveNames(tt.rule, system)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got).To(Equal(tt.want))
		})
	}
}

func TestTargetNamespaces(t *testing.T) {
	tests := []struct {
		name             string
		targetNamespaces kcmv1.TargetNamespaces
		namespaces       []string
		expected         []string
		err              string
	}{
		{
			name:             "explicit list without the system namespace is used verbatim",
			targetNamespaces: kcmv1.TargetNamespaces{List: []string{testTargetNamespace, "team-b"}},
			expected:         []string{testTargetNamespace, "team-b"},
		},
		{
			name:             "explicit list drops the system namespace",
			targetNamespaces: kcmv1.TargetNamespaces{List: []string{testTargetNamespace, testSystemNamespace, "team-b"}},
			expected:         []string{testTargetNamespace, "team-b"},
		},
		{
			name:             "explicit list of only the system namespace resolves to nothing",
			targetNamespaces: kcmv1.TargetNamespaces{List: []string{testSystemNamespace}},
			expected:         []string{},
		},
		{
			name:       "no list and no selector lists every namespace but the system one",
			namespaces: []string{testSystemNamespace, testTargetNamespace, "team-b"},
			expected:   []string{testTargetNamespace, "team-b"},
		},
		{
			name:             "a selector matching the system namespace still excludes it",
			targetNamespaces: kcmv1.TargetNamespaces{StringSelector: "kcm=true"},
			namespaces:       []string{testSystemNamespace, testTargetNamespace},
			expected:         []string{testTargetNamespace},
		},
		{
			name:             "an invalid selector is reported",
			targetNamespaces: kcmv1.TargetNamespaces{StringSelector: "!!!"},
			err:              "failed to construct selector from target namespaces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			objs := make([]client.Object, 0, len(tt.namespaces))
			for _, ns := range tt.namespaces {
				objs = append(objs, &corev1.Namespace{
					Name: ns, Labels: map[string]string{"kcm": "true"},
				})
			}

			c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(objs...).Build()

			got, err := TargetNamespaces(t.Context(), c, testSystemNamespace, tt.targetNamespaces)
			if tt.err != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tt.err)))
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got).To(ConsistOf(tt.expected))
		})
	}
}

func TestIsAccessManagementOwnerReference(t *testing.T) {
	g := NewWithT(t)
	accessMgmt := newTestAccessManagement()

	stale := accessManagementOwnerReference()
	stale.UID = "since-recreated"
	stale.APIVersion = schema.GroupVersion{Group: kcmv1.GroupVersion.Group, Version: "v1alpha1"}.String()
	g.Expect(IsAccessManagementOwnerReference(stale, accessMgmt)).To(BeTrue(), "any version or UID of the same AccessManagement counts")

	g.Expect(IsAccessManagementOwnerReference(chainOwnerReference(), accessMgmt)).To(BeFalse())
	g.Expect(HasForeignOwnerReference([]metav1.OwnerReference{accessManagementOwnerReference()}, accessMgmt)).To(BeFalse())
	g.Expect(HasForeignOwnerReference([]metav1.OwnerReference{accessManagementOwnerReference(), chainOwnerReference()}, accessMgmt)).To(BeTrue())
}
