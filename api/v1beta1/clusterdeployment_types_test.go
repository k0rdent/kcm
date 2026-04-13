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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestHelmValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		cd              *ClusterDeployment
		expectedValues  map[string]any
		errContainsText string
	}{
		{
			name:            "nil receiver returns error",
			cd:              nil,
			errContainsText: "cluster deployment is nil",
		},
		{
			name:           "nil config returns empty map",
			cd:             &ClusterDeployment{},
			expectedValues: map[string]any{},
		},
		{
			name: "empty raw returns empty map",
			cd: &ClusterDeployment{
				Spec: ClusterDeploymentSpec{Config: &apiextv1.JSON{Raw: []byte("  \n\t  ")}},
			},
			expectedValues: map[string]any{},
		},
		{
			name: "null raw returns empty map",
			cd: &ClusterDeployment{
				Spec: ClusterDeploymentSpec{Config: &apiextv1.JSON{Raw: []byte("null")}},
			},
			expectedValues: map[string]any{},
		},
		{
			name: "json config unmarshals",
			cd: &ClusterDeployment{
				Spec: ClusterDeploymentSpec{Config: &apiextv1.JSON{Raw: []byte(`{"region":"eu","nested":{"enabled":true}}`)}},
			},
			expectedValues: map[string]any{
				"region": "eu",
				"nested": map[string]any{"enabled": true},
			},
		},
		{
			name: "yaml config unmarshals",
			cd: &ClusterDeployment{
				Spec: ClusterDeploymentSpec{Config: &apiextv1.JSON{Raw: []byte("region: eu\nnested:\n  enabled: true\n")}},
			},
			expectedValues: map[string]any{
				"region": "eu",
				"nested": map[string]any{"enabled": true},
			},
		},
		{
			name: "invalid config returns error",
			cd: &ClusterDeployment{
				Spec: ClusterDeploymentSpec{Config: &apiextv1.JSON{Raw: []byte("{invalid")}},
			},
			errContainsText: "error unmarshalling helm values for ClusterDeployment",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			values, err := tc.cd.HelmValues()
			if tc.errContainsText != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errContainsText)
				require.Nil(t, values)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, values)
			require.Equal(t, tc.expectedValues, values)
		})
	}
}

func TestSetHelmValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		cd                *ClusterDeployment
		values            map[string]any
		errContainsText   string
		expectConfigNil   bool
		expectedConfigRaw string
	}{
		{
			name:            "nil receiver returns error",
			cd:              nil,
			values:          map[string]any{"region": "eu"},
			errContainsText: "cluster deployment is nil",
		},
		{
			name: "nil values clear config",
			cd: &ClusterDeployment{
				Spec: ClusterDeploymentSpec{Config: &apiextv1.JSON{Raw: []byte(`{"region":"eu"}`)}},
			},
			values:          nil,
			expectConfigNil: true,
		},
		{
			name:              "empty map is persisted as object",
			cd:                &ClusterDeployment{},
			values:            map[string]any{},
			expectedConfigRaw: `{}`,
		},
		{
			name:              "map is marshalled to config",
			cd:                &ClusterDeployment{},
			values:            map[string]any{"region": "eu"},
			expectedConfigRaw: `{"region":"eu"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.cd.SetHelmValues(tc.values)
			if tc.errContainsText != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errContainsText)
				return
			}

			require.NoError(t, err)

			if tc.expectConfigNil {
				require.Nil(t, tc.cd.Spec.Config)
				return
			}

			require.NotNil(t, tc.cd.Spec.Config)
			require.JSONEq(t, tc.expectedConfigRaw, string(tc.cd.Spec.Config.Raw))
		})
	}
}

func TestAddHelmValues(t *testing.T) {
	t.Parallel()

	mutatorErr := errors.New("boom")

	tests := []struct {
		name            string
		cd              *ClusterDeployment
		mutator         func(map[string]any) error
		errContainsText string
		expectedValues  map[string]any
		expectConfigNil bool
	}{
		{
			name:            "nil mutator returns error",
			cd:              &ClusterDeployment{},
			mutator:         nil,
			errContainsText: "helm values mutator is nil",
		},
		{
			name:            "nil receiver returns error",
			cd:              nil,
			mutator:         func(map[string]any) error { return nil },
			errContainsText: "failed to get helm values: cluster deployment is nil",
		},
		{
			name: "mutator receives and persists values",
			cd:   &ClusterDeployment{},
			mutator: func(values map[string]any) error {
				values["region"] = "eu"
				values["nested"] = map[string]any{"enabled": true}
				return nil
			},
			expectedValues: map[string]any{
				"region": "eu",
				"nested": map[string]any{"enabled": true},
			},
		},
		{
			name:            "mutator error is returned",
			cd:              &ClusterDeployment{},
			mutator:         func(map[string]any) error { return mutatorErr },
			errContainsText: "failed to mutate helm values: boom",
			expectConfigNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.cd.AddHelmValues(tc.mutator)
			if tc.errContainsText != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errContainsText)
				if tc.cd != nil && tc.expectConfigNil {
					require.Nil(t, tc.cd.Spec.Config)
				}
				return
			}

			require.NoError(t, err)
			values, valuesErr := tc.cd.HelmValues()
			require.NoError(t, valuesErr)
			require.Equal(t, tc.expectedValues, values)
		})
	}
}
