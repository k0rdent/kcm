// Copyright 2026
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

package sveltos

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func Test_servicesStateFromSummary_Helm(t *testing.T) {
	type testCase struct {
		description string
		summary     string
		serviceSet  *kcmv1.ServiceSet
		expected    []kcmv1.ServiceState
	}

	cases := []testCase{
		// {
		// 	description: "empty status with no services",
		// },
		{
			description: "empty status with services",
			// summary: `,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateNotDeployed,
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateNotDeployed,
				},
			},
		},
		{
			description: "nginx Failed",
			summary: `
  status:
    dependencies: no dependencies
    featureSummaries:
    - consecutiveFailures: 1
      failureMessage: |
        chart: ingress-nginx, releaseNamespace: nginx, release: nginx, context deadline exceeded
      featureID: Helm
      hash: r3heWCzDuieATqLebzLZqzkIU6JqVY2+pUnrNfplcLM=
      lastAppliedTime: "2026-04-23T06:08:08Z"
      status: Failed
    helmReleaseSummaries:
    - failureMessage: context deadline exceeded
      releaseName: nginx
      releaseNamespace: nginx
      status: Managing
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "nginx",
					Namespace:      "nginx",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
			},
		},
		{
			description: "nginx Failed->Provisioning",
			summary: `
  status:
    dependencies: no dependencies
    featureSummaries:
    - failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
      featureID: Helm
      hash: 9uIsgqogeawbyindlsMevgCxjOoYRtTurwUU1aVDeBA=
      lastAppliedTime: "2026-04-23T08:28:19Z"
      status: Provisioning
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateProvisioning,
				},
			},
		},
		{
			description: "nginx Deployed",
			summary: `
  status:
    dependencies: no dependencies
    featureSummaries:
    - featureID: Helm
      hash: RrRcFPXyTypTdFD3d8q7emG92cPZfUrI7djagQI8/K8=
      lastAppliedTime: "2024-10-01T02:58:32Z"
      status: Provisioned
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: Eq4yyx7ALQHto1gbEnwf7jsNxTVy7WuvI5choD2C4SY=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateDeployed,
				},
			},
		},
		{
			description: "nginx Deployed->Provisioning",
			summary: `
  status:
    dependencies: no dependencies
    featureSummaries:
    - featureID: Helm
      hash: XEqVbp+3j+cNDb+oJGfsQW6+3w6vLuYIXkcn6xsKLbo=
      lastAppliedTime: "2026-04-23T08:28:39Z"
      status: Provisioning
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: RTxW4tOWWuPqhKwLwZ8935jZY/koONmlkiGx3l14SQs=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateProvisioning,
				},
			},
		},
		{
			description: "nginx Deployed->Provisioning->Failed",
			summary: `
  status:
    dependencies: no dependencies
    featureSummaries:
    - consecutiveFailures: 1
      failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
      featureID: Helm
      hash: r3heWCzDuieATqLebzLZqzkIU6JqVY2+pUnrNfplcLM=
      lastAppliedTime: "2026-04-23T10:12:22Z"
      status: Failed
    helmReleaseSummaries:
    - failureMessage: context deadline exceeded
      releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "nginx",
					Namespace:      "nginx",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
			},
		},
		{
			description: "nginx Failed & postgres Failed",
			summary: `
  status:
    dependencies: no dependencies
    featureSummaries:
    - failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
        chart=postgres-operator, releaseNamespace=postgres-operator, releaseName=postgres-operator: context deadline exceeded
      featureID: Helm
      hash: +OIMqZZ3LuRy6P8yCT45zb92Y6i4BAj1W94pMGq9wfk=
      lastAppliedTime: "2026-03-25T13:45:16Z"
      status: Failed
    helmReleaseSummaries:
    - failureMessage: context deadline exceeded
      releaseName: nginx
      releaseNamespace: nginx
      status: Managing
    - failureMessage: context deadline exceeded
      releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "nginx",
					Namespace:      "nginx",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "postgres-operator",
					Namespace:      "postgres-operator",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
			},
		},
		// Fill out these test cases
		{
			description: "nginx Failed & postgres Failed->Provisioning",
			summary: `
  status:
    dependencies: no dependencies
    featureSummaries:
    - failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
        chart=postgres-operator, releaseNamespace=postgres-operator, releaseName=postgres-operator: context deadline exceeded
      featureID: Helm
      hash: 9J6imd1Rsr9faFztMeQDOQthMKWpFk2FyQ0WBm89XNg=
      lastAppliedTime: "2026-04-24T21:02:44Z"
      status: Provisioning
    helmReleaseSummaries:
    - failureMessage: context deadline exceeded
      releaseName: nginx
      releaseNamespace: nginx
      status: Managing
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "nginx",
					Namespace:      "nginx",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateProvisioning,
				},
			},
		},
		{
			description: "nginx Failed & postgres Failed->Provisioning->Deployed",
			summary: `
  status:
    dependencies: no dependencies
    featureSummaries:
    - consecutiveFailures: 31
      failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
      featureID: Helm
      hash: 9J6imd1Rsr9faFztMeQDOQthMKWpFk2FyQ0WBm89XNg=
      lastAppliedTime: "2026-04-24T21:18:50Z"
      status: Failed
    helmReleaseSummaries:
    - failureMessage: context deadline exceeded
      releaseName: nginx
      releaseNamespace: nginx
      status: Managing
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "nginx",
					Namespace:      "nginx",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateDeployed,
				},
			},
		},
		{
			// In this case the valuesHash for each service is set even though the
			// previous state was Failed because during provisioning Sveltos sets
			// the valuesHash before it updates the Helm Feature's FailureMessage.
			description: "nginx Failed->Provisioning & postgres Failed->Provisioning",
			summary: `
  status:
    featureSummaries:
    - failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
        chart=postgres-operator, releaseNamespace=postgres-operator, releaseName=postgres-operator: context deadline exceeded
      featureID: Helm
      hash: mBh74LoXFwbhno8ShBxkx8C0+L7qYvsL3L1Mj0P/BQw=
      lastAppliedTime: "2026-04-24T21:32:14Z"
      status: Provisioning
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateProvisioning,
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateProvisioning,
				},
			},
		},
		{
			description: "nginx Provisioning & postgres Provisioning",
			summary: `
  status:
    featureSummaries:
    - featureID: Helm
      hash: mBh74LoXFwbhno8ShBxkx8C0+L7qYvsL3L1Mj0P/BQw=
      status: Provisioning
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateProvisioning,
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateProvisioning,
				},
			},
		},
		{
			description: "nginx Deployed & postgres Deployed",
			summary: `
  status:
    featureSummaries:
    - featureID: Helm
      hash: mBh74LoXFwbhno8ShBxkx8C0+L7qYvsL3L1Mj0P/BQw=
      lastAppliedTime: "2026-04-24T21:41:08Z"
      status: Provisioned
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateDeployed,
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateDeployed,
				},
			},
		},
		{
			// When creating this case, I updated the values of nginx to 3 replicas so
			// that nginx would enter the Provisioning state. The postgres-operator
			// service was unchanged, however, looking at the ClusterSummary's status
			// there is no way to know which of the services is Provisioning or Deployed
			// or both because both have valuesHash set and no failure message and the
			// Helm Feature's FailureMessage is also empty. So the best we can do in this
			// transitionary period is set the status for both services as Provisioning and
			// once nginx transitions from Provisioning to either Failed or Deployed,
			// the status will be updated to capture the correct state for each service.
			description: "nginx Deployed->Provisioning & postgres Deployed",
			summary: `
  status:
    featureSummaries:
    - featureID: Helm
      hash: a28oOzTRQxNGS+4N3349+goL8MKG+GjOLoza0spa7uo=
      lastAppliedTime: "2026-04-24T21:41:08Z"
      status: Provisioning
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateProvisioning,
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateProvisioning,
				},
			},
		},
		{
			description: "nginx Deployed->Provisioning & postgres Deployed->Provisioning",
			summary: `
  status:
    featureSummaries:
    - featureID: Helm
      hash: dFJHYOa9fQO15cZjkLTgKmxXpz0dBuRvb1qc7ccMGW8=
      lastAppliedTime: "2026-04-24T21:52:14Z"
      status: Provisioning
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateProvisioning,
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateProvisioning,
				},
			},
		},
		{
			description: "nginx Deployed->Provisioning->Failed & postgres Deployed",
			summary: `
  status:
    featureSummaries:
    - consecutiveFailures: 2
      failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
      featureID: Helm
      hash: 0DxX3s8cx9qPS+0hcC+LZizjB0u1SNNIgSgG8fv+o4k=
      lastAppliedTime: "2026-04-24T21:56:03Z"
      status: Failed
    helmReleaseSummaries:
    - failureMessage: context deadline exceeded
      releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: RTxW4tOWWuPqhKwLwZ8935jZY/koONmlkiGx3l14SQs=
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
      valuesHash: ++UQu2VpyN9315nhoxA3kxUpkqroDv/32xGowdzQwrA=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "nginx",
					Namespace:      "nginx",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateDeployed,
				},
			},
		},
		{
			description: "nginx Deployed->Provisioning->Failed & postgres Deployed->Provisioning",
			summary: `
  status:
    featureSummaries:
    - failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
      featureID: Helm
      hash: 9J6imd1Rsr9faFztMeQDOQthMKWpFk2FyQ0WBm89XNg=
      lastAppliedTime: "2026-04-24T21:58:33Z"
      status: Provisioning
    helmReleaseSummaries:
    - failureMessage: context deadline exceeded
      releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: RTxW4tOWWuPqhKwLwZ8935jZY/koONmlkiGx3l14SQs=
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "nginx",
					Namespace:      "nginx",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateProvisioning,
				},
			},
		},
		{
			description: "nginx Deployed->Provisioning->Failed & postgres Deployed->Provisioning->Failed",
			summary: `
  status:
    featureSummaries:
    - failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
        chart=postgres-operator, releaseNamespace=postgres-operator, releaseName=postgres-operator: context deadline exceeded
      featureID: Helm
      hash: +OIMqZZ3LuRy6P8yCT45zb92Y6i4BAj1W94pMGq9wfk=
      lastAppliedTime: "2026-03-25T13:45:16Z"
      status: Failed
    helmReleaseSummaries:
    - failureMessage: context deadline exceeded
      releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: RTxW4tOWWuPqhKwLwZ8935jZY/koONmlkiGx3l14SQs=
    - failureMessage: context deadline exceeded
      releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "nginx",
					Namespace:      "nginx",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "postgres-operator",
					Namespace:      "postgres-operator",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
			},
		},
		{
			description: "nginx Failed->Provisioning->Deployed & postgres Failed->Provisioning->Deployed",
			summary: `
  status:
    featureSummaries:
    - featureID: Helm
      hash: mBh74LoXFwbhno8ShBxkx8C0+L7qYvsL3L1Mj0P/BQw=
      lastAppliedTime: "2026-04-24T22:35:48Z"
      status: Provisioned
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateDeployed,
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateDeployed,
				},
			},
		},
		{
			description: "nginx Deployed->Provisioning->Failed->Provisioning & postgres Deployed",
			summary: `
  status:
    featureSummaries:
    - failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
      featureID: Helm
      hash: mBh74LoXFwbhno8ShBxkx8C0+L7qYvsL3L1Mj0P/BQw=
      lastAppliedTime: "2026-04-24T22:38:38Z"
      status: Provisioning
    helmReleaseSummaries:
    - failureMessage: context deadline exceeded
      releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "nginx",
					Namespace:      "nginx",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateProvisioning,
				},
			},
		},
		{
			// nginx failed & postgres isn't even attempted because continueOnError=false

			// nginx: 		Failed
			// postgres: Pending
			description: "nginx Failed & Postgres Pending because continueOnError=False",
			summary: `
  status:
    dependencies: no dependencies
    featureSummaries:
    - consecutiveFailures: 3
      failureMessage: 'chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx:
        context deadline exceeded'
      featureID: Helm
      hash: p+CtqayLCV/oE2GmSQVgH/njs+PNS9jPuxK2fBfAA+A=
      lastAppliedTime: "2026-04-22T21:40:36Z"
      status: Failed
    helmReleaseSummaries:
    - failureMessage: context deadline exceeded
      releaseName: nginx
      releaseNamespace: nginx
      status: Managing
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "nginx",
					Namespace:      "nginx",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateNotDeployed,
				},
			},
		},
		{
			description: "nginx Failed->Removing & postgres Deployed->Removing",
			summary: `
  status:
    featureSummaries:
    - consecutiveFailures: 9
      failureMessage: 'chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx:
        context deadline exceeded'
      featureID: Helm
      lastAppliedTime: "2026-04-24T22:48:42Z"
      status: Removing
    - featureID: Resources
      status: Removing
    - featureID: Kustomize
      status: Removing
    helmReleaseSummaries:
    - failureMessage: context deadline exceeded
      releaseName: nginx
      releaseNamespace: nginx
      status: Managing
    - releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Managing
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateDeleting,
				},
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "postgres-operator",
					Namespace: "postgres-operator",
					State:     kcmv1.ServiceStateDeleting,
				},
			},
		},
		{
			description: "nginx Failed->Provisioning & postgres Conflict",
			summary: `
  status:
    featureSummaries:
    - failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
      featureID: Helm
      hash: mBh74LoXFwbhno8ShBxkx8C0+L7qYvsL3L1Mj0P/BQw=
      lastAppliedTime: "2026-04-25T00:07:31Z"
      status: Provisioning
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
    - conflictMessage: ClusterSummary management-3556efa5-sveltos-mgmt managing it
      releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Conflict
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateProvisioning,
				},
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "postgres-operator",
					Namespace:      "postgres-operator",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "ClusterSummary management-3556efa5-sveltos-mgmt managing it",
				},
			},
		},
		{
			description: "nginx Failed & postgres Conflict",
			summary: `
  status:
    featureSummaries:
    - consecutiveFailures: 6
      failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
      featureID: Helm
      hash: 9J6imd1Rsr9faFztMeQDOQthMKWpFk2FyQ0WBm89XNg=
      lastAppliedTime: "2026-04-25T00:02:11Z"
      status: Failed
    helmReleaseSummaries:
    - failureMessage: context deadline exceeded
      releaseName: nginx
      releaseNamespace: nginx
      status: Managing
    - conflictMessage: ClusterSummary management-3556efa5-sveltos-mgmt managing it
      releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Conflict
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "nginx",
					Namespace:      "nginx",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "context deadline exceeded",
				},
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "postgres-operator",
					Namespace:      "postgres-operator",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "ClusterSummary management-3556efa5-sveltos-mgmt managing it",
				},
			},
		},
		// {
		// 	description: "nginx Failed->Provisioning & postgres Conflict",
		// },
		{
			description: "nginx Failed->Provisioning & postgres Conflict",
			summary: `
  status:
    featureSummaries:
    - failureMessage: |
        chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
      featureID: Helm
      hash: mBh74LoXFwbhno8ShBxkx8C0+L7qYvsL3L1Mj0P/BQw=
      lastAppliedTime: "2026-04-25T00:07:31Z"
      status: Provisioning
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
    - conflictMessage: ClusterSummary management-3556efa5-sveltos-mgmt managing it
      releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Conflict
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateProvisioning,
				},
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "postgres-operator",
					Namespace:      "postgres-operator",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "ClusterSummary management-3556efa5-sveltos-mgmt managing it",
				},
			},
		},
		{
			// wahab:
			// We treat FailedNonRetriable as Failed but in this particular case
			// nginx is actually deployed. So it could be that with FailedNotRetriable,
			// if the service has valuesHash set and no failureMessage then it may be either
			// Provisioning or Deployed. Hmmm, I suspect it can't be Provisioning because then
			// the Helm Feature's status would be Provisioning. So the fact that the status
			// is a non-transitory one (i.e FailedNonRetriable) probably means that there
			// is no more processing happening so nginx must be Deployed cuz if it had failed
			// then it's failureMessage field would have the error message.
			// Fix this.
			//
			// Therefore, I think this description should be:
			// "nginx Failed->Provisioning->Deployed & postgres Conflict"
			description: "nginx Failed->Provisioning->Deployed & postgres Conflict with FailedNonRetriable",
			summary: `
  status:
    featureSummaries:
    - consecutiveFailures: 2
      failureMessage: |
        cannot manage chart postgres-operator/postgres-operator. ClusterSummary management-3556efa5-sveltos-mgmt managing it.
      featureID: Helm
      hash: mBh74LoXFwbhno8ShBxkx8C0+L7qYvsL3L1Mj0P/BQw=
      lastAppliedTime: "2026-04-25T00:07:48Z"
      status: FailedNonRetriable
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
    - conflictMessage: ClusterSummary management-3556efa5-sveltos-mgmt managing it
      releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Conflict
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateDeployed,
				},
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "postgres-operator",
					Namespace:      "postgres-operator",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "ClusterSummary management-3556efa5-sveltos-mgmt managing it",
				},
			},
		},
		{
			description: "nginx Deployed & postgres Conflict",
			summary: `
  status:
    featureSummaries:
    - consecutiveFailures: 2
      failureMessage: |
        cannot manage chart postgres-operator/postgres-operator. ClusterSummary management-3556efa5-sveltos-mgmt managing it.
      featureID: Helm
      hash: mBh74LoXFwbhno8ShBxkx8C0+L7qYvsL3L1Mj0P/BQw=
      lastAppliedTime: "2026-04-25T00:22:22Z"
      status: FailedNonRetriable
    helmReleaseSummaries:
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
    - conflictMessage: ClusterSummary management-3556efa5-sveltos-mgmt managing it
      releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Conflict
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateDeployed,
				},
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "postgres-operator",
					Namespace:      "postgres-operator",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "ClusterSummary management-3556efa5-sveltos-mgmt managing it",
				},
			},
		},
		{
			description: "nginx Deployed->Provisioning & postgres Conflict",
			summary: `
  status:
    featureSummaries:
    - failureMessage: |
        cannot manage chart postgres-operator/postgres-operator. ClusterSummary management-3556efa5-sveltos-mgmt managing it.
      featureID: Helm
      hash: a28oOzTRQxNGS+4N3349+goL8MKG+GjOLoza0spa7uo=
      lastAppliedTime: "2026-04-25T00:22:22Z"
      status: Provisioning
    helmReleaseSummaries:
    - conflictMessage: ClusterSummary management-3556efa5-sveltos-mgmt managing it
      releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Conflict
    - releaseName: nginx
      releaseNamespace: nginx
      status: Managing
      valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:      kcmv1.ServiceTypeHelm,
					Name:      "nginx",
					Namespace: "nginx",
					State:     kcmv1.ServiceStateProvisioning,
				},
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "postgres-operator",
					Namespace:      "postgres-operator",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "ClusterSummary management-3556efa5-sveltos-mgmt managing it",
				},
			},
		},
		{
			description: "nginx Conflict & postgres Conflict",
			summary: `
  status:
    dependencies: no dependencies
    featureSummaries:
    - consecutiveFailures: 2
    failureMessage: |
      cannot manage chart nginx/nginx. ClusterSummary management-3556efa5-sveltos-mgmt managing it.
      cannot manage chart postgres-operator/postgres-operator. ClusterSummary management-3556efa5-sveltos-mgmt managing it.
    featureID: Helm
    hash: 16CwFOvGTz25T2tUxoruIMDZaqv+AswuQylmSX/wKB8=
    lastAppliedTime: "2026-04-22T10:25:45Z"
    status: FailedNonRetriable
    helmReleaseSummaries:
    - conflictMessage: ClusterSummary management-3556efa5-sveltos-mgmt managing it
      releaseName: nginx
      releaseNamespace: nginx
      status: Conflict
    - conflictMessage: ClusterSummary management-3556efa5-sveltos-mgmt managing it
      releaseName: postgres-operator
      releaseNamespace: postgres-operator
      status: Conflict
`,
			serviceSet: &kcmv1.ServiceSet{
				Spec: kcmv1.ServiceSetSpec{
					Services: []kcmv1.ServiceWithValues{
						{Name: "nginx", Namespace: "nginx"},
						{Name: "postgres-operator", Namespace: "postgres-operator"},
					},
				},
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: "nginx", Namespace: "nginx", Type: kcmv1.ServiceTypeHelm},
						{Name: "postgres-operator", Namespace: "postgres-operator", Type: kcmv1.ServiceTypeHelm},
					},
				},
			},
			expected: []kcmv1.ServiceState{
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "nginx",
					Namespace:      "nginx",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "ClusterSummary management-3556efa5-sveltos-mgmt managing it",
				},
				{
					Type:           kcmv1.ServiceTypeHelm,
					Name:           "postgres-operator",
					Namespace:      "postgres-operator",
					State:          kcmv1.ServiceStateFailed,
					FailureMessage: "ClusterSummary management-3556efa5-sveltos-mgmt managing it",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			logger := ctrl.LoggerFrom(t.Context()).WithName("test")
			summary := &addoncontrollerv1beta1.ClusterSummary{}
			err := yaml.Unmarshal(bytes.TrimSpace([]byte(tc.summary)), summary)
			require.NoError(t, err)
			servicesState := servicesStateFromSummary(logger, summary, tc.serviceSet)
			compareStates(t, tc.description, tc.expected, servicesState)
			// b, _ := json.MarshalIndent(servicesState, "", "  ")
			// fmt.Printf("\n-------- %s --------\n%s\n----------------------------------------\n", tc.description, string(b))
		})
	}
}

func compareStates(t *testing.T, description string, expected, actual []kcmv1.ServiceState) {
	t.Helper()

	type projectedState struct {
		Name           string
		Namespace      string
		Type           kcmv1.ServiceType
		State          string
		FailureMessage string
	}

	expectedProjected := make([]projectedState, len(expected))
	for i, ex := range expected {
		expectedProjected[i] = projectedState{
			Name:           ex.Name,
			Namespace:      ex.Namespace,
			Type:           ex.Type,
			State:          ex.State,
			FailureMessage: ex.FailureMessage,
		}
	}

	actualProjected := make([]projectedState, len(actual))
	for i, ac := range actual {
		actualProjected[i] = projectedState{
			Name:           ac.Name,
			Namespace:      ac.Namespace,
			Type:           ac.Type,
			State:          ac.State,
			FailureMessage: ac.FailureMessage,
		}
	}

	require.ElementsMatch(t, expectedProjected, actualProjected, description)
}
