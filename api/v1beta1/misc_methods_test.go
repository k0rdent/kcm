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
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
)

func TestClusterTemplateChainMethods(t *testing.T) {
	c := &ClusterTemplateChain{Spec: TemplateChainSpec{SupportedTemplates: []SupportedTemplate{{Name: "t1"}}}}

	if got := c.Kind(); got != ClusterTemplateChainKind {
		t.Errorf("Kind() = %q, want %q", got, ClusterTemplateChainKind)
	}
	if got := c.TemplateKind(); got != ClusterTemplateKind {
		t.Errorf("TemplateKind() = %q, want %q", got, ClusterTemplateKind)
	}
	if got := c.GetSpec(); got != &c.Spec {
		t.Error("GetSpec() did not return a pointer to Spec")
	}
	if got := c.GetStatus(); got != &c.Status {
		t.Error("GetStatus() did not return a pointer to Status")
	}
}

func TestServiceTemplateChainMethods(t *testing.T) {
	c := &ServiceTemplateChain{Spec: TemplateChainSpec{SupportedTemplates: []SupportedTemplate{{Name: "t1"}}}}

	if got := c.Kind(); got != ServiceTemplateChainKind {
		t.Errorf("Kind() = %q, want %q", got, ServiceTemplateChainKind)
	}
	if got := c.TemplateKind(); got != ServiceTemplateKind {
		t.Errorf("TemplateKind() = %q, want %q", got, ServiceTemplateKind)
	}
	if got := c.GetSpec(); got != &c.Spec {
		t.Error("GetSpec() did not return a pointer to Spec")
	}
	if got := c.GetStatus(); got != &c.Status {
		t.Error("GetStatus() did not return a pointer to Status")
	}
}

func TestCredentialGetConditions(t *testing.T) {
	cred := &Credential{Status: CredentialStatus{Conditions: []metav1.Condition{{Type: "Ready"}}}}
	got := cred.GetConditions()
	if got != &cred.Status.Conditions {
		t.Error("GetConditions() did not return a pointer to Status.Conditions")
	}
}

func TestClusterDeploymentGetConditions(t *testing.T) {
	cd := &ClusterDeployment{Status: ClusterDeploymentStatus{Conditions: []metav1.Condition{{Type: "Ready"}}}}
	got := cd.GetConditions()
	if got != &cd.Status.Conditions {
		t.Error("GetConditions() did not return a pointer to Status.Conditions")
	}
}

func TestClusterIPAMClaimValidate(t *testing.T) {
	t.Run("no networks: valid", func(t *testing.T) {
		c := &ClusterIPAMClaim{}
		if err := c.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("valid CIDR and IP addresses", func(t *testing.T) {
		c := &ClusterIPAMClaim{Spec: ClusterIPAMClaimSpec{
			NodeNetwork: AddressSpaceSpec{CIDR: "10.0.0.0/24", IPAddresses: []string{"10.0.0.5"}},
		}}
		if err := c.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid CIDR returns error", func(t *testing.T) {
		c := &ClusterIPAMClaim{Spec: ClusterIPAMClaimSpec{
			ClusterNetwork: AddressSpaceSpec{CIDR: "not-a-cidr"},
		}}
		if err := c.Validate(); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("invalid IP address returns error", func(t *testing.T) {
		c := &ClusterIPAMClaim{Spec: ClusterIPAMClaimSpec{
			ExternalNetwork: AddressSpaceSpec{IPAddresses: []string{"not-an-ip"}},
		}}
		if err := c.Validate(); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestClusterAuditPolicyGetPolicyAndToAuditPolicy(t *testing.T) {
	t.Run("GetPolicy on nil spec returns empty policy", func(t *testing.T) {
		var s *ClusterAuditPolicySpec
		got := s.GetPolicy()
		if got == nil || len(got.Rules) != 0 {
			t.Errorf("got %+v, want empty policy", got)
		}
	})

	t.Run("GetPolicy populates TypeMeta and copies rules", func(t *testing.T) {
		s := &ClusterAuditPolicySpec{Policy: Policy{Rules: []auditv1.PolicyRule{{Level: auditv1.LevelMetadata}}}}
		got := s.GetPolicy()
		if got.APIVersion != "audit.k8s.io/v1" || got.Kind != "Policy" {
			t.Errorf("TypeMeta = %+v", got.TypeMeta)
		}
		if len(got.Rules) != 1 || got.Rules[0].Level != auditv1.LevelMetadata {
			t.Errorf("Rules = %+v", got.Rules)
		}
	})

	t.Run("ToAuditPolicy on nil receiver returns empty policy, no error", func(t *testing.T) {
		var p *ClusterAuditPolicy
		got, err := p.ToAuditPolicy()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || len(got.Rules) != 0 {
			t.Errorf("got %+v, want empty policy", got)
		}
	})

	t.Run("ToAuditPolicy converts rules", func(t *testing.T) {
		p := &ClusterAuditPolicy{Spec: ClusterAuditPolicySpec{
			Policy: Policy{Rules: []auditv1.PolicyRule{{Level: auditv1.LevelRequestResponse}}},
		}}
		got, err := p.ToAuditPolicy()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Rules) != 1 || string(got.Rules[0].Level) != string(auditv1.LevelRequestResponse) {
			t.Errorf("got %+v", got)
		}
	})
}

func TestClusterAuthenticationGetAuthConfig(t *testing.T) {
	t.Run("nil spec returns empty config", func(t *testing.T) {
		var s *ClusterAuthenticationSpec
		got := s.GetAuthConfig()
		if got == nil || len(got.JWT) != 0 {
			t.Errorf("got %+v, want empty config", got)
		}
	})

	t.Run("populated spec copies JWT and Anonymous", func(t *testing.T) {
		s := &ClusterAuthenticationSpec{
			AuthenticationConfiguration: &AuthenticationConfiguration{
				JWT:       []apiserverv1.JWTAuthenticator{{Issuer: apiserverv1.Issuer{URL: "https://issuer.example.com"}}},
				Anonymous: &apiserverv1.AnonymousAuthConfig{Enabled: true},
			},
		}
		got := s.GetAuthConfig()
		if got.APIVersion != "apiserver.config.k8s.io/v1" || got.Kind != "AuthenticationConfiguration" {
			t.Errorf("TypeMeta = %+v", got.TypeMeta)
		}
		if len(got.JWT) != 1 || got.JWT[0].Issuer.URL != "https://issuer.example.com" {
			t.Errorf("JWT = %+v", got.JWT)
		}
		if got.Anonymous == nil || !got.Anonymous.Enabled {
			t.Errorf("Anonymous = %+v", got.Anonymous)
		}
	})
}
