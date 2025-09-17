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

package validation

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func TestValidateMCSDependency(t *testing.T) {
	for _, tc := range []struct {
		testName    string
		mcs         *kcmv1.MultiClusterService
		mcsList     *kcmv1.MultiClusterServiceList
		expectedErr string
	}{
		{
			testName: "empty",
		},
		{
			testName: "single mcs",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
			},
		},
		{
			testName: "mcs A->B but B doesn't exist",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
				Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}},
			},
			expectedErr: "dependency /b of /a is not defined",
		},
		{
			testName: "mcs A->B and B exists",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
				Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}},
				},
			},
		},
		{
			testName: "A->BC and B exists and C does not exist",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
				Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}},
				},
			},
			expectedErr: "dependency /c of /a is not defined",
		},
		{
			testName: "A->BC and B exists and C exists",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
				Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "c"}},
				},
			},
		},
	} {
		t.Run(tc.testName, func(t *testing.T) {
			if err := validateMCSDependency(tc.mcs, tc.mcsList); err != nil {
				require.EqualError(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateMCSDependencyCycle(t *testing.T) {
	for _, tc := range []struct {
		testName string
		mcs      *kcmv1.MultiClusterService
		mcsList  *kcmv1.MultiClusterServiceList
		isErr    bool
	}{
		{
			testName: "empty",
		},
		{
			testName: "single mcs",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
			},
		},
		{
			testName: "mcs A->B",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
				Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "b"},
					},
				},
			},
		},
		{
			testName: "mcs B->A",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "b"},
						Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}},
					},
				},
			},
		},
		{
			testName: "mcs A->A",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
				Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}},
			},
			isErr: true,
		},
		{
			testName: "mcs A<->B",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
				Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "b"},
						Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}},
					},
				},
			},
			isErr: true,
		},
		{
			testName: "mcs A->BC, B->DE, C, D, E",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
				Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"d", "e"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "c"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "d"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "e"}},
				},
			},
		},
		{
			testName: "mcs A->BC, B->DE, C, D, E->A",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
				Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"d", "e"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "c"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "d"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "e"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}}},
				},
			},
			isErr: true,
		},
		{
			// Even though this has a cycle, the function won't return an error
			// because the starting point C does not depend on any other MCS.
			testName: "mcs C, A->BC, D, B->DE, E->A",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "c"},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "d"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"d", "e"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "e"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}}},
				},
			},
		},
		{
			testName: "mcs C->B, A->BC, D, B->DE, E->A",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "c"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "d"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"d", "e"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "e"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}}},
				},
			},
			isErr: true,
		},
	} {
		t.Run(tc.testName, func(t *testing.T) {
			err := validateMCSDependencyCycle(tc.mcs, tc.mcsList)
			if tc.isErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGenerateMCSDependencyGraph(t *testing.T) {
	for _, tc := range []struct {
		testName      string
		mcsList       *kcmv1.MultiClusterServiceList
		expectedGraph map[client.ObjectKey][]client.ObjectKey
	}{
		{
			testName: "empty",
		},
		{
			testName: "with no items",
			mcsList:  &kcmv1.MultiClusterServiceList{},
		},
		{
			testName: "returned graph should contain MCS as key even if it has 0 dependents",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: nil,
			},
		},
		{
			testName: "illegal A->A should still return correct graph",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: {{Name: "a"}},
			},
		},
		{
			testName: "illegal A<->B should still return correct graph",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: {{Name: "b"}},
				{Name: "b"}: {{Name: "a"}},
			},
		},
		{
			testName: "A->BC with B and C not defined",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: {{Name: "b"}, {Name: "c"}},
			},
		},
		{
			testName: "A->BC",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "c"}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: {{Name: "b"}, {Name: "c"}},
				{Name: "b"}: nil,
				{Name: "c"}: nil,
			},
		},
		{
			testName: "A->B->C",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"c"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "c"}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: {{Name: "b"}},
				{Name: "b"}: {{Name: "c"}},
				{Name: "c"}: nil,
			},
		},
		{
			testName: "A->BC, B->DE, C, D, E",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"d", "e"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "c"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "d"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "e"}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: {{Name: "b"}, {Name: "c"}},
				{Name: "b"}: {{Name: "d"}, {Name: "e"}},
				{Name: "c"}: nil,
				{Name: "d"}: nil,
				{Name: "e"}: nil,
			},
		},
		{
			testName: "A->BC, B->DE, C, D, E->A",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"d", "e"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "c"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "d"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "e"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: {{Name: "b"}, {Name: "c"}},
				{Name: "b"}: {{Name: "d"}, {Name: "e"}},
				{Name: "c"}: nil,
				{Name: "d"}: nil,
				{Name: "e"}: {{Name: "a"}},
			},
		},
	} {
		t.Run(tc.testName, func(t *testing.T) {
			graph := generateMCSDependencyGraph(tc.mcsList)
			if !equality.Semantic.DeepEqual(graph, tc.expectedGraph) {
				t.Errorf("generateMCSDependencyGraph(%s): \n\texpected:\n\t%v\n\n\tactual:\n\t%v", tc.testName, tc.expectedGraph, graph)
			}
		})
	}
}

func TestGenerateReserveMCSDependencyGraph(t *testing.T) {
	for _, tc := range []struct {
		testName      string
		mcsList       *kcmv1.MultiClusterServiceList
		expectedGraph map[client.ObjectKey][]client.ObjectKey
	}{
		{
			testName: "empty",
		},
		{
			testName: "with no items",
			mcsList:  &kcmv1.MultiClusterServiceList{},
		},
		{
			testName: "returned graph should contain MCS as key even if it has 0 dependents",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: nil,
			},
		},
		{
			testName: "illegal A->A should still return correct graph",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: {{Name: "a"}},
			},
		},
		{
			testName: "illegal A<->B should still return correct graph",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: {{Name: "b"}},
				{Name: "b"}: {{Name: "a"}},
			},
		},
		{
			testName: "A->BC with B and C not defined",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: nil,
				{Name: "b"}: {{Name: "a"}},
				{Name: "c"}: {{Name: "a"}},
			},
		},
		{
			testName: "A->BC",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "c"}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: nil,
				{Name: "b"}: {{Name: "a"}},
				{Name: "c"}: {{Name: "a"}},
			},
		},
		{
			testName: "A->B->C",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"c"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "c"}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: nil,
				{Name: "b"}: {{Name: "a"}},
				{Name: "c"}: {{Name: "b"}},
			},
		},
		{
			testName: "A->B, C->B",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "c"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: nil,
				{Name: "b"}: {{Name: "a"}, {Name: "c"}},
				{Name: "c"}: nil,
			},
		},
		{
			testName: "A->BC, B->DE, C, D, E",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"d", "e"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "c"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "d"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "e"}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: nil,
				{Name: "b"}: {{Name: "a"}},
				{Name: "c"}: {{Name: "a"}},
				{Name: "d"}: {{Name: "b"}},
				{Name: "e"}: {{Name: "b"}},
			},
		},
		{
			testName: "A->BC, B->DE, C, D, E->A",
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"d", "e"}}},
					{ObjectMeta: metav1.ObjectMeta{Name: "c"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "d"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "e"}, Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}}},
				},
			},
			expectedGraph: map[client.ObjectKey][]client.ObjectKey{
				{Name: "a"}: {{Name: "e"}},
				{Name: "b"}: {{Name: "a"}},
				{Name: "c"}: {{Name: "a"}},
				{Name: "d"}: {{Name: "b"}},
				{Name: "e"}: {{Name: "b"}},
			},
		},
	} {
		t.Run(tc.testName, func(t *testing.T) {
			graph := generateReserveMCSDependencyGraph(tc.mcsList)
			if !equality.Semantic.DeepEqual(graph, tc.expectedGraph) {
				t.Errorf("generateMCSDependencyGraph(%s): \n\texpected:\n\t%v\n\n\tactual:\n\t%v", tc.testName, tc.expectedGraph, graph)
			}
		})
	}
}
