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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func TestValidateClusterAuditPolicy(t *testing.T) {
	t.Run("valid policy passes", func(t *testing.T) {
		clPolicy := &kcmv1.ClusterAuditPolicy{
			Spec: kcmv1.ClusterAuditPolicySpec{
				Policy: kcmv1.Policy{
					Rules: []auditv1.PolicyRule{{Level: auditv1.LevelMetadata}},
				},
			},
		}

		if err := ValidateClusterAuditPolicy(clPolicy); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid rule level returns error", func(t *testing.T) {
		clPolicy := &kcmv1.ClusterAuditPolicy{
			Spec: kcmv1.ClusterAuditPolicySpec{
				Policy: kcmv1.Policy{
					Rules: []auditv1.PolicyRule{{Level: "NotALevel"}},
				},
			},
		}

		err := ValidateClusterAuditPolicy(clPolicy)
		if err == nil || !strings.Contains(err.Error(), "invalid audit policy provided") {
			t.Fatalf("err = %v, want invalid audit policy error", err)
		}
	})
}

func TestClusterAuditPolicyDeletionAllowed(t *testing.T) {
	clPolicy := &kcmv1.ClusterAuditPolicy{ObjectMeta: metav1.ObjectMeta{Name: "policy1", Namespace: "ns1"}}

	testDeletionAllowedByClusterDeploymentRef(
		t, clPolicy, ClusterAuditPolicyDeletionAllowed,
		kcmv1.ClusterDeploymentAuditPolicyIndexKey, kcmv1.ExtractClusterAuditPolicyNameFromClusterDeployment,
		kcmv1.ClusterDeploymentSpec{AuditPolicy: "policy1"},
	)
}
