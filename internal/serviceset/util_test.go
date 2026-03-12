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
	"context"
	"testing"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

type fakeFieldIndexer struct {
	builder *fake.ClientBuilder
}

func (f *fakeFieldIndexer) IndexField(
	_ context.Context,
	obj client.Object,
	field string,
	extractValue client.IndexerFunc,
) error {
	f.builder.WithIndex(obj, field, extractValue)
	return nil
}

func Test_GetServiceSetWithOperation(t *testing.T) {
	ctx := context.Background()

	scheme := runtime.NewScheme()
	require.NoError(t, kcmv1.AddToScheme(scheme))

	key := client.ObjectKey{
		Name:      "test-serviceset",
		Namespace: "default",
	}

	provider := &kcmv1.StateManagementProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ksm-projectsveltos",
		},
		Spec: kcmv1.StateManagementProviderSpec{
			Selector: &metav1.LabelSelector{},
		},
		Status: kcmv1.StateManagementProviderStatus{
			Ready: true,
		},
	}

	template := &kcmv1.ServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: kcmv1.ClusterDeploymentSpec{
			ServiceSpec: kcmv1.ServiceSpec{
				Services: []kcmv1.Service{
					{
						Name:      "test-service",
						Namespace: "default",
						Template:  "test",
					},
				},
			},
		},
	}

	tests := []struct {
		name         string
		existingObjs []client.Object
		operationReq OperationRequisites
		expectOp     kcmv1.ServiceSetOperation
		expectErr    bool
	}{
		{
			name: "serviceset not found -> create",
			existingObjs: []client.Object{
				provider,
				template,
			},
			operationReq: OperationRequisites{
				ObjectKey: key,
				CD:        cd,
			},
			expectOp: kcmv1.ServiceSetOperationCreate,
		},
		{
			name: "serviceset exists -> none",
			existingObjs: []client.Object{
				provider,
				template,
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      key.Name,
						Namespace: key.Namespace,
					},
					Spec: kcmv1.ServiceSetSpec{
						Cluster: cd.Name,
						Provider: kcmv1.StateManagementProviderConfig{
							Config: &apiextv1.JSON{
								Raw: []byte("{}"),
							},
							Name:           provider.Name,
							SelfManagement: false,
						},
						Services: []kcmv1.ServiceWithValues{
							{
								Name:      cd.Spec.ServiceSpec.Services[0].Name,
								Namespace: cd.Spec.ServiceSpec.Services[0].Namespace,
								Template:  cd.Spec.ServiceSpec.Services[0].Template,
								Version:   &cd.Spec.ServiceSpec.Services[0].Template,
							},
						},
					},
				},
			},
			operationReq: OperationRequisites{
				ObjectKey: key,
				CD:        cd,
			},
			expectOp: kcmv1.ServiceSetOperationNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if len(tt.existingObjs) > 0 {
				builder = builder.WithObjects(tt.existingObjs...)
			}
			indexer := &fakeFieldIndexer{
				builder: builder,
			}

			require.NoError(t, kcmv1.SetupFieldIndexers(ctx, indexer))
			c := builder.Build()
			serviceSet, op, err := GetServiceSetWithOperation(ctx, c, tt.operationReq)

			if tt.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.expectOp, op)
			require.NotNil(t, serviceSet)

			if op == kcmv1.ServiceSetOperationCreate {
				require.Equal(t, key.Name, serviceSet.Name)
				require.Equal(t, key.Namespace, serviceSet.Namespace)
			}
		})
	}
}

func Test_FillServiceVersions(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, kcmv1.AddToScheme(scheme))
	ctx := context.Background()
	namespace := "test-ns"
	tests := []struct {
		name        string
		templates   []runtime.Object
		services    []kcmv1.Service
		expectErr   bool
		expectedVer string
	}{
		{
			name: "version from spec.version",
			templates: []runtime.Object{
				&kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tmpl1",
						Namespace: namespace,
					},
					Spec: kcmv1.ServiceTemplateSpec{
						Version: "1.2.3",
					},
				},
			},
			services: []kcmv1.Service{
				{
					Template: "tmpl1",
				},
			},
			expectedVer: "1.2.3",
		},
		{
			name: "version from helm chart",
			templates: []runtime.Object{
				&kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tmpl2",
						Namespace: namespace,
					},
					Spec: kcmv1.ServiceTemplateSpec{
						Helm: &kcmv1.HelmSpec{
							ChartSpec: &sourcev1.HelmChartSpec{
								Version: "9.9.9",
							},
						},
					},
				},
			},
			services: []kcmv1.Service{
				{
					Template: "tmpl2",
				},
			},
			expectedVer: "9.9.9",
		},
		{
			name: "fallback to template name",
			templates: []runtime.Object{
				&kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tmpl3",
						Namespace: namespace,
					},
				},
			},
			services: []kcmv1.Service{
				{
					Template: "tmpl3",
				},
			},
			expectedVer: "tmpl3",
		},
		{
			name:      "already has version",
			templates: []runtime.Object{},
			services: []kcmv1.Service{
				{
					Version:  "existing",
					Template: "tmpl4",
				},
			},
			expectedVer: "existing",
		},
		{
			name:      "template not found",
			templates: []runtime.Object{},
			services: []kcmv1.Service{
				{
					Template: "missing",
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if len(tt.templates) > 0 {
				builder = builder.WithRuntimeObjects(tt.templates...)
			}

			cl := builder.Build()

			err := fillServiceVersions(ctx, cl, namespace, tt.services)
			if tt.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.expectedVer, tt.services[0].Version)
		})
	}
}

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
					Version:   new("1.1.0.0"),
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					Version:   new("2.1.0.0"),
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
							Version:   new("1.1.0.0"),
						},
						{
							State:     kcmv1.ServiceStateDeployed,
							Name:      "service2",
							Namespace: metav1.NamespaceDefault,
							Version:   new("2.1.0.0"),
						},
					},
				},
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{
							Name:      "service1",
							Namespace: metav1.NamespaceDefault,
							Template:  "template1-1-0-0",
							Version:   new("1.1.0.0"),
						},
						{
							Name:      "service2",
							Namespace: metav1.NamespaceDefault,
							Template:  "template2-1-0-0",
							Version:   new("2.1.0.0"),
						},
					},
				},
			},
			expectedServices: []kcmv1.ServiceWithValues{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-5-0",
					Version:   new("1.1.5.0"),
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					Version:   new("2.1.0.0"),
				},
			},
		},
		{
			// Regression test: when a service's initial deployment fails, the version
			// in the ServiceSet spec is set but no "Deployed" entry appears in status.
			// A subsequent spec update (e.g., corrected helm values) must be reflected
			// in the resulting services instead of being silently discarded.
			description: "values-updated-after-failed-deploy",
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
			},
			desiredServices: []kcmv1.Service{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
					Version:   "1.1.0.0",
					// Corrected values after the failed deploy.
					Values: "replicaCount: 1\n",
				},
			},
			deployedServices: &kcmv1.ServiceSet{
				// Status has no Deployed entry — the deploy failed.
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{
							State:     kcmv1.ServiceStateFailed,
							Name:      "service1",
							Namespace: metav1.NamespaceDefault,
						},
					},
				},
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{
							Name:      "service1",
							Namespace: metav1.NamespaceDefault,
							Template:  "template1-1-0-0",
							Version:   new("1.1.0.0"),
							// Wrong values that caused the failure.
							Values: "replicaCount: two\n",
						},
					},
				},
			},
			expectedServices: []kcmv1.ServiceWithValues{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
					// Version is preserved from the ServiceSet spec (in-flight tracking).
					Version: new("1.1.0.0"),
					// Values must reflect the updated desired spec.
					Values: "replicaCount: 1\n",
				},
			},
		},
		{
			// Regression test: all mutable fields (ValuesFrom, HelmOptions, HelmAction) must
			// also be propagated from the desired spec when the in-flight path is taken.
			description: "mutable-fields-updated-after-failed-deploy",
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
			},
			desiredServices: []kcmv1.Service{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
					Version:   "1.1.0.0",
					ValuesFrom: []kcmv1.ValuesFrom{
						{Kind: "ConfigMap", Name: "my-config"},
					},
				},
			},
			deployedServices: &kcmv1.ServiceSet{
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{
							State:     kcmv1.ServiceStateFailed,
							Name:      "service1",
							Namespace: metav1.NamespaceDefault,
						},
					},
				},
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{
							Name:      "service1",
							Namespace: metav1.NamespaceDefault,
							Template:  "template1-1-0-0",
							Version:   new("1.1.0.0"),
							Values:    "replicaCount: two\n",
						},
					},
				},
			},
			expectedServices: []kcmv1.ServiceWithValues{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
					Version:   new("1.1.0.0"),
					ValuesFrom: []kcmv1.ValuesFrom{
						{Kind: "ConfigMap", Name: "my-config"},
					},
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
							Version:   new("1.1.0.0"),
						},
						{
							Name:      "service2",
							Namespace: metav1.NamespaceDefault,
							Template:  "template2-1-0-0",
							Version:   new("2.1.0.0"),
						},
					},
				},
			},
			expectedServices: []kcmv1.ServiceWithValues{
				{
					Name:      "service1",
					Namespace: metav1.NamespaceDefault,
					Template:  "template1-1-0-0",
					Version:   new("1.1.0.0"),
				},
				{
					Name:      "service2",
					Namespace: metav1.NamespaceDefault,
					Template:  "template2-1-0-0",
					Version:   new("2.1.0.0"),
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
	c := testService{kcmv1.Service{Namespace: "C", Name: "c"}}

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
		{
			// This means that service A and B were deployed successfully and
			// then later on A's state changed to other than Deployed (maybe Failed or Pending).
			// Now the fact that B being a dependent of A is still present in the ServiceSet's spec
			// (irrespective of its state) means that A's state was Deployed sometime in the past.
			// Therefore, the dependents of A should be added to the ServiceSet's spec because
			// we don't want any Deployed or Pending dependent service of A to be uninstalled by
			// not including it is the ServiceSet's spec.
			testName:        "service A currently !Deployed with B,C->A and B is currently Deployed",
			desiredServices: []testService{a, b.dependsOn(a), c.dependsOn(a)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec: kcmv1.ServiceSetSpec{
						Cluster: cd.GetName(),
						Services: []kcmv1.ServiceWithValues{
							{Namespace: a.Namespace, Name: a.Name},
							{Namespace: b.Namespace, Name: b.Name},
						},
					},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateFailed},
							{Namespace: b.Namespace, Name: b.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
			},
			expected: []testService{a, b, c},
		},
		{
			testName:        "service A currently !Deployed with B,C->A and C is currently !Deployed",
			desiredServices: []testService{a, b.dependsOn(a), c.dependsOn(a)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec: kcmv1.ServiceSetSpec{
						Cluster: cd.GetName(),
						Services: []kcmv1.ServiceWithValues{
							{Namespace: a.Namespace, Name: a.Name},
							{Namespace: c.Namespace, Name: c.Name},
						},
					},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateFailed},
							{Namespace: c.Namespace, Name: c.Name, State: kcmv1.ServiceStateProvisioning},
						},
					},
				},
			},
			expected: []testService{a, b, c},
		},
		{
			// In this case A was originally Deployed triggering deployment of B which
			// is successfully Deployed. Then sometime later A became !Deployed.
			testName:        "service A currently !Deployed with C->B->A and B is Deployed",
			desiredServices: []testService{a, b.dependsOn(a), c.dependsOn(b)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec: kcmv1.ServiceSetSpec{
						Cluster: cd.GetName(),
						Services: []kcmv1.ServiceWithValues{
							{Namespace: a.Namespace, Name: a.Name},
							{Namespace: b.Namespace, Name: b.Name},
						},
					},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateFailed},
							{Namespace: b.Namespace, Name: b.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
			},
			expected: []testService{a, b, c},
		},
		{
			// In this case A was originally Deployed triggering deployment of B
			// which either Failed or is still Provisioning (doesn't matter which as long as !Deployed),
			// so C was never added to the ServiceSet's spec. Then sometime later A became !Deployed.
			testName:        "service A currently !Deployed with C->B->A and B is currently !Deployed",
			desiredServices: []testService{a, b.dependsOn(a), c.dependsOn(b)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec: kcmv1.ServiceSetSpec{
						Cluster: cd.GetName(),
						Services: []kcmv1.ServiceWithValues{
							{Namespace: a.Namespace, Name: a.Name},
							{Namespace: b.Namespace, Name: b.Name},
						},
					},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateFailed},
							{Namespace: b.Namespace, Name: b.Name, State: kcmv1.ServiceStateProvisioning},
						},
					},
				},
			},
			expected: []testService{a, b},
		},
		{
			// In this case A was originally Deployed triggering deployment of B which
			// was successfully Deployed triggering the deployment of C (meaning C was included
			// in the ServiceSet's spec). Then sometime later A and B both became !Deployed.
			testName:        "service A currently !Deployed with C->B->A and B is currently !Deployed",
			desiredServices: []testService{a, b.dependsOn(a), c.dependsOn(b)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec: kcmv1.ServiceSetSpec{
						Cluster: cd.GetName(),
						Services: []kcmv1.ServiceWithValues{
							{Namespace: a.Namespace, Name: a.Name},
							{Namespace: b.Namespace, Name: b.Name},
							{Namespace: c.Namespace, Name: c.Name},
						},
					},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: a.Namespace, Name: a.Name, State: kcmv1.ServiceStateFailed},
							{Namespace: b.Namespace, Name: b.Name, State: kcmv1.ServiceStateFailed},
						},
					},
				},
			},
			expected: []testService{b, a, c},
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
		t.Helper()
		actual, err := StateManagementProviderConfigFromServiceSpec(tc.spec)
		require.NoError(t, err)
		require.Equal(t, tc.want, actual)
	}

	testCases := []testCase{
		{
			description: "neither provider name nor config is set",
			spec: kcmv1.ServiceSpec{
				PolicyRefs: []addoncontrollerv1beta1.PolicyRef{
					{
						Name:           "policy-name",
						Namespace:      "policy-namespace",
						Kind:           "ConfigMap",
						DeploymentType: addoncontrollerv1beta1.DeploymentTypeRemote,
					},
				},
			},
			want: kcmv1.StateManagementProviderConfig{
				Name: kubeutil.DefaultStateManagementProvider,
				Config: &apiextv1.JSON{
					Raw: []byte(`{"policyRefs":[{"namespace":"policy-namespace","name":"policy-name","kind":"ConfigMap","deploymentType":"Remote"}]}`),
				},
			},
		},
		{
			description: "provider name is not set, config is set",
			spec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Config: &apiextv1.JSON{
						Raw: []byte(`{"policyRefs":[{"namespace":"policy-namespace","name":"policy-name","kind":"ConfigMap","deploymentType":"Remote"}]}`),
					},
					SelfManagement: false,
				},
			},
			want: kcmv1.StateManagementProviderConfig{
				Name: kubeutil.DefaultStateManagementProvider,
				Config: &apiextv1.JSON{
					Raw: []byte(`{"policyRefs":[{"namespace":"policy-namespace","name":"policy-name","kind":"ConfigMap","deploymentType":"Remote"}]}`),
				},
			},
		},
		{
			description: "provider name is not set, config is set, deprecated fields are discarded",
			spec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Config: &apiextv1.JSON{
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
			want: kcmv1.StateManagementProviderConfig{
				Name: kubeutil.DefaultStateManagementProvider,
				Config: &apiextv1.JSON{
					Raw: []byte(`{"policyRefs":[{"namespace":"policy-namespace","name":"policy-name","kind":"ConfigMap","deploymentType":"Remote"}]}`),
				},
				SelfManagement: true,
			},
		},
		{
			description: "provider name is set, config is not set",
			spec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Name:           "custom-provider",
					SelfManagement: false,
				},
			},
			want: kcmv1.StateManagementProviderConfig{
				Name:           "custom-provider",
				SelfManagement: false,
			},
		},
		{
			description: "provider name is set, config is not set, self management is set to true",
			spec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Name:           "custom-provider",
					SelfManagement: true,
				},
			},
			want: kcmv1.StateManagementProviderConfig{
				Name:           "custom-provider",
				SelfManagement: true,
			},
		},
		{
			description: "provider name is set, config is set",
			spec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
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
					SelfManagement: false,
				},
			},
			want: kcmv1.StateManagementProviderConfig{
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

func Test_FilterServiceDependencies_Order(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kcmv1.AddToScheme(scheme))

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cd", Namespace: "test-ns"},
	}

	// Services in deliberately non-alphabetical order.
	services := []kcmv1.Service{
		{Namespace: "ns-z", Name: "svc-z"},
		{Namespace: "ns-a", Name: "svc-b"},
		{Namespace: "ns-a", Name: "svc-a"},
		{Namespace: "ns-m", Name: "svc-m"},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetClusterIndexKey, kcmv1.ExtractServiceSetCluster).
		WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetMultiClusterServiceIndexKey, kcmv1.ExtractServiceSetMultiClusterService).
		Build()

	// Call multiple times to confirm the result is stable (not random).
	var prev []kcmv1.Service
	for range 5 {
		got, err := FilterServiceDependencies(t.Context(), cl, "system-ns", nil, cd, services)
		require.NoError(t, err)
		require.Equal(t, []kcmv1.Service{
			{Namespace: "ns-a", Name: "svc-a"},
			{Namespace: "ns-a", Name: "svc-b"},
			{Namespace: "ns-m", Name: "svc-m"},
			{Namespace: "ns-z", Name: "svc-z"},
		}, got)
		if prev != nil {
			require.Equal(t, prev, got, "output order must be stable across calls")
		}
		prev = got
	}
}
