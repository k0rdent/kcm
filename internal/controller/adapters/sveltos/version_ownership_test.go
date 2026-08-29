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
	"testing"
	"time"

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

const versionOwnershipSystemNamespace = "kcm-system"

func serviceSetWithLaggingVersion() *kcmv1.ServiceSet {
	return &kcmv1.ServiceSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "management-abcdef",
			Namespace: versionOwnershipSystemNamespace,
		},
		Spec: kcmv1.ServiceSetSpec{
			MultiClusterService: "kacs-mothership-auth-api",
			Provider: kcmv1.StateManagementProviderConfig{
				Name:           "ksm-projectsveltos",
				SelfManagement: true,
			},
			Services: []kcmv1.ServiceWithValues{{
				Name:      "kacs-auth-api",
				Namespace: "k0rdent-apis",
				Template:  "kacs-k0r-ai-catalog-1.1.0-rc.1",
				Version:   new("1.1.0-rc.1"),
			}},
		},
		Status: kcmv1.ServiceSetStatus{
			Services: []kcmv1.ServiceState{{
				Name:      "kacs-auth-api",
				Namespace: "k0rdent-apis",
				Template:  "kacs-k0r-ai-catalog-1.1.0-rc.1",
				// Last version the verifier confirmed, before the template bump.
				Version: new("1.0.0"),
				Type:    kcmv1.ServiceTypeHelm,
				State:   kcmv1.ServiceStateDeployed,
			}},
		},
	}
}

// KSM-207 regression. Status.Version on the Helm path is verifier-owned, but the
// verifier does not run when no health rules are configured. Before the fix that
// left Status.Version pinned at the last confirmed value forever, which
// permanently closes the dependency gate in serviceset.FilterServiceDependencies
// and silently freezes every dependent service at its old template.
func Test_verifyServiceStates_NoRules_KeepsVersionLive(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kcmv1.AddToScheme(scheme))

	serviceSet := serviceSetWithLaggingVersion()

	// No health-rule ConfigMaps exist, so rulesFromConfigMaps returns an empty
	// rule set and the verifier has no independent opinion to offer.
	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(serviceSet).Build()
	r := &ServiceSetReconciler{
		Client:          mgmtClient,
		SystemNamespace: versionOwnershipSystemNamespace,
		timeFunc:        time.Now,
		requeueInterval: time.Minute,
	}

	pendingStamp, err := r.verifyServiceStates(t.Context(), mgmtClient, serviceSet)
	require.NoError(t, err)
	assert.False(t, pendingStamp)

	require.Len(t, serviceSet.Status.Services, 1)
	got := serviceSet.Status.Services[0]
	require.NotNil(t, got.Version)
	assert.Equal(t, "1.1.0-rc.1", *got.Version,
		"with no health rules the verifier must fall back to the sveltos verdict and let Status.Version track spec; "+
			"leaving it pinned permanently closes the dependsOn gate (KSM-207)")
	assert.Equal(t, kcmv1.ServiceStateDeployed, got.State, "the fallback must not change the sveltos-reported state")
}

func Test_mirrorSpecVersionsForDeployedHelm(t *testing.T) {
	t.Parallel()

	specVersion := new("2.0.0")

	tests := map[string]struct {
		state       kcmv1.ServiceState
		wantVersion *string
		wantChanged bool
	}{
		"helm service reported Deployed is advanced to the spec version": {
			state: kcmv1.ServiceState{
				Name: "svc", Namespace: "ns",
				Type: kcmv1.ServiceTypeHelm, State: kcmv1.ServiceStateDeployed,
				Version: new("1.0.0"),
			},
			wantVersion: new("2.0.0"),
			wantChanged: true,
		},
		"helm service still rolling out keeps its old version and holds its dependents": {
			state: kcmv1.ServiceState{
				Name: "svc", Namespace: "ns",
				Type: kcmv1.ServiceTypeHelm, State: kcmv1.ServiceStateProvisioning,
				Version: new("1.0.0"),
			},
			wantVersion: new("1.0.0"),
			wantChanged: false,
		},
		"failed helm service keeps its old version": {
			state: kcmv1.ServiceState{
				Name: "svc", Namespace: "ns",
				Type: kcmv1.ServiceTypeHelm, State: kcmv1.ServiceStateFailed,
				Version: new("1.0.0"),
			},
			wantVersion: new("1.0.0"),
			wantChanged: false,
		},
		"non-helm services are owned by state.go and left alone": {
			state: kcmv1.ServiceState{
				Name: "svc", Namespace: "ns",
				Type: kcmv1.ServiceTypeResource, State: kcmv1.ServiceStateDeployed,
				Version: new("1.0.0"),
			},
			wantVersion: new("1.0.0"),
			wantChanged: false,
		},
		"helm service with no version yet is stamped": {
			state: kcmv1.ServiceState{
				Name: "svc", Namespace: "ns",
				Type: kcmv1.ServiceTypeHelm, State: kcmv1.ServiceStateDeployed,
			},
			wantVersion: new("2.0.0"),
			wantChanged: true,
		},
		"already at the spec version is not reported as a change": {
			state: kcmv1.ServiceState{
				Name: "svc", Namespace: "ns",
				Type: kcmv1.ServiceTypeHelm, State: kcmv1.ServiceStateDeployed,
				Version: new("2.0.0"),
			},
			wantVersion: new("2.0.0"),
			wantChanged: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			serviceSet := &kcmv1.ServiceSet{
				Status: kcmv1.ServiceSetStatus{Services: []kcmv1.ServiceState{tc.state}},
			}
			specVersions := map[client.ObjectKey]*string{
				{Namespace: "ns", Name: "svc"}: specVersion,
			}

			changed := mirrorSpecVersionsForDeployedHelm(serviceSet, specVersions)
			assert.Equal(t, tc.wantChanged, changed)

			got := serviceSet.Status.Services[0].Version
			if tc.wantVersion == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tc.wantVersion, *got)
		})
	}
}

// A service present in spec but absent from status must not be given a version
// belonging to some other service, and must not panic on the missing entry.
func Test_mirrorSpecVersionsForDeployedHelm_UnknownService(t *testing.T) {
	t.Parallel()

	serviceSet := &kcmv1.ServiceSet{
		Status: kcmv1.ServiceSetStatus{Services: []kcmv1.ServiceState{{
			Name: "not-in-spec", Namespace: "ns",
			Type: kcmv1.ServiceTypeHelm, State: kcmv1.ServiceStateDeployed,
			Version: new("1.0.0"),
		}}},
	}

	changed := mirrorSpecVersionsForDeployedHelm(serviceSet, map[client.ObjectKey]*string{
		{Namespace: "ns", Name: "some-other-svc"}: new("2.0.0"),
	})

	assert.False(t, changed)
	require.NotNil(t, serviceSet.Status.Services[0].Version)
	assert.Equal(t, "1.0.0", *serviceSet.Status.Services[0].Version)
}

// fillNotDeployedServices must carry Version over from spec. A nil Version
// normalises to the template name in FilterServiceDependencies, which can never
// equal the spec's semver version, so it would hold back every dependent of this
// service the moment it reaches Deployed.
func Test_fillNotDeployedServices_CarriesVersionFromSpec(t *testing.T) {
	t.Parallel()

	serviceSet := &kcmv1.ServiceSet{
		Spec: kcmv1.ServiceSetSpec{Services: []kcmv1.ServiceWithValues{{
			Name:      "svc",
			Namespace: "ns",
			Template:  "tmpl-1.2.3",
			Version:   new("1.2.3"),
		}}},
	}

	fillNotDeployedServices(serviceSet, time.Now)

	require.Len(t, serviceSet.Status.Services, 1)
	got := serviceSet.Status.Services[0]
	assert.Equal(t, kcmv1.ServiceStateNotDeployed, got.State)
	require.NotNil(t, got.Version, "Version must be carried over from spec, not left nil")
	assert.Equal(t, "1.2.3", *got.Version)
}
