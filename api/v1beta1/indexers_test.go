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

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestExtractAccessManagementTargetNamespaceLists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		object   client.Object
		expected []string
	}{
		{
			name:     "returns nil for unsupported object",
			object:   &Credential{},
			expected: nil,
		},
		{
			name: "deduplicates and sorts namespaces",
			object: &AccessManagement{
				Spec: AccessManagementSpec{
					AccessRules: []AccessRule{
						{TargetNamespaces: TargetNamespaces{List: []string{"ns-b", "ns-a"}}},
						{TargetNamespaces: TargetNamespaces{List: []string{"ns-b", "ns-c"}}},
					},
				},
			},
			expected: []string{"ns-a", "ns-b", "ns-c"},
		},
		{
			name: "returns nil when no list-based targets",
			object: &AccessManagement{
				Spec: AccessManagementSpec{
					AccessRules: []AccessRule{
						{TargetNamespaces: TargetNamespaces{StringSelector: "env=prod"}},
					},
				},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			namespaces := ExtractAccessManagementTargetNamespaceLists(tt.object)
			require.Equal(t, tt.expected, namespaces)
		})
	}
}

func TestExtractAccessManagementUsesSelector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		object   client.Object
		expected []string
	}{
		{
			name: "returns true marker for string selector",
			object: &AccessManagement{
				Spec: AccessManagementSpec{
					AccessRules: []AccessRule{{TargetNamespaces: TargetNamespaces{StringSelector: "env=prod"}}},
				},
			},
			expected: []string{"true"},
		},
		{
			name: "returns true marker for structured selector",
			object: &AccessManagement{
				Spec: AccessManagementSpec{
					AccessRules: []AccessRule{{TargetNamespaces: TargetNamespaces{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"env": "prod"}}}}},
				},
			},
			expected: []string{"true"},
		},
		{
			name: "returns nil for list-only rules",
			object: &AccessManagement{
				Spec: AccessManagementSpec{
					AccessRules: []AccessRule{{TargetNamespaces: TargetNamespaces{List: []string{"ns-a"}}}},
				},
			},
			expected: nil,
		},
		{
			name: "returns nil for empty structured selector",
			object: &AccessManagement{
				Spec: AccessManagementSpec{
					AccessRules: []AccessRule{{TargetNamespaces: TargetNamespaces{Selector: &metav1.LabelSelector{}}}},
				},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := ExtractAccessManagementUsesSelector(tt.object)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractAccessManagementTargetsAllNamespaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		object   client.Object
		expected []string
	}{
		{
			name: "returns true marker for empty target namespaces",
			object: &AccessManagement{
				Spec: AccessManagementSpec{AccessRules: []AccessRule{{TargetNamespaces: TargetNamespaces{}}}},
			},
			expected: []string{"true"},
		},
		{
			name: "returns true marker for empty structured selector",
			object: &AccessManagement{
				Spec: AccessManagementSpec{
					AccessRules: []AccessRule{{TargetNamespaces: TargetNamespaces{Selector: &metav1.LabelSelector{}}}},
				},
			},
			expected: []string{"true"},
		},
		{
			name: "returns nil for explicit namespace list",
			object: &AccessManagement{
				Spec: AccessManagementSpec{
					AccessRules: []AccessRule{{TargetNamespaces: TargetNamespaces{List: []string{"ns-a"}}}},
				},
			},
			expected: nil,
		},
		{
			name: "returns nil for non-empty selector",
			object: &AccessManagement{
				Spec: AccessManagementSpec{
					AccessRules: []AccessRule{{TargetNamespaces: TargetNamespaces{StringSelector: "env=prod"}}},
				},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := ExtractAccessManagementTargetsAllNamespaces(tt.object)
			require.Equal(t, tt.expected, result)
		})
	}
}
