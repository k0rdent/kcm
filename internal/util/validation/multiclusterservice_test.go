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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
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
			expectedErr: "dependency b of a is not defined",
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
			expectedErr: "dependency c of a is not defined",
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
			if err := validateMCSDependency(tc.mcs, mcsList2mcsCommonList(tc.mcsList)); err != nil {
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
			// Make A the starting node. Even though it has a cycle C<->B,
			// the validation should pass because A is the starting node
			// so only the subgraph A->D will be validated for a cycle.
			testName: "mcs C<->B->A->D starting at A",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "a"},
				Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"d"}},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "d"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "b"},
						Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a", "c"}},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "c"},
						Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}},
					},
				},
			},
		},
		{
			// Since starting node is B the validation will detect the cycle.
			testName: "mcs C<->B->A->D starting at B",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "b"},
				Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a", "c"}},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "a"},
						Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"d"}},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "d"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "c"},
						Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}},
					},
				},
			},
			isErr: true,
		},
		{
			testName: "mcs BC->A, D->BC",
			mcs: &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: "d"},
				Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b", "c"}},
			},
			mcsList: &kcmv1.MultiClusterServiceList{
				Items: []kcmv1.MultiClusterService{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "a"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "b"},
						Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "c"},
						Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"a"}},
					},
				},
			},
			isErr: false,
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
			err := validateMCSDependencyCycle(tc.mcs, mcsList2mcsCommonList(tc.mcsList))
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
			graph := generateMCSDependencyGraph(mcsList2mcsCommonList(tc.mcsList))
			if !equality.Semantic.DeepEqual(graph, tc.expectedGraph) {
				t.Errorf("generateMCSDependencyGraph(%s): \n\texpected:\n\t%v\n\n\tactual:\n\t%v", tc.testName, tc.expectedGraph, graph)
			}
		})
	}
}

func TestGenerateReverseMCSDependencyGraph(t *testing.T) {
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
			graph := generateReverseMCSDependencyGraph(mcsList2mcsCommonList(tc.mcsList))
			if !equality.Semantic.DeepEqual(graph, tc.expectedGraph) {
				t.Errorf("generateMCSDependencyGraph(%s): \n\texpected:\n\t%v\n\n\tactual:\n\t%v", tc.testName, tc.expectedGraph, graph)
			}
		})
	}
}

func mcsList2mcsCommonList(mcsList *kcmv1.MultiClusterServiceList) []kcmv1.MultiClusterServiceCommon {
	if mcsList == nil {
		return nil
	}

	list := make([]kcmv1.MultiClusterServiceCommon, len(mcsList.Items))
	for i, mcs := range mcsList.Items {
		list[i] = &mcs
	}

	return list
}

// Test_fetchMCSCommon guards fetchMCSCommon's two, currently namespace-scoping-dependent
// branches: a NamespacedMultiClusterService must only ever see NamespacedMultiClusterService
// siblings from its own namespace (never a cluster-scoped MultiClusterService, never a
// same-named NamespacedMultiClusterService from a different namespace), while a MultiClusterService
// sees every MultiClusterService cluster-wide and no NamespacedMultiClusterService at all.
func Test_fetchMCSCommon(t *testing.T) {
	t.Parallel()

	nsAB := &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: "ns-a", Name: "b"}}
	nsAC := &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: "ns-a", Name: "c"}}
	// Same name ("b") as nsAB, but a different namespace - must not leak into ns-a's list.
	nsBB := &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: "ns-b", Name: "b"}}
	clusterMCS := &kcmv1.MultiClusterService{ObjectMeta: metav1.ObjectMeta{Name: "cluster-scoped"}}

	cl := fake.NewClientBuilder().WithScheme(testscheme.Scheme).
		WithObjects(nsAB, nsAC, nsBB, clusterMCS).
		Build()

	namesOf := func(list []kcmv1.MultiClusterServiceCommon) []string {
		names := make([]string, len(list))
		for i, m := range list {
			names[i] = m.GetFullname()
		}
		return names
	}

	t.Run("NamespacedMultiClusterService sees only its own namespace, not cluster-scoped MCS", func(t *testing.T) {
		t.Parallel()

		subject := &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: "ns-a", Name: "a"}}
		got, err := fetchMCSCommon(t.Context(), cl, subject)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"ns-a/b", "ns-a/c"}, namesOf(got))
	})

	t.Run("NamespacedMultiClusterService in an empty namespace sees no siblings", func(t *testing.T) {
		t.Parallel()

		subject := &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: "ns-empty", Name: "a"}}
		got, err := fetchMCSCommon(t.Context(), cl, subject)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("MultiClusterService sees every MultiClusterService cluster-wide, no NamespacedMultiClusterService", func(t *testing.T) {
		t.Parallel()

		subject := &kcmv1.MultiClusterService{ObjectMeta: metav1.ObjectMeta{Name: "a"}}
		got, err := fetchMCSCommon(t.Context(), cl, subject)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"cluster-scoped"}, namesOf(got))
	})
}

// Test_ValidateMCSDependencyOverall_namespaced is a regression guard for cross-namespace
// dependency-name collisions: a NamespacedMultiClusterService's DependsOn must resolve against
// its own namespace only, so a same-named NamespacedMultiClusterService living in another
// namespace must never be mistaken for satisfying the dependency.
func Test_ValidateMCSDependencyOverall_namespaced(t *testing.T) {
	t.Parallel()

	t.Run("dependency satisfied by a same-namespace sibling", func(t *testing.T) {
		t.Parallel()

		b := &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: "ns-a", Name: "b"}}
		cl := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(b).Build()

		a := &kcmv1.NamespacedMultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns-a", Name: "a"},
			Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}},
		}
		require.NoError(t, ValidateMCSDependencyOverall(t.Context(), cl, a))
	})

	t.Run("same-named object in a different namespace does not satisfy the dependency", func(t *testing.T) {
		t.Parallel()

		bInOtherNamespace := &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: "ns-other", Name: "b"}}
		cl := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(bInOtherNamespace).Build()

		a := &kcmv1.NamespacedMultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns-a", Name: "a"},
			Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}},
		}
		err := ValidateMCSDependencyOverall(t.Context(), cl, a)
		require.Error(t, err)
		require.Contains(t, err.Error(), "dependency ns-a/b of ns-a/a is not defined")
	})
}

// Test_ValidateMCSDelete_namespaced mirrors Test_ValidateMCSDependencyOverall_namespaced for the
// reverse-dependency (delete-blocking) direction: a NamespacedMultiClusterService dependent in one
// namespace must not block deletion of a same-named NamespacedMultiClusterService in another.
func Test_ValidateMCSDelete_namespaced(t *testing.T) {
	t.Parallel()

	t.Run("blocked while a same-namespace dependent still exists", func(t *testing.T) {
		t.Parallel()

		b := &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: "ns-a", Name: "b"}}
		a := &kcmv1.NamespacedMultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns-a", Name: "a"},
			Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}},
		}
		cl := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(a, b).Build()

		err := ValidateMCSDelete(t.Context(), cl, b)
		require.Error(t, err)
		require.Contains(t, err.Error(), "ns-a/b")
	})

	t.Run("a dependent in a different namespace does not block deletion", func(t *testing.T) {
		t.Parallel()

		b := &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: "ns-a", Name: "b"}}
		// Same name ("a") depending on "b", but in a different namespace - its DependsOn
		// resolves against its own namespace's "b", not ns-a's, so it must not block this delete.
		aInOtherNamespace := &kcmv1.NamespacedMultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns-other", Name: "a"},
			Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"b"}},
		}
		cl := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(b, aInOtherNamespace).Build()

		require.NoError(t, ValidateMCSDelete(t.Context(), cl, b))
	})

	t.Run("deletion allowed once the dependent is gone", func(t *testing.T) {
		t.Parallel()

		b := &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: "ns-a", Name: "b"}}
		cl := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(b).Build()

		require.NoError(t, ValidateMCSDelete(t.Context(), cl, b))
	})
}
