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

package webhook

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/test/objects/clusterdeployment"
	"github.com/K0rdent/kcm/test/objects/rbacpolicy"
	"github.com/K0rdent/kcm/test/scheme"
)

var (
	validRBACPolicySpec = kcmv1.RBACPolicySpec{
		Bindings: []kcmv1.RBACPolicyBinding{
			{Name: "compute-admin", ClusterRole: "admin"},
			{Name: "compute-viewer", ClusterRole: "view"},
		},
	}

	invalidRBACPolicySpec = kcmv1.RBACPolicySpec{
		Bindings: []kcmv1.RBACPolicyBinding{
			{Name: "compute-admin", ClusterRole: "admin"},
			{Name: "compute-admin", ClusterRole: "view"},
		},
	}
)

//nolint:dupl
func TestRBACPolicyValidateCreate(t *testing.T) {
	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
		},
	})

	const namespace = "test-ns"

	tests := []struct {
		name            string
		policy          *kcmv1.RBACPolicy
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name: "should fail if the spec has duplicate binding names",
			policy: rbacpolicy.New(
				rbacpolicy.WithNamespace(namespace),
				rbacpolicy.WithSpec(invalidRBACPolicySpec),
			),
			err: `the RBACPolicy is invalid: duplicate binding name "compute-admin"`,
		},
		{
			name: "should succeed",
			policy: rbacpolicy.New(
				rbacpolicy.WithNamespace(namespace),
				rbacpolicy.WithSpec(validRBACPolicySpec),
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				Build()
			validator := &RBACPolicyValidator{Client: c}
			warn, err := validator.ValidateCreate(ctx, tt.policy)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}

			g.Expect(warn).To(Equal(tt.warnings))
		})
	}
}

func TestRBACPolicyValidateUpdate(t *testing.T) {
	const namespace = "test-ns"

	tests := []struct {
		name   string
		oldObj *kcmv1.RBACPolicy
		newObj *kcmv1.RBACPolicy
		err    string
	}{
		{
			name:   "should fail if the updated spec has duplicate binding names",
			oldObj: rbacpolicy.New(rbacpolicy.WithNamespace(namespace), rbacpolicy.WithSpec(validRBACPolicySpec)),
			newObj: rbacpolicy.New(rbacpolicy.WithNamespace(namespace), rbacpolicy.WithSpec(invalidRBACPolicySpec)),
			err:    `the RBACPolicy is invalid: duplicate binding name "compute-admin"`,
		},
		{
			name:   "should succeed",
			oldObj: rbacpolicy.New(rbacpolicy.WithNamespace(namespace), rbacpolicy.WithSpec(validRBACPolicySpec)),
			newObj: rbacpolicy.New(rbacpolicy.WithNamespace(namespace), rbacpolicy.WithSpec(validRBACPolicySpec)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
			validator := &RBACPolicyValidator{Client: c}
			_, err := validator.ValidateUpdate(t.Context(), tt.oldObj, tt.newObj)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}
		})
	}
}

//nolint:dupl
func TestRBACPolicyValidateDelete(t *testing.T) {
	g := NewWithT(t)

	ctx := t.Context()

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
			name: "deletion is not allowed, RBACPolicy is referenced in the ClusterDeployment",
			policy: rbacpolicy.New(
				rbacpolicy.WithNamespace(namespace),
				rbacpolicy.WithName(policyName),
			),
			existingObjects: []runtime.Object{
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithNamespace(namespace),
					clusterdeployment.WithRBACPolicy(policyName),
				),
			},
			err: fmt.Sprintf("cannot delete RBACPolicy %s/%s: it is still referenced by one or more ClusterDeployments", namespace, policyName),
		},
		{
			name: "deletion is allowed",
			policy: rbacpolicy.New(
				rbacpolicy.WithNamespace(namespace),
				rbacpolicy.WithName(policyName),
			),
			existingObjects: []runtime.Object{
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithNamespace("another-namespace"),
					clusterdeployment.WithRBACPolicy(policyName),
				),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentRBACPolicyIndexKey, kcmv1.ExtractRBACPolicyNameFromClusterDeployment).
				Build()
			validator := &RBACPolicyValidator{Client: c}
			_, err := validator.ValidateDelete(ctx, tt.policy)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				if err.Error() != tt.err {
					t.Fatalf("expected error '%s', got error: %s", tt.err, err.Error())
				}
			} else {
				g.Expect(err).To(Succeed())
			}
		})
	}
}
