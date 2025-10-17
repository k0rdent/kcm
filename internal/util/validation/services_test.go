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

	"github.com/stretchr/testify/require"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
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
			name: "golden path",
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
			testName: "single service illegally repeated",
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
