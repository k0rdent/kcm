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

package serviceset

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func Test_ServicesToDeploy(t *testing.T) {
	t.Parallel()

	type testCase struct {
		description      string
		upgradePaths     []kcmv1.ServiceUpgradePaths
		desiredServices  []kcmv1.Service
		deployedServices []kcmv1.ServiceWithValues
		expectedServices []kcmv1.ServiceWithValues
	}

	f := func(t *testing.T, tc testCase) {
		t.Helper()
		actualServices := ServicesToDeploy(tc.upgradePaths, tc.desiredServices, tc.deployedServices)
		assert.ElementsMatch(t, tc.expectedServices, actualServices)
	}

	cases := []testCase{
		{
			description: "all-service-to-deploy",
			upgradePaths: []kcmv1.ServiceUpgradePaths{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
					AvailableUpgrades: []kcmv1.UpgradePath{
						{
							Versions: []string{"template1-1-0-0"},
						},
					},
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					AvailableUpgrades: []kcmv1.UpgradePath{
						{
							Versions: []string{"template2-1-0-0"},
						},
					},
				},
			},
			desiredServices: []kcmv1.Service{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
				},
			},
			deployedServices: []kcmv1.ServiceWithValues{},
			expectedServices: []kcmv1.ServiceWithValues{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
				},
			},
		},
		{
			description: "service-to-be-upgraded",
			upgradePaths: []kcmv1.ServiceUpgradePaths{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
					AvailableUpgrades: []kcmv1.UpgradePath{
						{
							Versions: []string{"template1-1-5-0"},
						},
					},
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					AvailableUpgrades: []kcmv1.UpgradePath{
						{
							Versions: []string{"template2-1-0-0"},
						},
					},
				},
			},
			desiredServices: []kcmv1.Service{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-5-0",
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
				},
			},
			deployedServices: []kcmv1.ServiceWithValues{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
				},
			},
			expectedServices: []kcmv1.ServiceWithValues{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-5-0",
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
				},
			},
		},
		{
			description: "service-should-not-be-upgraded",
			upgradePaths: []kcmv1.ServiceUpgradePaths{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
					AvailableUpgrades: []kcmv1.UpgradePath{
						{
							Versions: []string{"template1-1-5-0"},
						},
					},
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					AvailableUpgrades: []kcmv1.UpgradePath{
						{
							Versions: []string{"template2-1-0-0"},
						},
					},
				},
			},
			desiredServices: []kcmv1.Service{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-5-0",
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-2-0-0",
				},
			},
			deployedServices: []kcmv1.ServiceWithValues{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
				},
			},
			expectedServices: []kcmv1.ServiceWithValues{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-5-0",
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			t.Parallel()
			f(t, tc)
		})
	}
}

func Test_FilterServiceDependencies(t *testing.T) {
	cdNamespace, cdName := "cd1-ns", "cd1"

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kcmv1.AddToScheme(scheme))

	a := testService{kcmv1.Service{Namespace: "A", Name: "a"}}
	b := testService{kcmv1.Service{Namespace: "B", Name: "b"}}

	for _, tc := range []struct {
		testName        string
		desiredServices []testService
		objects         []client.Object
		expected        []testService
	}{
		{
			testName: "empty",
		},
		{
			testName:        "service A provisioning",
			desiredServices: []testService{a},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cdNamespace, Name: cdName},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cdName},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateProvisioning},
						},
					},
				},
			},
			expected: []testService{a},
		},
		{
			testName: "service A deployed",
			desiredServices: []testService{
				a,
			},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cdNamespace, Name: cdName},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cdName},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
			},
			expected: []testService{a},
		},
		{
			testName:        "service A provisioning when B->A",
			desiredServices: []testService{a, b.dependsOn(a)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cdNamespace, Name: cdName},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cdName},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateProvisioning},
						},
					},
				},
			},
			expected: []testService{a},
		},
		{
			testName:        "service A deployed when B->A",
			desiredServices: []testService{a, b.dependsOn(a)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cdNamespace, Name: cdName},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cdName},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
			},
			expected: []testService{a, b},
		},
		{
			testName: "service A deployed & B provisioning when B->A",
			desiredServices: []testService{
				a,
				b.dependsOn(a),
			},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cdNamespace, Name: cdName},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cdName},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateDeployed},
							{Namespace: b.Namespace, Name: b.Name, State: kcmv1.ServiceStateProvisioning},
						},
					},
				},
			},
			expected: []testService{a, b},
		},
		{
			testName:        "service A & B deployed when B->A",
			desiredServices: []testService{a, b.dependsOn(a)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cdNamespace, Name: cdName},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cdName},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateDeployed},
							{Namespace: b.Namespace, Name: b.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
			},
			expected: []testService{a, b},
		},
		{
			testName:        "service A deployed & B provisioning in different servicesets when B->A",
			desiredServices: []testService{a, b.dependsOn(a)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cdNamespace, Name: cdName},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cdName},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cdNamespace, Name: cdName + "-7sc4gx"},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cdName},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: b.Namespace, Name: b.Name, State: kcmv1.ServiceStateProvisioning},
						},
					},
				},
			},
			expected: []testService{a, b},
		},
		{
			testName:        "service A & B deployed in different servicesets when B->A",
			desiredServices: []testService{a, b.dependsOn(a)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cdNamespace, Name: cdName},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cdName},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cdNamespace, Name: cdName + "-7sc4gx"},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cdName},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: b.Namespace, Name: b.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
			},
			expected: []testService{a, b},
		},
	} {
		t.Run(tc.testName, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.objects...).
				WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetClusterIndexKey, kcmv1.ExtractServiceSetCluster).
				Build()

			filtered, err := FilterServiceDependencies(t.Context(), client, cdNamespace, cdName, testServices2Services(t, tc.desiredServices))
			require.NoError(t, err)
			require.Len(t, tc.expected, len(filtered))
			require.ElementsMatch(t, relevantFields(t, testServices2Services(t, tc.expected)), relevantFields(t, filtered))
		})
	}
}

func Test_FilterServiceDependencies_Operation(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kcmv1.AddToScheme(scheme))

	cdNamespace, cdName := "cd1-ns", "cd1"

	a := testService{kcmv1.Service{Namespace: "A", Name: "a"}}
	b := testService{kcmv1.Service{Namespace: "B", Name: "b"}}
	c := testService{kcmv1.Service{Namespace: "C", Name: "c"}}
	d := testService{kcmv1.Service{Namespace: "D", Name: "d"}}
	e := testService{kcmv1.Service{Namespace: "E", Name: "e"}}
	f := testService{kcmv1.Service{Namespace: "F", Name: "f"}}
	g := testService{kcmv1.Service{Namespace: "G", Name: "g"}}

	for _, tc := range []struct {
		testName        string
		desiredServices []testService
		// expectedServices is a list of services (from desiredServices) expected
		// to be returned at every iteration of the FilterServiceDependencies call.
		expectedServices [][]testService
	}{
		{
			testName: "empty",
		},
		{
			testName:        "single service",
			desiredServices: []testService{a},
			expectedServices: [][]testService{
				0: {a},
			},
		},
		{
			testName: "services B->A",
			desiredServices: []testService{
				a,
				b.dependsOn(a),
			},
			expectedServices: [][]testService{
				0: {a},
				1: {a, b},
			},
		},
		{
			testName: "services A->D, B->D, C->EF, D->E, E, F->E, G",
			desiredServices: []testService{
				a.dependsOn(d),
				b.dependsOn(d),
				c.dependsOn(e, f),
				d.dependsOn(e),
				e,
				f.dependsOn(e),
				g,
			},
			expectedServices: [][]testService{
				0: {e, g},
				1: {e, g, d, f},
				2: {e, g, d, f, c, a, b},
			},
		},
	} {
		t.Run(tc.testName, func(t *testing.T) {
			var err error
			var filtered []kcmv1.Service

			ssetCD := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: cdNamespace, Name: cdName},
				Spec:       kcmv1.ServiceSetSpec{Cluster: cdName},
			}
			ssetMCS := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: cdNamespace, Name: cdName + "gswge"},
				Spec:       kcmv1.ServiceSetSpec{Cluster: cdName},
			}

			for itr := range tc.expectedServices {
				// Divide expected services between 2 ServiceSets targeting the same cluster,
				// where one serviceset belongs to ClusterDeployment and the other to MultiClusterService.
				for j, svc := range filtered {
					sstate := kcmv1.ServiceState{
						Namespace: svc.Namespace, Name: svc.Name, State: kcmv1.ServiceStateDeployed,
					}
					if j%2 == 0 {
						ssetCD.Status.Services = append(ssetCD.Status.Services, sstate)
					} else {
						ssetMCS.Status.Services = append(ssetMCS.Status.Services, sstate)
					}
				}

				client := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(ssetCD, ssetMCS).
					WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetClusterIndexKey, kcmv1.ExtractServiceSetCluster).
					Build()

				filtered, err = FilterServiceDependencies(t.Context(), client, cdNamespace, cdName, testServices2Services(t, tc.desiredServices))
				require.NoError(t, err)
				// For each iteration of desiredServices being filtered wrt dependencies,
				// we expect the returned filtered services to match the expected services.
				require.ElementsMatch(t,
					relevantFields(t, testServices2Services(t, tc.expectedServices[itr])),
					relevantFields(t, filtered),
				)
			}
		})
	}
}

// testService is only used for testing purposes.
// TODO: Maybe can be used in a non-test file if we find it
// useful in making test related to services more readable.
type testService struct {
	kcmv1.Service
}

func (s testService) dependsOn(services ...testService) testService {
	for _, d := range services {
		s.DependsOn = append(s.DependsOn, kcmv1.ServiceDependsOn{
			Namespace: d.Namespace, Name: d.Name,
		})
	}
	return s
}

func testServices2Services(t *testing.T, services []testService) []kcmv1.Service {
	t.Helper()
	ret := []kcmv1.Service{}
	for _, svc := range services {
		ret = append(ret, kcmv1.Service{
			Namespace: svc.Namespace, Name: svc.Name, DependsOn: svc.DependsOn,
		})
	}
	return ret
}

func relevantFields(t *testing.T, services []kcmv1.Service) []map[client.ObjectKey]struct{} {
	t.Helper()
	result := make([]map[client.ObjectKey]struct{}, len(services))
	for i, svc := range services {
		result[i] = map[client.ObjectKey]struct{}{
			ServiceKey(svc.Namespace, svc.Name): {},
		}
	}
	return result
}
