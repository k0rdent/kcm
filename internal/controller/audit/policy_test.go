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

package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
)

func TestGetDefaultClusterAuditPolicySpec(t *testing.T) {
	spec, err := GetDefaultClusterAuditPolicySpec()
	require.NoError(t, err)

	policy := spec.Policy
	assert.Equal(t, "audit.k8s.io/v1", policy.APIVersion)
	assert.Equal(t, "Policy", policy.Kind)
	assert.NotEmpty(t, policy.Rules, "policy should have rules")

	// Verify that at least one rule references the known API groups.
	found := false
	for _, rule := range policy.Rules {
		if matchesKnownAPIs(rule.Resources) {
			found = true
			break
		}
	}
	assert.True(t, found, "policy should contain at least one rule with known API groups")
}

func matchesKnownAPIs(groups []auditv1.GroupResources) bool {
	if len(groups) != len(knownAPIs) {
		return false
	}
	have := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		have[g.Group] = struct{}{}
	}
	for _, api := range knownAPIs {
		if _, ok := have[api]; !ok {
			return false
		}
	}
	return true
}
