// Copyright 2025
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

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type templateChainSpecTest struct {
	templateChainSpec   TemplateChainSpec
	templateName        string
	expectedUpgradePath [][]string
	expectError         bool
}

func TestTemplateChainSpec_TemplateUpgradePath(t *testing.T) {
	t.Parallel()

	f := func(t *testing.T, tc templateChainSpecTest) {
		t.Helper()
		actualUpgradePath, err := tc.templateChainSpec.UpgradePaths(tc.templateName)
		if tc.expectError {
			require.Error(t, err)
			require.Nil(t, actualUpgradePath)
		} else {
			require.ElementsMatch(t, tc.expectedUpgradePath, actualUpgradePath)
		}
	}

	tests := map[string]templateChainSpecTest{
		"no-upgrades": {
			templateChainSpec: TemplateChainSpec{SupportedTemplates: []SupportedTemplate{
				{Name: "foo-0-1-0"},
			}},
			templateName:        "foo-0-1-0",
			expectedUpgradePath: [][]string{},
		},
		"upgrade-path-error": {
			templateChainSpec: TemplateChainSpec{SupportedTemplates: []SupportedTemplate{
				{
					Name: "foo-0-1-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-0-2-0"},
					},
				},
				{
					Name: "foo-0-2-0",
				},
			}},
			templateName:        "foo-1-0-0",
			expectedUpgradePath: nil,
			expectError:         true,
		},
		"upgrade-path-1": {
			templateChainSpec: TemplateChainSpec{SupportedTemplates: []SupportedTemplate{
				{
					Name: "foo-0-1-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-0-2-0"},
					},
				},
				{
					Name: "foo-0-2-0",
				},
			}},
			templateName:        "foo-0-1-0",
			expectedUpgradePath: [][]string{{"foo-0-2-0"}},
		},
		"upgrade-path-2": {
			templateChainSpec: TemplateChainSpec{SupportedTemplates: []SupportedTemplate{
				{
					Name: "foo-0-1-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-0-2-0"},
						{Name: "foo-0-3-0"},
						{Name: "foo-0-4-0"},
					},
				},
				{
					Name: "foo-0-2-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-0-3-0"},
						{Name: "foo-0-4-0"},
					},
				},
				{
					Name: "foo-0-3-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-0-4-0"},
					},
				},
				{
					Name: "foo-0-4-0",
				},
			}},
			templateName:        "foo-0-1-0",
			expectedUpgradePath: [][]string{{"foo-0-2-0"}, {"foo-0-3-0"}, {"foo-0-4-0"}},
		},
		"upgrade-path-3": {
			templateChainSpec: TemplateChainSpec{SupportedTemplates: []SupportedTemplate{
				{
					Name: "foo-0-1-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-0-2-0"},
						{Name: "foo-0-3-0"},
						{Name: "foo-0-4-0"},
					},
				},
				{
					Name: "foo-0-2-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-0-3-0"},
						{Name: "foo-0-4-0"},
					},
				},
				{
					Name: "foo-0-3-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-0-4-0"},
					},
				},
				{
					Name: "foo-0-4-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-1-0-0"},
					},
				},
				{
					Name: "foo-1-0-0",
				},
			}},
			templateName: "foo-0-1-0",
			expectedUpgradePath: [][]string{
				{"foo-0-2-0"},
				{"foo-0-3-0"},
				{"foo-0-4-0"},
				{"foo-0-4-0", "foo-1-0-0"},
			},
		},
		"upgrade-path-4": {
			templateChainSpec: TemplateChainSpec{SupportedTemplates: []SupportedTemplate{
				{
					Name: "foo-0-1-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-0-2-0"},
						{Name: "foo-0-3-0"},
						{Name: "foo-0-4-0"},
					},
				},
				{
					Name: "foo-0-2-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-0-3-0"},
						{Name: "foo-0-4-0"},
					},
				},
				{
					Name: "foo-0-3-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-0-4-0"},
					},
				},
				{
					Name: "foo-0-4-0",
					AvailableUpgrades: []AvailableUpgrade{
						{Name: "foo-1-0-0"},
					},
				},
				{
					Name: "foo-1-0-0",
				},
			}},
			templateName: "foo-0-3-0",
			expectedUpgradePath: [][]string{
				{"foo-0-4-0"},
				{"foo-0-4-0", "foo-1-0-0"},
			},
		},
	}

	for testName, tc := range tests {
		t.Run(testName, func(t *testing.T) {
			t.Parallel()
			f(t, tc)
		})
	}
}
