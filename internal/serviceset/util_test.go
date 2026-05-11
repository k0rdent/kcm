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

const testSystemNamespace = "test-system-ns"

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
	d := testService{kcmv1.Service{Namespace: "D", Name: "d"}}

	for _, tc := range []struct {
		testName        string
		desiredServices []testService
		objects         []client.Object
		expected        []testService
		wantErr         bool
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
			// A is failing. B (depends on A) is deployed at its current version.
			// C (depends on A) was never deployed.
			// Because A is not Deployed, B and C both have unsatisfied dependencies and
			// are excluded from the filtered list. B is locked at its stored version by
			// BuildServicesList — it stays in the spec unchanged and won't be upgraded
			// until A recovers. C (not in stored) is absent until A is deployed.
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
			expected: []testService{a},
		},
		{
			// A is failing, C (depends on A) is provisioning. Neither is Deployed.
			// B (depends on A) also has an unsatisfied dependency.
			// Only A (no deps) is returned. B and C are locked by BuildServicesList.
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
			expected: []testService{a},
		},
		{
			// A is failing. B (depends on A) is deployed. C depends on B.
			// A is not Deployed → B has an unsatisfied dependency and is excluded from filtered.
			// B being Deployed means C's dependency (B) IS satisfied → C is included.
			// B is locked at its stored version by BuildServicesList.
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
			expected: []testService{a, c},
		},
		{
			// A is failing. B (depends on A) is provisioning. C (depends on B) was never added to spec.
			// Neither A nor B is Deployed, so both B and C have unsatisfied deps.
			// Only A (no deps) is returned. B is locked by BuildServicesList. C stays absent.
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
			expected: []testService{a},
		},
		{
			// A and B are both failing. C (depends on B) is in spec (was deployed previously).
			// Neither A nor B is Deployed, so B and C both have unsatisfied deps.
			// Only A (no deps) is returned. B and C are locked by BuildServicesList.
			testName:        "service A currently !Deployed with C->B->A and B is currently !Deployed (C in spec)",
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
			expected: []testService{a},
		},
		{
			// Timeline: a, b, c(->b) all deployed. Spec changes to d, b(->d), c(->b).
			// After the first reconcile d is added to the ServiceSet; b and c were preserved.
			// d then fails. d is not Deployed → b (depends on d) has an unsatisfied dep and
			// is excluded from filtered. B is locked at its stored version by BuildServicesList.
			// c depends on b which IS Deployed → c's dep is satisfied → c is included in filtered.
			testName:        "deployed services preserved when newly added dependency fails",
			desiredServices: []testService{d, b.dependsOn(d), c.dependsOn(b)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec: kcmv1.ServiceSetSpec{
						Cluster: cd.GetName(),
						Services: []kcmv1.ServiceWithValues{
							// d was added in the previous reconcile; b and c were preserved.
							{Namespace: d.Namespace, Name: d.Name},
							{Namespace: b.Namespace, Name: b.Name},
							{Namespace: c.Namespace, Name: c.Name},
						},
					},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: d.Namespace, Name: d.Name, State: kcmv1.ServiceStateFailed},
							{Namespace: b.Namespace, Name: b.Name, State: kcmv1.ServiceStateDeployed},
							{Namespace: c.Namespace, Name: c.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
			},
			// d: no deps → included. b: depends on d (d not Deployed) → excluded, locked by BuildServicesList.
			// c: depends on b (b Deployed) → included.
			expected: []testService{d, c},
		},
		{
			// Spec update introduces a as a new dependency for the previously-deployed b (now failed).
			// a is new (not in stored). b is failed → not Deployed → b and c both have unsatisfied deps.
			// Only a (no deps) is returned. b and c are locked by BuildServicesList at their stored versions.
			testName:        "previously deployed but currently failed service excluded from filtered list when new dependency is unsatisfied",
			desiredServices: []testService{a, b.dependsOn(a), c.dependsOn(b)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec: kcmv1.ServiceSetSpec{
						Cluster: cd.GetName(),
						Services: []kcmv1.ServiceWithValues{
							{Namespace: b.Namespace, Name: b.Name},
							{Namespace: c.Namespace, Name: c.Name},
						},
					},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: b.Namespace, Name: b.Name, State: kcmv1.ServiceStateFailed},
							{Namespace: c.Namespace, Name: c.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
			},
			// a: no deps → included. b: depends on a (a not Deployed) → excluded, locked by BuildServicesList.
			// c: depends on b (b not Deployed) → excluded, locked by BuildServicesList.
			expected: []testService{a},
		},
		{
			// When a spec update adds a new dependency to an already-deployed service,
			// FilterServiceDependencies returns only services with all dependencies satisfied.
			// b has a new unsatisfied dependency (a) so it is excluded here; BuildServicesList
			// preserves it at its current version.
			testName:        "already deployed service excluded from filtered list when spec update adds new unsatisfied dependency",
			desiredServices: []testService{a, b.dependsOn(a), c.dependsOn(b)},
			objects: []client.Object{
				&kcmv1.ServiceSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: cd.GetNamespace(), Name: cd.GetName()},
					Spec: kcmv1.ServiceSetSpec{
						Cluster: cd.GetName(),
						Services: []kcmv1.ServiceWithValues{
							// b and c were deployed before the spec change; a is brand new.
							{Namespace: b.Namespace, Name: b.Name},
							{Namespace: c.Namespace, Name: c.Name},
						},
					},
					Status: kcmv1.ServiceSetStatus{
						Services: []kcmv1.ServiceState{
							{Namespace: b.Namespace, Name: b.Name, State: kcmv1.ServiceStateDeployed},
							{Namespace: c.Namespace, Name: c.Name, State: kcmv1.ServiceStateDeployed},
						},
					},
				},
			},
			// a has no deps → included. b depends on a (unsatisfied) → excluded (locked, handled by BuildServicesList).
			// c depends on b which is deployed → count = 0 → included.
			expected: []testService{a, c},
		},
		{
			testName:        "error when dependency is absent from desired services list",
			desiredServices: []testService{b.dependsOn(a)},
			expected:        nil,
			wantErr:         true,
		},
	} {
		t.Run(tc.testName, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.objects...).
				WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetClusterIndexKey, kcmv1.ExtractServiceSetCluster).
				WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetMultiClusterServiceIndexKey, kcmv1.ExtractServiceSetMultiClusterService).
				Build()

			filtered, err := FilterServiceDependencies(t.Context(), client, testSystemNamespace, nil, cd, testServices2Services(t, tc.desiredServices))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
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

				filtered, err = FilterServiceDependencies(t.Context(), client, testSystemNamespace, nil, cd, testServices2Services(t, tc.desiredServices))
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

func Test_BuildServicesList(t *testing.T) {
	t.Parallel()

	svcA := kcmv1.ServiceWithValues{Namespace: "A", Name: "a", Version: new("1.0")}
	svcB := kcmv1.ServiceWithValues{Namespace: "B", Name: "b", Version: new("1.0")}
	svcC := kcmv1.ServiceWithValues{Namespace: "C", Name: "c", Version: new("1.0")}
	svcD := kcmv1.ServiceWithValues{Namespace: "D", Name: "d", Version: new("1.0")}

	desiredAll := []kcmv1.Service{
		{Namespace: "A", Name: "a"},
		{Namespace: "B", Name: "b"},
		{Namespace: "C", Name: "c"},
	}

	for _, tc := range []struct {
		testName string
		stored   []kcmv1.ServiceWithValues
		filtered []kcmv1.ServiceWithValues
		desired  []kcmv1.Service
		expected []kcmv1.ServiceWithValues
	}{
		{
			testName: "empty",
		},
		{
			testName: "all services in filtered are included",
			stored:   nil,
			filtered: []kcmv1.ServiceWithValues{svcA, svcB},
			desired:  desiredAll,
			expected: []kcmv1.ServiceWithValues{svcA, svcB},
		},
		{
			testName: "locked service preserved from stored when not in filtered",
			stored:   []kcmv1.ServiceWithValues{svcA, svcB},
			filtered: []kcmv1.ServiceWithValues{svcA},
			desired:  desiredAll,
			// b is still desired but not in filtered (locked) → kept as-is from stored
			expected: []kcmv1.ServiceWithValues{svcA, svcB},
		},
		{
			testName: "service removed from desired is dropped from stored",
			stored:   []kcmv1.ServiceWithValues{svcA, svcB, svcC},
			filtered: []kcmv1.ServiceWithValues{svcA},
			// b is no longer desired
			desired: []kcmv1.Service{{Namespace: "A", Name: "a"}, {Namespace: "C", Name: "c"}},
			// b dropped; c is desired but not in filtered → kept as-is
			expected: []kcmv1.ServiceWithValues{svcA, svcC},
		},
		{
			testName: "new service in filtered is added even if not in stored",
			stored:   []kcmv1.ServiceWithValues{svcA},
			filtered: []kcmv1.ServiceWithValues{svcA, svcD},
			desired:  []kcmv1.Service{{Namespace: "A", Name: "a"}, {Namespace: "D", Name: "d"}},
			expected: []kcmv1.ServiceWithValues{svcA, svcD},
		},
		{
			testName: "filtered version takes precedence over stored version",
			stored:   []kcmv1.ServiceWithValues{{Namespace: "A", Name: "a", Version: new("1.0")}},
			filtered: []kcmv1.ServiceWithValues{{Namespace: "A", Name: "a", Version: new("2.0")}},
			desired:  []kcmv1.Service{{Namespace: "A", Name: "a"}},
			expected: []kcmv1.ServiceWithValues{{Namespace: "A", Name: "a", Version: new("2.0")}},
		},
	} {
		t.Run(tc.testName, func(t *testing.T) {
			t.Parallel()
			got := BuildServicesList(tc.stored, tc.filtered, tc.desired)
			assert.ElementsMatch(t, tc.expected, got)
		})
	}
}

// Test_GetServiceSetWithOperation_NoSpuriousUpdates verifies that calling
// GetServiceSetWithOperation multiple times without any changes in the
// desired services produces OperationNone after the initial Create.
// This is a regression test for a bug where fillServiceWithValueVersions
// checked `svc.Values == ""` instead of the version field, causing
// version resolution to overwrite stored versions on every reconcile
// and resulting in an infinite update loop (continuously increasing
// .metadata.generation).
func Test_GetServiceSetWithOperation_NoSpuriousUpdates(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kcmv1.AddToScheme(scheme))

	const (
		cdNamespace  = "test-ns"
		cdName       = "test-cd"
		providerName = "custom-provider"
		svcName      = "propagation-svc"
		svcNamespace = "default"
		templateName = "propagation-template"
	)

	selectorLabel := map[string]string{"test-selector": "true"}

	// ServiceTemplate with Resources (not Helm) and NO Version.
	// This is exactly the scenario that triggers the bug:
	// fillServiceWithValueVersions would fall back to Template name
	// as version, but the guard condition checked Values instead of
	// Version, causing the fallback to fire on every reconcile.
	serviceTemplate := &kcmv1.ServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      templateName,
			Namespace: cdNamespace,
		},
		Spec: kcmv1.ServiceTemplateSpec{
			// No Helm, no Version — resource-type template
			Resources: &kcmv1.SourceSpec{
				LocalSourceRef: &kcmv1.LocalSourceRef{
					Kind: "ConfigMap",
					Name: "test-configmap",
				},
				DeploymentType: "Remote",
			},
		},
		Status: kcmv1.ServiceTemplateStatus{
			TemplateStatusCommon: kcmv1.TemplateStatusCommon{
				TemplateValidationStatus: kcmv1.TemplateValidationStatus{
					Valid: true,
				},
			},
			SourceStatus: &kcmv1.SourceStatus{
				Kind:      "ConfigMap",
				Name:      "test-configmap",
				Namespace: cdNamespace,
			},
		},
	}

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cdName,
			Namespace: cdNamespace,
		},
		Spec: kcmv1.ClusterDeploymentSpec{
			Template:   "sample-template",
			Credential: "sample-credential",
			ServiceSpec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Name: providerName,
				},
				Services: []kcmv1.Service{
					{
						Name:      svcName,
						Namespace: svcNamespace,
						Template:  templateName,
						// No Version set — version resolution must fill it
					},
				},
			},
		},
	}

	provider := &kcmv1.StateManagementProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name: providerName,
		},
		Spec: kcmv1.StateManagementProviderSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabel,
			},
			Adapter: kcmv1.ResourceReference{
				APIVersion: "v1",
				Kind:       "Deployment",
				Name:       "adapter",
				Namespace:  "adapter-ns",
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(serviceTemplate, cd, provider).
		WithStatusSubresource(&kcmv1.ServiceSet{}).
		WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetClusterIndexKey, kcmv1.ExtractServiceSetCluster).
		WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetMultiClusterServiceIndexKey, kcmv1.ExtractServiceSetMultiClusterService).
		Build()

	opReq := OperationRequisites{
		ObjectKey:       client.ObjectKey{Namespace: cdNamespace, Name: cdName},
		CD:              cd,
		SystemNamespace: testSystemNamespace,
	}

	// First call: ServiceSet does not exist → must return Create.
	serviceSet, op, err := GetServiceSetWithOperation(t.Context(), cl, opReq)
	require.NoError(t, err)
	require.Equal(t, kcmv1.ServiceSetOperationCreate, op, "first call should return Create")

	// Persist the ServiceSet (simulates what the controller does).
	proc := NewProcessor(cl)
	require.NoError(t, proc.CreateOrUpdateServiceSet(t.Context(), op, serviceSet))

	// Subsequent calls: ServiceSet exists and nothing changed → must return None.
	for i := range 5 {
		_, op, err = GetServiceSetWithOperation(t.Context(), cl, opReq)
		require.NoError(t, err)
		require.Equal(t, kcmv1.ServiceSetOperationNone, op,
			"iteration %d: expected OperationNone (no spurious update), got %s", i, op)
	}
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

// Test_FilterServiceDependencies_VersionGate covers the version-aware dependency
// gate: a dependency satisfies its dependents only when (state == Deployed) AND
// (Status.Version == Spec.Version) AND (Spec.Version == user's desired version).
// Each case isolates one of those conditions.
func Test_FilterServiceDependencies_VersionGate(t *testing.T) {
	t.Parallel()

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cd", Namespace: "test-cd-ns"},
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kcmv1.AddToScheme(scheme))

	makeServiceSet := func(spec []kcmv1.ServiceWithValues, status []kcmv1.ServiceState) *kcmv1.ServiceSet {
		return &kcmv1.ServiceSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: cd.Namespace, Name: cd.Name},
			Spec:       kcmv1.ServiceSetSpec{Cluster: cd.Name, Services: spec},
			Status:     kcmv1.ServiceSetStatus{Services: status},
		}
	}

	a := func(version string) kcmv1.Service {
		return kcmv1.Service{Namespace: "ns", Name: "a", Template: "tpl-a", Version: version}
	}
	b := func(version string) kcmv1.Service {
		return kcmv1.Service{
			Namespace: "ns", Name: "b", Template: "tpl-b", Version: version,
			DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "ns", Name: "a"}},
		}
	}
	specOf := func(name, version string) kcmv1.ServiceWithValues {
		return kcmv1.ServiceWithValues{Namespace: "ns", Name: name, Template: "tpl-" + name, Version: new(version)}
	}
	statusOf := func(name, state, version string) kcmv1.ServiceState {
		return kcmv1.ServiceState{Namespace: "ns", Name: name, State: state, Version: new(version)}
	}

	for _, tc := range []struct {
		name            string
		desiredServices []kcmv1.Service
		objects         []client.Object
		expected        []string
	}{
		{
			name:            "fully synced dep unlocks dependent",
			desiredServices: []kcmv1.Service{a("v1"), b("v1")},
			objects: []client.Object{makeServiceSet(
				[]kcmv1.ServiceWithValues{specOf("a", "v1")},
				[]kcmv1.ServiceState{statusOf("a", kcmv1.ServiceStateDeployed, "v1")},
			)},
			expected: []string{"a", "b"},
		},
		{
			name:            "in-flight dep (status != spec) locks dependent",
			desiredServices: []kcmv1.Service{a("v2"), b("v1")},
			objects: []client.Object{makeServiceSet(
				[]kcmv1.ServiceWithValues{specOf("a", "v2")},
				[]kcmv1.ServiceState{statusOf("a", kcmv1.ServiceStateProvisioning, "v1")},
			)},
			expected: []string{"a"},
		},
		{
			name:            "spec lags desired locks dependent (KOF upgrade bug)",
			desiredServices: []kcmv1.Service{a("v2"), b("v2")},
			objects: []client.Object{makeServiceSet(
				[]kcmv1.ServiceWithValues{specOf("a", "v1"), specOf("b", "v1")},
				[]kcmv1.ServiceState{
					statusOf("a", kcmv1.ServiceStateDeployed, "v1"),
					statusOf("b", kcmv1.ServiceStateDeployed, "v1"),
				},
			)},
			expected: []string{"a"},
		},
		{
			name:            "dep at user-desired version unlocks dependent on upgrade",
			desiredServices: []kcmv1.Service{a("v2"), b("v2")},
			objects: []client.Object{makeServiceSet(
				[]kcmv1.ServiceWithValues{specOf("a", "v2"), specOf("b", "v1")},
				[]kcmv1.ServiceState{
					statusOf("a", kcmv1.ServiceStateDeployed, "v2"),
					statusOf("b", kcmv1.ServiceStateDeployed, "v1"),
				},
			)},
			expected: []string{"a", "b"},
		},
		{
			name:            "failed dep locks dependent (regression with versions present)",
			desiredServices: []kcmv1.Service{a("v1"), b("v1")},
			objects: []client.Object{makeServiceSet(
				[]kcmv1.ServiceWithValues{specOf("a", "v1")},
				[]kcmv1.ServiceState{statusOf("a", kcmv1.ServiceStateFailed, "v1")},
			)},
			expected: []string{"a"},
		},
		{
			name:            "first install — no ServiceSet exists, only no-dep services pass",
			desiredServices: []kcmv1.Service{a("v1"), b("v1")},
			objects:         nil,
			expected:        []string{"a"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.objects...).
				WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetClusterIndexKey, kcmv1.ExtractServiceSetCluster).
				WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetMultiClusterServiceIndexKey, kcmv1.ExtractServiceSetMultiClusterService).
				Build()

			filtered, err := FilterServiceDependencies(t.Context(), cl, testSystemNamespace, nil, cd, tc.desiredServices)
			require.NoError(t, err)

			names := make([]string, len(filtered))
			for i, svc := range filtered {
				names[i] = svc.Name
			}
			require.ElementsMatch(t, tc.expected, names)
		})
	}
}

// Test_FilterServiceDependencies_UpgradeOrdering walks the KOF-shaped dependency
// chain (cm → ops → storage → collectors) through a full v1→v2 bump and verifies
// that exactly one new service unlocks per "upstream finished" step. This is a
// direct regression test for the issue where an upgrade let all four services
// land in the ServiceSet spec simultaneously.
func Test_FilterServiceDependencies_UpgradeOrdering(t *testing.T) {
	t.Parallel()

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cd", Namespace: "test-cd-ns"},
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kcmv1.AddToScheme(scheme))

	desired := []kcmv1.Service{
		{Namespace: "kof", Name: "cm", Template: "cm", Version: "v2"},
		{
			Namespace: "kof", Name: "ops", Template: "ops", Version: "v2",
			DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "kof", Name: "cm"}},
		},
		{
			Namespace: "kof", Name: "storage", Template: "storage", Version: "v2",
			DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "kof", Name: "ops"}},
		},
		{
			Namespace: "kof", Name: "collectors", Template: "collectors", Version: "v2",
			DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "kof", Name: "storage"}},
		},
	}
	services := []string{"cm", "ops", "storage", "collectors"}

	allDeployed := func() map[string]string {
		m := make(map[string]string, len(services))
		for _, s := range services {
			m[s] = kcmv1.ServiceStateDeployed
		}
		return m
	}
	versionsAt := func(advanced map[string]struct{}, advancedVersion, fallback string) map[string]string {
		m := make(map[string]string, len(services))
		for _, s := range services {
			if _, ok := advanced[s]; ok {
				m[s] = advancedVersion
			} else {
				m[s] = fallback
			}
		}
		return m
	}
	advancedSet := func(names ...string) map[string]struct{} {
		m := make(map[string]struct{}, len(names))
		for _, n := range names {
			m[n] = struct{}{}
		}
		return m
	}

	for _, tc := range []struct {
		name           string
		specVersions   map[string]string
		statusVersions map[string]string
		statusStates   map[string]string
		expected       []string
	}{
		{
			name:           "all v1, user just bumped to v2 — only chain head eligible",
			specVersions:   versionsAt(advancedSet(), "v2", "v1"),
			statusVersions: versionsAt(advancedSet(), "v2", "v1"),
			statusStates:   allDeployed(),
			expected:       []string{"cm"},
		},
		{
			name:           "cm spec advanced, status lagging — dependents still locked",
			specVersions:   versionsAt(advancedSet("cm"), "v2", "v1"),
			statusVersions: versionsAt(advancedSet(), "v2", "v1"),
			statusStates: func() map[string]string {
				m := allDeployed()
				m["cm"] = kcmv1.ServiceStateProvisioning
				return m
			}(),
			expected: []string{"cm"},
		},
		{
			name:           "cm finished v2 — ops unlocked, storage and collectors still locked",
			specVersions:   versionsAt(advancedSet("cm"), "v2", "v1"),
			statusVersions: versionsAt(advancedSet("cm"), "v2", "v1"),
			statusStates:   allDeployed(),
			expected:       []string{"cm", "ops"},
		},
		{
			name:           "ops finished v2 — storage unlocked, collectors still locked",
			specVersions:   versionsAt(advancedSet("cm", "ops"), "v2", "v1"),
			statusVersions: versionsAt(advancedSet("cm", "ops"), "v2", "v1"),
			statusStates:   allDeployed(),
			expected:       []string{"cm", "ops", "storage"},
		},
		{
			name:           "storage finished v2 — collectors unlocked, all eligible",
			specVersions:   versionsAt(advancedSet("cm", "ops", "storage"), "v2", "v1"),
			statusVersions: versionsAt(advancedSet("cm", "ops", "storage"), "v2", "v1"),
			statusStates:   allDeployed(),
			expected:       []string{"cm", "ops", "storage", "collectors"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sset := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: cd.Namespace, Name: cd.Name},
				Spec:       kcmv1.ServiceSetSpec{Cluster: cd.Name},
			}
			for _, name := range services {
				specVer := tc.specVersions[name]
				statusVer := tc.statusVersions[name]
				sset.Spec.Services = append(sset.Spec.Services, kcmv1.ServiceWithValues{
					Namespace: "kof", Name: name, Template: name, Version: &specVer,
				})
				sset.Status.Services = append(sset.Status.Services, kcmv1.ServiceState{
					Namespace: "kof", Name: name, State: tc.statusStates[name], Version: &statusVer,
				})
			}

			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(sset).
				WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetClusterIndexKey, kcmv1.ExtractServiceSetCluster).
				WithIndex(&kcmv1.ServiceSet{}, kcmv1.ServiceSetMultiClusterServiceIndexKey, kcmv1.ExtractServiceSetMultiClusterService).
				Build()

			filtered, err := FilterServiceDependencies(t.Context(), cl, testSystemNamespace, nil, cd, desired)
			require.NoError(t, err)

			names := make([]string, len(filtered))
			for i, svc := range filtered {
				names[i] = svc.Name
			}
			require.ElementsMatch(t, tc.expected, names)
		})
	}
}
