// Copyright 2024
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
	"testing"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

func TestValidateServiceDependency(t *testing.T) {
	for _, tc := range []struct {
		name        string
		services    []kcmv1.Service
		expectedErr string
	}{
		{
			name: "empty",
		},
		{
			name: "happy path",
			services: []kcmv1.Service{
				{Namespace: "A", Name: "a"},
				{Namespace: "B", Name: "b"},
				{Namespace: "C", Name: "c"},
			},
		},
		{
			name: "dependency that is not defined as a service",
			services: []kcmv1.Service{
				{Namespace: "A", Name: "a", DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "C", Name: "c"}}},
				{Namespace: "B", Name: "b"},
			},
			expectedErr: "dependency C/c of service A/a is not defined as a service",
		},
		{
			name: "multiple dependencies that are not defined as services",
			services: []kcmv1.Service{
				{Namespace: "A", Name: "a", DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "C", Name: "c"}, {Namespace: "D", Name: "d"}}},
				{Namespace: "B", Name: "b"},
			},
			expectedErr: "dependency C/c of service A/a is not defined as a service" +
				"\n" + "dependency D/d of service A/a is not defined as a service",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateServiceDependency(tc.services); err != nil {
				require.EqualError(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateServiceDependencyCycle(t *testing.T) {
	for _, tc := range []struct {
		testName string
		services []kcmv1.Service
		isErr    bool
	}{
		{
			testName: "empty",
			services: []kcmv1.Service{},
		},
		{
			testName: "single service",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
			},
		},
		{
			testName: "single service illegally repeated but no cycle",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
				{
					Namespace: "A", Name: "a",
				},
			},
		},
		{
			testName: "services A->B",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}},
				},
				{
					Namespace: "B", Name: "b",
				},
			},
		},
		{
			testName: "services B->A",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
			},
		},
		{
			testName: "services B->A",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
			},
		},
		{
			testName: "services A->A",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
			},
			isErr: true,
		},
		{
			testName: "services A<->B",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}},
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
			},
			isErr: true,
		},
		{
			testName: "services A, C<->B",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "C", Name: "c"}},
				},
				{
					Namespace: "C", Name: "c",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}},
				},
			},
			isErr: true,
		},
		{
			testName: "services C<->B->A",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}, {Namespace: "C", Name: "c"}},
				},
				{
					Namespace: "C", Name: "c",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}},
				},
			},
			isErr: true,
		},
		{
			testName: "services BC->A, D->BC",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
				{
					Namespace: "C", Name: "c",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
				{
					Namespace: "D", Name: "d",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}, {Namespace: "C", Name: "c"}},
				},
			},
		},
		{
			testName: "services A->BC, B->DE, C, D, E",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}, {Namespace: "C", Name: "c"}},
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "D", Name: "d"}, {Namespace: "E", Name: "e"}},
				},
				{
					Namespace: "C", Name: "c",
				},
				{
					Namespace: "D", Name: "d",
				},
				{
					Namespace: "E", Name: "e",
				},
			},
		},
		{
			testName: "services A->BC, B->DE, C, D, E->A",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}, {Namespace: "C", Name: "c"}},
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "D", Name: "d"}, {Namespace: "E", Name: "e"}},
				},
				{
					Namespace: "C", Name: "c",
				},
				{
					Namespace: "D", Name: "d",
				},
				{
					Namespace: "E", Name: "e",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
			},
			isErr: true,
		},
		{
			testName: "services C, A->BC, D, B->DE, E->A",
			services: []kcmv1.Service{
				{
					Namespace: "C", Name: "c",
				},
				{
					Namespace: "A", Name: "a",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}, {Namespace: "C", Name: "c"}},
				},
				{
					Namespace: "D", Name: "d",
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "D", Name: "d"}, {Namespace: "E", Name: "e"}},
				},
				{
					Namespace: "E", Name: "e",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
			},
			isErr: true,
		},
	} {
		t.Run(tc.testName, func(t *testing.T) {
			err := validateServiceDependencyCycle(tc.services)
			if tc.isErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestServiceSpecDriftDetection covers serviceSpecDriftDetection directly: both
// ClusterDeployDriftDetection and MultiClusterServiceDriftDetection are thin wrappers
// around it, so their own tests only need to check that they forward to it correctly.
func TestServiceSpecDriftDetection(t *testing.T) {
	tests := []struct {
		name string
		spec kcmv1.ServiceSpec
		err  string
	}{
		{
			name: "no serviceSpec: success",
		},
		{
			name: "top-level syncMode Continuous: success",
			spec: kcmv1.ServiceSpec{SyncMode: string(addoncontrollerv1beta1.SyncModeContinuous)}, //nolint:staticcheck
		},
		{
			name: "top-level syncMode ContinuousWithDriftDetection: error",
			spec: kcmv1.ServiceSpec{SyncMode: string(addoncontrollerv1beta1.SyncModeContinuousWithDriftDetection)}, //nolint:staticcheck
			err:  "SyncModeContinuousWithDriftDetection is not supported",
		},
		{
			name: "provider.config without syncMode: success",
			spec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Config: &apiextv1.JSON{Raw: []byte(`{"priority":1}`)},
				},
			},
		},
		{
			name: "provider.config syncMode Continuous: success",
			spec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Config: &apiextv1.JSON{Raw: []byte(`{"syncMode":"Continuous"}`)},
				},
			},
		},
		{
			name: "provider.config syncMode ContinuousWithDriftDetection: error",
			spec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Config: &apiextv1.JSON{Raw: []byte(`{"syncMode":"ContinuousWithDriftDetection"}`)},
				},
			},
			err: "SyncModeContinuousWithDriftDetection is not supported",
		},
		{
			name: "provider.config syncMode ContinuousWithDriftDetection, default provider name set explicitly: error",
			spec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Name:   kubeutil.DefaultStateManagementProvider,
					Config: &apiextv1.JSON{Raw: []byte(`{"syncMode":"ContinuousWithDriftDetection"}`)},
				},
			},
			err: "SyncModeContinuousWithDriftDetection is not supported",
		},
		{
			name: "provider.config syncMode ContinuousWithDriftDetection, non-default provider: success",
			spec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Name:   "some-other-provider",
					Config: &apiextv1.JSON{Raw: []byte(`{"syncMode":"ContinuousWithDriftDetection"}`)},
				},
			},
		},
		{
			name: "provider.config malformed JSON, non-default provider: success",
			spec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Name:   "some-other-provider",
					Config: &apiextv1.JSON{Raw: []byte(`not-json`)},
				},
			},
		},
		{
			name: "provider.config malformed JSON: error",
			spec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Config: &apiextv1.JSON{Raw: []byte(`{`)},
				},
			},
			err: "failed to unmarshal raw config to profile configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := serviceSpecDriftDetection(tt.spec)
			if tt.err == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.err)
		})
	}
}
