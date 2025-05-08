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

	kcmv1 "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestValidateServiceDependency(t *testing.T) {
	for _, tc := range []struct {
		name        string
		services    []kcmv1.Service
		expectedErr string
	}{
		{
			name: "test1",
		},
		{
			name: "test2",
			services: []kcmv1.Service{
				{Namespace: "nsA", Name: "A"},
				{Namespace: "nsB", Name: "B"},
				{Namespace: "nsC", Name: "C"},
			},
		},
		{
			name: "test3",
			services: []kcmv1.Service{
				{Namespace: "nsA", Name: "A", DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsC", Name: "C"}}},
				{Namespace: "nsB", Name: "B"},
			},
			expectedErr: "dependency nsC/C of service nsA/A is not defined as a service",
		},
		{
			name: "test4",
			services: []kcmv1.Service{
				{Namespace: "nsA", Name: "A", DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsC", Name: "C"}, {Namespace: "nsD", Name: "D"}}},
				{Namespace: "nsB", Name: "B"},
			},
			expectedErr: "dependency nsC/C of service nsA/A is not defined as a service" +
				"\n" + "dependency nsD/D of service nsA/A is not defined as a service",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateServiceDependency(tc.services); err != nil {
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
			testName: "test1",
			services: []kcmv1.Service{},
		},
		{
			testName: "test2",
			services: []kcmv1.Service{
				{
					Namespace: "nsA", Name: "A",
				},
			},
		},
		{
			testName: "test3",
			services: []kcmv1.Service{
				{
					Namespace: "nsA", Name: "A",
				},
				{
					Namespace: "nsA", Name: "A",
				},
			},
		},
		{
			testName: "test4",
			services: []kcmv1.Service{
				{
					Namespace: "nsA", Name: "A",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsB", Name: "B"}},
				},
				{
					Namespace: "nsB", Name: "B",
				},
			},
		},
		{
			testName: "test5",
			services: []kcmv1.Service{
				{
					Namespace: "nsA", Name: "A",
				},
				{
					Namespace: "nsB", Name: "B",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsA", Name: "A"}},
				},
			},
		},
		{
			testName: "test6",
			services: []kcmv1.Service{
				{
					Namespace: "nsA", Name: "A",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsA", Name: "A"}},
				},
			},
			isErr: true,
		},
		{
			testName: "test7",
			services: []kcmv1.Service{
				{
					Namespace: "nsA", Name: "A",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsB", Name: "B"}},
				},
				{
					Namespace: "nsB", Name: "B",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsA", Name: "A"}},
				},
			},
			isErr: true,
		},
		{
			testName: "test8",
			services: []kcmv1.Service{
				{
					Namespace: "nsA", Name: "A",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsB", Name: "B"}, {Namespace: "nsC", Name: "C"}},
				},
				{
					Namespace: "nsB", Name: "B",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsD", Name: "D"}, {Namespace: "nsE", Name: "E"}},
				},
				{
					Namespace: "nsC", Name: "C",
				},
				{
					Namespace: "nsD", Name: "D",
				},
				{
					Namespace: "nsE", Name: "E",
				},
			},
		},
		{
			testName: "test9",
			services: []kcmv1.Service{
				{
					Namespace: "nsA", Name: "A",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsB", Name: "B"}, {Namespace: "nsC", Name: "C"}},
				},
				{
					Namespace: "nsB", Name: "B",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsD", Name: "D"}, {Namespace: "nsE", Name: "E"}},
				},
				{
					Namespace: "nsC", Name: "C",
				},
				{
					Namespace: "nsD", Name: "D",
				},
				{
					Namespace: "nsE", Name: "E",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsA", Name: "A"}},
				},
			},
			isErr: true,
		},
		{
			testName: "test10",
			services: []kcmv1.Service{
				{
					Namespace: "nsC", Name: "C",
				},
				{
					Namespace: "nsA", Name: "A",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsB", Name: "B"}, {Namespace: "nsC", Name: "C"}},
				},
				{
					Namespace: "nsD", Name: "D",
				},
				{
					Namespace: "nsB", Name: "B",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsD", Name: "D"}, {Namespace: "nsE", Name: "E"}},
				},
				{
					Namespace: "nsE", Name: "E",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "nsA", Name: "A"}},
				},
			},
			isErr: true,
		},
	} {
		t.Run(tc.testName, func(t *testing.T) {
			err := ValidateServiceDependencyCycle(tc.services)
			if tc.isErr {
				require.Error(t, err)
			} else {
				require.Nil(t, err)
			}
		})
	}
}
