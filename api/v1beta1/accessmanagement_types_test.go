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

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestResourceRuleGroupKind(t *testing.T) {
	tests := []struct {
		name string
		rule ResourceRule
		want schema.GroupKind
	}{
		{
			name: "empty APIGroup defaults to the built-in group for a built-in Kind",
			rule: ResourceRule{Kind: ClusterTemplateChainKind},
			want: schema.GroupKind{Group: GroupVersion.Group, Kind: ClusterTemplateChainKind},
		},
		{
			name: "custom APIGroup is used verbatim",
			rule: ResourceRule{APIGroup: "example.com", Kind: "Widget"},
			want: schema.GroupKind{Group: "example.com", Kind: "Widget"},
		},
		{
			name: "empty APIGroup for a non-built-in Kind means the core group",
			rule: ResourceRule{Kind: "Secret"},
			want: schema.GroupKind{Group: "", Kind: "Secret"},
		},
		{
			name: "explicit empty APIGroup for a built-in Kind name still defaults it (Kind alone decides)",
			rule: ResourceRule{APIGroup: "", Kind: CredentialKind},
			want: schema.GroupKind{Group: GroupVersion.Group, Kind: CredentialKind},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(tt.rule.GroupKind()).To(Equal(tt.want))
		})
	}
}

func TestMigrateAccessRules(t *testing.T) {
	tests := []struct {
		name            string
		rules           []AccessRule
		wantResources   [][]ResourceRule
		wantChanged     bool
		wantDeprecClear bool
	}{
		{
			name:        "nil rules is a no-op",
			rules:       nil,
			wantChanged: false,
		},
		{
			name: "no deprecated fields, no Resources: no-op",
			rules: []AccessRule{
				{TargetNamespaces: TargetNamespaces{List: []string{"ns1"}}},
			},
			wantChanged:   false,
			wantResources: [][]ResourceRule{nil},
		},
		{
			name: "single deprecated field migrates to Resources and is cleared",
			rules: []AccessRule{
				{ClusterTemplateChains: []string{"chain-a", "chain-b"}},
			},
			wantChanged: true,
			wantResources: [][]ResourceRule{
				{{APIGroup: GroupVersion.Group, Kind: ClusterTemplateChainKind, Names: []string{"chain-a", "chain-b"}}},
			},
		},
		{
			name: "all six deprecated fields migrate in a stable order",
			rules: []AccessRule{
				{
					ClusterTemplateChains:  []string{"ct"},
					ServiceTemplateChains:  []string{"st"},
					Credentials:            []string{"cred"},
					ClusterAuthentications: []string{"auth"},
					DataSources:            []string{"ds"},
					ClusterAuditPolicies:   []string{"cap"},
				},
			},
			wantChanged: true,
			wantResources: [][]ResourceRule{
				{
					{APIGroup: GroupVersion.Group, Kind: ClusterTemplateChainKind, Names: []string{"ct"}},
					{APIGroup: GroupVersion.Group, Kind: ServiceTemplateChainKind, Names: []string{"st"}},
					{APIGroup: GroupVersion.Group, Kind: CredentialKind, Names: []string{"cred"}},
					{APIGroup: GroupVersion.Group, Kind: ClusterAuthenticationKind, Names: []string{"auth"}},
					{APIGroup: GroupVersion.Group, Kind: DataSourceKind, Names: []string{"ds"}},
					{APIGroup: GroupVersion.Group, Kind: ClusterAuditPolicyKind, Names: []string{"cap"}},
				},
			},
		},
		{
			name: "Resources entry for a built-in Kind missing APIGroup gets defaulted",
			rules: []AccessRule{
				{Resources: []ResourceRule{{Kind: CredentialKind, Names: []string{"c1"}}}},
			},
			wantChanged: true,
			wantResources: [][]ResourceRule{
				{{APIGroup: GroupVersion.Group, Kind: CredentialKind, Names: []string{"c1"}}},
			},
		},
		{
			name: "Resources entry for a non-built-in Kind with empty APIGroup means the core group and is left untouched",
			rules: []AccessRule{
				{Resources: []ResourceRule{{Kind: "Widget", Names: []string{"w1"}}}},
			},
			wantChanged: false,
			wantResources: [][]ResourceRule{
				{{Kind: "Widget", Names: []string{"w1"}}},
			},
		},
		{
			name: "Resources entry with explicit APIGroup is left untouched",
			rules: []AccessRule{
				{Resources: []ResourceRule{{APIGroup: "example.com", Kind: "Widget", Names: []string{"w1"}}}},
			},
			wantChanged: false,
			wantResources: [][]ResourceRule{
				{{APIGroup: "example.com", Kind: "Widget", Names: []string{"w1"}}},
			},
		},
		{
			name: "deprecated field and an existing Resources entry for the same Kind: appended, not merged",
			rules: []AccessRule{
				{
					Credentials: []string{"cred-b"},
					Resources:   []ResourceRule{{APIGroup: GroupVersion.Group, Kind: CredentialKind, Names: []string{"cred-a"}}},
				},
			},
			wantChanged: true,
			wantResources: [][]ResourceRule{
				{
					{APIGroup: GroupVersion.Group, Kind: CredentialKind, Names: []string{"cred-a"}},
					{APIGroup: GroupVersion.Group, Kind: CredentialKind, Names: []string{"cred-b"}},
				},
			},
		},
		{
			name: "empty-but-non-nil deprecated slice does not synthesize an entry",
			rules: []AccessRule{
				{ClusterTemplateChains: []string{}},
			},
			wantChanged:   false,
			wantResources: [][]ResourceRule{nil},
		},
		{
			name: "multiple rules are migrated independently",
			rules: []AccessRule{
				{ClusterTemplateChains: []string{"ct"}},
				{Credentials: []string{"cred"}},
				{TargetNamespaces: TargetNamespaces{List: []string{"ns"}}},
			},
			wantChanged: true,
			wantResources: [][]ResourceRule{
				{{APIGroup: GroupVersion.Group, Kind: ClusterTemplateChainKind, Names: []string{"ct"}}},
				{{APIGroup: GroupVersion.Group, Kind: CredentialKind, Names: []string{"cred"}}},
				nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			am := &AccessManagement{Spec: AccessManagementSpec{AccessRules: tt.rules}}
			changed := am.MigrateAccessRules()
			g.Expect(changed).To(Equal(tt.wantChanged))

			migrated := am.Spec.AccessRules
			if tt.wantResources != nil {
				g.Expect(migrated).To(HaveLen(len(tt.wantResources)))
				for i, want := range tt.wantResources {
					g.Expect(migrated[i].Resources).To(Equal(want))
				}
			}

			for _, rule := range migrated {
				g.Expect(rule.HasDeprecatedFields()).To(BeFalse(), "deprecated fields must always be cleared after migration")
			}
		})
	}
}

func TestMigrateAccessRulesIdempotent(t *testing.T) {
	g := NewWithT(t)

	am := &AccessManagement{
		Spec: AccessManagementSpec{
			AccessRules: []AccessRule{
				{
					ClusterTemplateChains:  []string{"ct"},
					ServiceTemplateChains:  []string{"st"},
					Credentials:            []string{"cred"},
					ClusterAuthentications: []string{"auth"},
					DataSources:            []string{"ds"},
					ClusterAuditPolicies:   []string{"cap"},
					Resources:              []ResourceRule{{APIGroup: "example.com", Kind: "Widget", Names: []string{"w1"}}},
				},
			},
		},
	}

	g.Expect(am.MigrateAccessRules()).To(BeTrue())
	g.Expect(am.Spec.AccessRules[0].Resources).To(HaveLen(7))

	migrated := am.Spec.AccessRules
	g.Expect(am.MigrateAccessRules()).To(BeFalse())
	g.Expect(am.Spec.AccessRules).To(Equal(migrated))
}

func TestAccessRuleEffectiveResources(t *testing.T) {
	tests := []struct {
		name string
		rule AccessRule
		want []ResourceRule
	}{
		{
			name: "no Resources, no deprecated fields: empty",
			rule: AccessRule{},
			want: nil,
		},
		{
			name: "old-styled only: synthesized from deprecated fields for backward compatibility",
			rule: AccessRule{
				ClusterTemplateChains: []string{"ct"},
				Credentials:           []string{"cred"},
			},
			want: []ResourceRule{
				{APIGroup: GroupVersion.Group, Kind: ClusterTemplateChainKind, Names: []string{"ct"}},
				{APIGroup: GroupVersion.Group, Kind: CredentialKind, Names: []string{"cred"}},
			},
		},
		{
			name: "old-styled, all six deprecated fields synthesized in a stable order",
			rule: AccessRule{
				ClusterTemplateChains:  []string{"ct"},
				ServiceTemplateChains:  []string{"st"},
				Credentials:            []string{"cred"},
				ClusterAuthentications: []string{"auth"},
				DataSources:            []string{"ds"},
				ClusterAuditPolicies:   []string{"cap"},
			},
			want: []ResourceRule{
				{APIGroup: GroupVersion.Group, Kind: ClusterTemplateChainKind, Names: []string{"ct"}},
				{APIGroup: GroupVersion.Group, Kind: ServiceTemplateChainKind, Names: []string{"st"}},
				{APIGroup: GroupVersion.Group, Kind: CredentialKind, Names: []string{"cred"}},
				{APIGroup: GroupVersion.Group, Kind: ClusterAuthenticationKind, Names: []string{"auth"}},
				{APIGroup: GroupVersion.Group, Kind: DataSourceKind, Names: []string{"ds"}},
				{APIGroup: GroupVersion.Group, Kind: ClusterAuditPolicyKind, Names: []string{"cap"}},
			},
		},
		{
			name: "new-styled only: Resources returned verbatim",
			rule: AccessRule{
				Resources: []ResourceRule{{APIGroup: "example.com", Kind: "Widget", Names: []string{"w1"}}},
			},
			want: []ResourceRule{{APIGroup: "example.com", Kind: "Widget", Names: []string{"w1"}}},
		},
		{
			name: "both present: new-styled Resources wins outright, deprecated fields ignored",
			rule: AccessRule{
				Resources:   []ResourceRule{{APIGroup: "example.com", Kind: "Widget", Names: []string{"w1"}}},
				Credentials: []string{"cred"},
			},
			want: []ResourceRule{{APIGroup: "example.com", Kind: "Widget", Names: []string{"w1"}}},
		},
		{
			name: "empty-but-non-nil deprecated slice contributes nothing",
			rule: AccessRule{ClusterTemplateChains: []string{}},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(tt.rule.EffectiveResources()).To(Equal(tt.want))
		})
	}
}
