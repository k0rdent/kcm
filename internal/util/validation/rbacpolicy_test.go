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
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/test/objects/clusterdeployment"
	"github.com/K0rdent/kcm/test/objects/rbacpolicy"
	"github.com/K0rdent/kcm/test/scheme"
)

func TestValidateRBACPolicy(t *testing.T) {
	tests := []struct {
		name   string
		policy *kcmv1.RBACPolicy
		err    string
	}{
		{
			name: "valid spec",
			policy: rbacpolicy.New(rbacpolicy.WithSpec(kcmv1.RBACPolicySpec{
				Bindings: []kcmv1.RBACPolicyBinding{
					{Name: "compute-admin", ClusterRole: "admin"},
					{Name: "compute-viewer", ClusterRole: "view"},
				},
			})),
		},
		{
			name: "duplicate binding names",
			policy: rbacpolicy.New(rbacpolicy.WithSpec(kcmv1.RBACPolicySpec{
				Bindings: []kcmv1.RBACPolicyBinding{
					{Name: "compute-admin", ClusterRole: "admin"},
					{Name: "compute-admin", ClusterRole: "view"},
				},
			})),
			err: `duplicate binding name "compute-admin"`,
		},
		{
			name: "multiple bindings referencing the same pre-existing clusterRole without rules is valid",
			policy: rbacpolicy.New(rbacpolicy.WithSpec(kcmv1.RBACPolicySpec{
				Bindings: []kcmv1.RBACPolicyBinding{
					{Name: "compute-admin", ClusterRole: "admin"},
					{Name: "compute-admin-2", ClusterRole: "admin"},
				},
			})),
		},
		{
			name: "two bindings defining rules for the same clusterRole",
			policy: rbacpolicy.New(rbacpolicy.WithSpec(kcmv1.RBACPolicySpec{
				Bindings: []kcmv1.RBACPolicyBinding{
					{Name: "compute-admin", ClusterRole: "custom", Rules: []rbacv1.PolicyRule{{Verbs: []string{"get"}}}},
					{Name: "compute-admin-2", ClusterRole: "custom", Rules: []rbacv1.PolicyRule{{Verbs: []string{"list"}}}},
				},
			})),
			err: `clusterRole "custom" has rules defined in more than one binding`,
		},
		{
			name: "binding name with uppercase characters is invalid",
			policy: rbacpolicy.New(rbacpolicy.WithSpec(kcmv1.RBACPolicySpec{
				Bindings: []kcmv1.RBACPolicyBinding{
					{Name: "Compute-Admin", ClusterRole: "admin"},
				},
			})),
			err: `binding name "Compute-Admin" produces an invalid ClusterRoleBinding name "k0rdent-Compute-Admin"`,
		},
		{
			name: "binding name with an invalid character is invalid",
			policy: rbacpolicy.New(rbacpolicy.WithSpec(kcmv1.RBACPolicySpec{
				Bindings: []kcmv1.RBACPolicyBinding{
					{Name: "compute_admin", ClusterRole: "admin"},
				},
			})),
			err: `binding name "compute_admin" produces an invalid ClusterRoleBinding name "k0rdent-compute_admin"`,
		},
		{
			name: "binding name that overflows the DNS-1123 subdomain length limit is invalid",
			policy: rbacpolicy.New(rbacpolicy.WithSpec(kcmv1.RBACPolicySpec{
				Bindings: []kcmv1.RBACPolicyBinding{
					{Name: strings.Repeat("a", 250), ClusterRole: "admin"},
				},
			})),
			err: "must be no more than 253 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			err := ValidateRBACPolicy(tt.policy)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}
		})
	}
}

func TestRBACPolicyDeletionAllowed(t *testing.T) {
	const (
		namespace  = "test-ns"
		policyName = "test-rbac-policy"
	)

	tests := []struct {
		name            string
		policy          *kcmv1.RBACPolicy
		existingObjects []runtime.Object
		err             string
	}{
		{
			name:   "referenced by a ClusterDeployment in the same namespace: not allowed",
			policy: rbacpolicy.New(rbacpolicy.WithNamespace(namespace), rbacpolicy.WithName(policyName)),
			existingObjects: []runtime.Object{
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithNamespace(namespace),
					clusterdeployment.WithRBACPolicy(policyName),
				),
			},
			err: fmt.Sprintf("cannot delete RBACPolicy %s/%s: it is still referenced by one or more ClusterDeployments", namespace, policyName),
		},
		{
			name:   "referenced by a ClusterDeployment in a different namespace: allowed",
			policy: rbacpolicy.New(rbacpolicy.WithNamespace(namespace), rbacpolicy.WithName(policyName)),
			existingObjects: []runtime.Object{
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithNamespace("another-namespace"),
					clusterdeployment.WithRBACPolicy(policyName),
				),
			},
		},
		{
			name:   "not referenced: allowed",
			policy: rbacpolicy.New(rbacpolicy.WithNamespace(namespace), rbacpolicy.WithName(policyName)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentRBACPolicyIndexKey, kcmv1.ExtractRBACPolicyNameFromClusterDeployment).
				Build()

			err := RBACPolicyDeletionAllowed(t.Context(), c, tt.policy)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}
		})
	}
}
