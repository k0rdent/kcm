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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
