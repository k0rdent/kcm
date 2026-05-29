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
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/test/objects/clusterauditpolicy"
	"github.com/K0rdent/kcm/test/objects/clusterdeployment"
	"github.com/K0rdent/kcm/test/scheme"
)

var (
	validAuditPolicySpec = kcmv1.ClusterAuditPolicySpec{
		Policy: kcmv1.Policy{
			Rules: []auditv1.PolicyRule{
				{
					Level: auditv1.LevelMetadata,
				},
			},
		},
	}

	invalidAuditPolicySpec = kcmv1.ClusterAuditPolicySpec{
		Policy: kcmv1.Policy{
			Rules: []auditv1.PolicyRule{
				{
					Level: auditv1.Level("invalid-level"),
				},
			},
		},
	}
)

func TestClusterAuditPolicyValidateCreate(t *testing.T) {
	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
		},
	})

	const namespace = "test-ns"

	tests := []struct {
		name            string
		clAuditPolicy   *kcmv1.ClusterAuditPolicy
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name: "should fail if the audit Policy is invalid",
			clAuditPolicy: clusterauditpolicy.New(
				clusterauditpolicy.WithNamespace(namespace),
				clusterauditpolicy.WithSpec(invalidAuditPolicySpec),
			),
			err: "the ClusterAuditPolicy is invalid: invalid audit policy provided: rules[0].level: Unsupported value: \"invalid-level\": supported values: \"None\", \"Metadata\", \"Request\", \"RequestResponse\"",
		},
		{
			name: "should succeed",
			clAuditPolicy: clusterauditpolicy.New(
				clusterauditpolicy.WithNamespace(namespace),
				clusterauditpolicy.WithSpec(validAuditPolicySpec),
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
			validator := &ClusterAuditPolicyValidator{Client: c}
			warn, err := validator.ValidateCreate(ctx, tt.clAuditPolicy)
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

//nolint:dupl
func TestClusterAuditPolicyValidateDelete(t *testing.T) {
	g := NewWithT(t)

	ctx := t.Context()

	const (
		namespace     = "test-ns"
		clAuditPolicy = "test-cl-audit-policy"
	)

	tests := []struct {
		name            string
		clAuditPolicy   *kcmv1.ClusterAuditPolicy
		existingObjects []runtime.Object
		err             string
	}{
		{
			name: "deletion is not allowed, ClusterAuditPolicy is referenced in the ClusterDeployment",
			clAuditPolicy: clusterauditpolicy.New(
				clusterauditpolicy.WithNamespace(namespace),
				clusterauditpolicy.WithName(clAuditPolicy),
			),
			existingObjects: []runtime.Object{
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithNamespace(namespace),
					clusterdeployment.WithClusterAuditPolicy(clAuditPolicy),
				),
			},
			err: fmt.Sprintf("cannot delete ClusterAuditPolicy %s/%s: it is still referenced by one or more ClusterDeployments", namespace, clAuditPolicy),
		},
		{
			name: "deletion is allowed",
			clAuditPolicy: clusterauditpolicy.New(
				clusterauditpolicy.WithNamespace(namespace),
				clusterauditpolicy.WithName(clAuditPolicy),
			),
			existingObjects: []runtime.Object{
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithNamespace("another-namespace"),
					clusterdeployment.WithClusterAuditPolicy(clAuditPolicy),
				),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentAuditPolicyIndexKey, kcmv1.ExtractClusterAuditPolicyNameFromClusterDeployment).
				Build()
			validator := &ClusterAuditPolicyValidator{Client: c}
			_, err := validator.ValidateDelete(ctx, tt.clAuditPolicy)
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
