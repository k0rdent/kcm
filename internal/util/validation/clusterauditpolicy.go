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
	"fmt"

	auditvalidation "k8s.io/apiserver/pkg/apis/audit/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func ValidateClusterAuditPolicy(clPolicy *kcmv1.ClusterAuditPolicy) error {
	policy, err := clPolicy.ToAuditPolicy()
	if err != nil {
		return err // already wrapped
	}
	if errs := auditvalidation.ValidatePolicy(policy); errs != nil {
		return fmt.Errorf("invalid audit policy provided: %w", errs.ToAggregate())
	}

	return nil
}

func ClusterAuditPolicyDeletionAllowed(ctx context.Context, mgmtClient client.Client, clPolicy *kcmv1.ClusterAuditPolicy) error {
	return deletionAllowedIfUnreferenced(ctx, mgmtClient, clPolicy, kcmv1.ClusterDeploymentAuditPolicyIndexKey, "ClusterAuditPolicy")
}
