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

	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// ValidateRBACPolicy performs structural validation that cannot be expressed via CRD schema
// (e.g. cross-field uniqueness).
func ValidateRBACPolicy(policy *kcmv1.RBACPolicy) error {
	names := make(map[string]struct{}, len(policy.Spec.Bindings))
	rolesWithRules := make(map[string]struct{}, len(policy.Spec.Bindings))
	for _, binding := range policy.Spec.Bindings {
		if _, exists := names[binding.Name]; exists {
			return fmt.Errorf("duplicate binding name %q", binding.Name)
		}
		names[binding.Name] = struct{}{}

		if len(binding.Rules) == 0 {
			continue
		}
		if _, exists := rolesWithRules[binding.ClusterRole]; exists {
			return fmt.Errorf("clusterRole %q has rules defined in more than one binding", binding.ClusterRole)
		}
		rolesWithRules[binding.ClusterRole] = struct{}{}
	}

	return nil
}

// RBACPolicyDeletionAllowed returns an error if the given RBACPolicy is still referenced by any
// ClusterDeployment in its namespace, since those ClusterDeployments would be left pointing at a
// missing object.
func RBACPolicyDeletionAllowed(ctx context.Context, mgmtClient client.Client, policy *kcmv1.RBACPolicy) error {
	return deletionAllowedIfUnreferenced(ctx, mgmtClient, policy, kcmv1.ClusterDeploymentRBACPolicyIndexKey, "RBACPolicy")
}
