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

	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
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
		deployedServices *kcmv1.ServiceSet
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
							Versions: []kcmv1.AvailableUpgrade{{Name: "template1-1-0-0", Version: "1.1.0.0"}},
						},
					},
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					AvailableUpgrades: []kcmv1.UpgradePath{
						{
							Versions: []kcmv1.AvailableUpgrade{{Name: "template2-1-0-0", Version: "2.1.0.0"}},
						},
					},
				},
			},
			desiredServices: []kcmv1.Service{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
					Version:   "1.1.0.0",
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					Version:   "2.1.0.0",
				},
			},
			deployedServices: &kcmv1.ServiceSet{},
			expectedServices: []kcmv1.ServiceWithValues{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
					Version:   ptr.To("1.1.0.0"),
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					Version:   ptr.To("2.1.0.0"),
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
							Versions: []kcmv1.AvailableUpgrade{{Name: "template1-1-5-0", Version: "1.1.5.0"}},
						},
					},
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					AvailableUpgrades: []kcmv1.UpgradePath{
						{
							Versions: []kcmv1.AvailableUpgrade{{Name: "template2-1-0-0", Version: "2.1.0.0"}},
						},
					},
				},
			},
			desiredServices: []kcmv1.Service{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-5-0",
					Version:   "1.1.5.0",
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					Version:   "2.1.0.0",
				},
			},
			deployedServices: &kcmv1.ServiceSet{
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{
							State:     kcmv1.ServiceStateDeployed,
							Name:      "service1",
							Namespace: metav1.NamespaceDefault,
							Version:   ptr.To("1.1.0.0"),
						},
						{
							State:     kcmv1.ServiceStateDeployed,
							Name:      "service2",
							Namespace: metav1.NamespaceDefault,
							Version:   ptr.To("2.1.0.0"),
						},
					},
				},
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{
							Name:      "service1",
							Namespace: metav1.NamespaceDefault,
							Template:  "template1-1-0-0",
							Version:   ptr.To("1.1.0.0"),
						},
						{
							Name:      "service2",
							Namespace: metav1.NamespaceDefault,
							Template:  "template2-1-0-0",
							Version:   ptr.To("2.1.0.0"),
						},
					},
				},
			},
			expectedServices: []kcmv1.ServiceWithValues{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-5-0",
					Version:   ptr.To("1.1.5.0"),
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					Version:   ptr.To("2.1.0.0"),
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
							Versions: []kcmv1.AvailableUpgrade{{Name: "template1-1-5-0", Version: "1.1.5.0"}},
						},
					},
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					AvailableUpgrades: []kcmv1.UpgradePath{
						{
							Versions: []kcmv1.AvailableUpgrade{{Name: "template2-1-0-0", Version: "2.1.0.0"}},
						},
					},
				},
			},
			desiredServices: []kcmv1.Service{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-5-0",
					Version:   "1.1.5.0",
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-2-0-0",
					Version:   "2.2.0.0",
				},
			},
			deployedServices: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{
							Name:      "service1",
							Namespace: metav1.NamespaceDefault,
							Template:  "template1-1-0-0",
							Version:   ptr.To("1.1.0.0"),
						},
						{
							Name:      "service2",
							Namespace: metav1.NamespaceDefault,
							Template:  "template2-1-0-0",
							Version:   ptr.To("2.1.0.0"),
						},
					},
				},
			},
			expectedServices: []kcmv1.ServiceWithValues{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
					Version:   ptr.To("1.1.0.0"),
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					Version:   ptr.To("2.1.0.0"),
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
	systemNamespace := "test-system-ns"

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cd",
			Namespace: "test-cd-ns",
		},
	}

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
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cd.GetName()},
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
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cd.GetName()},
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
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cd.GetName()},
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
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cd.GetName()},
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
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cd.GetName()},
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
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cd.GetName()},
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
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cd.GetName()},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName() + "-7sc4gx"},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cd.GetName()},
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
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cd.GetName()},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName() + "-7sc4gx"},
					Spec:       kcmv1.ServiceSetSpec{Cluster: cd.GetName()},
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
				WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetMultiClusterServiceIndexKey, kcmv1.ExtractServiceSetMultiClusterService).
				Build()

			filtered, err := FilterServiceDependencies(t.Context(), client, systemNamespace, nil, cd, testServices2Services(t, tc.desiredServices))
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

	systemNamespace := "test-system-ns"

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cd",
			Namespace: "test-cd-ns",
		},
	}

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
				ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
				Spec:       kcmv1.ServiceSetSpec{Cluster: cd.GetName()},
			}
			ssetMCS := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName() + "gswge"},
				Spec:       kcmv1.ServiceSetSpec{Cluster: cd.GetName()},
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

				filtered, err = FilterServiceDependencies(t.Context(), client, systemNamespace, nil, cd, testServices2Services(t, tc.desiredServices))
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

func TestUtil_StateManagementProviderConfigFromServiceSpec(t *testing.T) {
	t.Parallel()

	type testCase struct {
		description string
		spec        kcmv1.ServiceSpec
		want        kcmv1.StateManagementProviderConfig
	}

	f := func(t *testing.T, tc testCase) {
		actual, err := StateManagementProviderConfigFromServiceSpec(tc.spec)
		require.NoError(t, err)
		require.Equal(t, tc.want, actual)
	}

	testCases := []testCase{
		{
			description: "neither provider name nor config is set",
			spec:        kcmv1.ServiceSpec{
				PolicyRefs: []addoncontrollerv1beta1.PolicyRef{
					{
						Name:           "policy-name",
						Namespace:      "policy-namespace",
						Kind:           "ConfigMap",
						DeploymentType: addoncontrollerv1beta1.DeploymentTypeRemote,
					},
				},
			},
			want:        kcmv1.StateManagementProviderConfig{
				Name: kubeutil.DefaultStateManagementProvider,
				Config: &apiextv1.JSON{
					Raw: []byte(`{"policyRefs":[{"namespace":"policy-namespace","name":"policy-name","kind":"ConfigMap","deploymentType":"Remote"}]}`),
				},
			},
		},
		{
			description: "provider name is not set, config is set",
			spec:        kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Config:         &apiextv1.JSON{
						Raw: []byte(`{"policyRefs":[{"namespace":"policy-namespace","name":"policy-name","kind":"ConfigMap","deploymentType":"Remote"}]}`),
					},
					SelfManagement: false,
				},
			},
			want:        kcmv1.StateManagementProviderConfig{
				Name: kubeutil.DefaultStateManagementProvider,
				Config: &apiextv1.JSON{
					Raw: []byte(`{"policyRefs":[{"namespace":"policy-namespace","name":"policy-name","kind":"ConfigMap","deploymentType":"Remote"}]}`),
				},
			},
		},
		{
			description: "provider name is not set, config is set, deprecated fields are discarded",
			spec:        kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Config:         &apiextv1.JSON{
						Raw: []byte(`{"policyRefs":[{"namespace":"policy-namespace","name":"policy-name","kind":"ConfigMap","deploymentType":"Remote"}]}`),
					},
					SelfManagement: true,
				},
				PolicyRefs: []addoncontrollerv1beta1.PolicyRef{
					{
						Name:           "discarded-policy-name",
						Namespace:      "discarded-policy-namespace",
						Kind:           "ConfigMap",
						DeploymentType: addoncontrollerv1beta1.DeploymentTypeRemote,
					},
				},
			},
			want:        kcmv1.StateManagementProviderConfig{
				Name: kubeutil.DefaultStateManagementProvider,
				Config: &apiextv1.JSON{
					Raw: []byte(`{"policyRefs":[{"namespace":"policy-namespace","name":"policy-name","kind":"ConfigMap","deploymentType":"Remote"}]}`),
				},
				SelfManagement: true,
			},
		},
		{
			description: "provider name is set, config is not set",
			spec:        kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Name: "custom-provider",
					SelfManagement: false,
				},
			},
			want:        kcmv1.StateManagementProviderConfig{
				Name: "custom-provider",
				SelfManagement: false,
			},
		},
		{
			description: "provider name is set, config is not set, self management is set to true",
			spec:        kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Name: "custom-provider",
					SelfManagement: true,
				},
			},
			want:        kcmv1.StateManagementProviderConfig{
				Name: "custom-provider",
				SelfManagement: true,
			},
		},
		{
			description: "provider name is set, config is set",
			spec:        kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Name: "custom-provider",
					Config:         &apiextv1.JSON{
						Raw: []byte(`
{
  "policyRefs":
  [
    {
      "namespace":"policy-namespace",
      "name":"policy-name",
      "kind":"ConfigMap",
      "deploymentType":"Remote"
    }
  ],
  "syncMode":"OneTime",
  "continueOnError":true
}`),
					},
					SelfManagement: false,
				},
			},
			want:        kcmv1.StateManagementProviderConfig{
				Name: "custom-provider",
				Config: &apiextv1.JSON{
					Raw: []byte(`
{
  "policyRefs":
  [
    {
      "namespace":"policy-namespace",
      "name":"policy-name",
      "kind":"ConfigMap",
      "deploymentType":"Remote"
    }
  ],
  "syncMode":"OneTime",
  "continueOnError":true
}`),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			t.Parallel()
			f(t, tc)
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
