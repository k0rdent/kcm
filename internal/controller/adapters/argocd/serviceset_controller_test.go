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

package argocd

import (
	"testing"

	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	argocdv1 "github.com/K0rdent/kcm/api/argocd/v1alpha1"
	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func serviceSet() *kcmv1.ServiceSet {
	return &kcmv1.ServiceSet{Name: "cluster-mcs", Namespace: "kcm-system"}
}

func helmSource() *argocdv1.ApplicationSource {
	return &argocdv1.ApplicationSource{
		RepoURL:        "ghcr.io/k0rdent/catalog/charts",
		Chart:          "traefik",
		TargetRevision: "41.2.0",
	}
}

func TestBuildApplication(t *testing.T) {
	svc := kcmv1.ServiceWithValues{Name: "traefik", Namespace: "traefik", Template: "traefik-41-2-0"}
	tmpl := &kcmv1.ServiceTemplate{Name: "traefik-41-2-0", Namespace: "kcm-system"}

	app := buildApplication(serviceSet(), svc, tmpl, helmSource(), "replicas: 2\n",
		argocdv1.ApplicationDestination{Server: inClusterServer}, providerConfig{}, "argocd", "app")

	require.Equal(t, "argocd", app.Namespace)
	require.Equal(t, []string{argocdv1.ResourcesFinalizer}, app.Finalizers)
	require.Equal(t, "cluster-mcs", app.Labels[serviceSetNameLabel])
	require.Equal(t, "traefik", app.Labels[serviceNameLabel])

	require.Equal(t, defaultProject, app.Spec.Project)
	require.Equal(t, inClusterServer, app.Spec.Destination.Server)
	require.Equal(t, "traefik", app.Spec.Destination.Namespace)

	require.NotNil(t, app.Spec.Source.Helm)
	require.Equal(t, "traefik", app.Spec.Source.Helm.ReleaseName)
	require.Equal(t, "replicas: 2\n", app.Spec.Source.Helm.Values)

	require.NotNil(t, app.Spec.SyncPolicy.Automated)
	require.True(t, app.Spec.SyncPolicy.Automated.Prune)
	require.True(t, app.Spec.SyncPolicy.Automated.SelfHeal)
	require.Contains(t, app.Spec.SyncPolicy.SyncOptions, "CreateNamespace=true")
}

// A service with no namespace lands in one named after itself, as it does
// under the other adapters.
func TestBuildApplicationDefaultsNamespaceToServiceName(t *testing.T) {
	svc := kcmv1.ServiceWithValues{Name: "traefik", Template: "t"}
	app := buildApplication(serviceSet(), svc, &kcmv1.ServiceTemplate{}, helmSource(), "",
		argocdv1.ApplicationDestination{Server: inClusterServer}, providerConfig{}, "argocd", "app")
	require.Equal(t, "traefik", app.Spec.Destination.Namespace)
}

// Argo CD has no suspend: dropping the automated policy is what stops it from
// syncing behind KCM's back.
func TestBuildApplicationPauseDropsAutomatedSync(t *testing.T) {
	set := serviceSet()
	set.Annotations = map[string]string{kcmv1.ServiceSetPausedAnnotation: "true"}

	app := buildApplication(set, kcmv1.ServiceWithValues{Name: "svc", Namespace: "ns"},
		&kcmv1.ServiceTemplate{}, helmSource(), "", argocdv1.ApplicationDestination{}, providerConfig{}, "argocd", "app")

	require.Nil(t, app.Spec.SyncPolicy.Automated)
}

func TestProviderConfigIgnoresForeignKeys(t *testing.T) {
	// KCM fills the config with sveltos-shaped keys when the user sets none.
	cfg, err := providerConfigFromJSON(&apiextv1.JSON{
		Raw: []byte(`{"syncMode":"Continuous","priority":10,"project":"kcm","prune":false,"retryLimit":2}`),
	})
	require.NoError(t, err)
	require.Equal(t, "kcm", cfg.Project)
	require.NotNil(t, cfg.Prune)
	require.False(t, *cfg.Prune)
	require.Equal(t, int64(2), retryLimit(cfg))

	app := buildApplication(serviceSet(), kcmv1.ServiceWithValues{Name: "svc", Namespace: "ns"},
		&kcmv1.ServiceTemplate{}, helmSource(), "", argocdv1.ApplicationDestination{}, cfg, "argocd", "app")
	require.Equal(t, "kcm", app.Spec.Project)
	require.False(t, app.Spec.SyncPolicy.Automated.Prune)
	require.True(t, app.Spec.SyncPolicy.Automated.SelfHeal)
}

func TestServiceStateFromApplication(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    argocdv1.ApplicationStatus
		wantState string
		wantMsg   string
	}{
		{
			name:      "nothing reported yet",
			wantState: kcmv1.ServiceStateProvisioning,
		},
		{
			name: "healthy and synced",
			status: argocdv1.ApplicationStatus{
				Health: argocdv1.HealthStatus{Status: argocdv1.HealthStatusHealthy},
				Sync:   argocdv1.SyncStatus{Status: argocdv1.SyncStatusSynced},
			},
			wantState: kcmv1.ServiceStateDeployed,
		},
		{
			// Healthy but out of sync is the previous revision still standing,
			// not the one that was asked for.
			name: "healthy but out of sync",
			status: argocdv1.ApplicationStatus{
				Health: argocdv1.HealthStatus{Status: argocdv1.HealthStatusHealthy},
				Sync:   argocdv1.SyncStatus{Status: argocdv1.SyncStatusOutOfSync},
			},
			wantState: kcmv1.ServiceStateProvisioning,
		},
		{
			name: "sync failed",
			status: argocdv1.ApplicationStatus{
				Health:         argocdv1.HealthStatus{Status: argocdv1.HealthStatusHealthy},
				Sync:           argocdv1.SyncStatus{Status: argocdv1.SyncStatusSynced},
				OperationState: &argocdv1.OperationState{Phase: argocdv1.OperationFailed, Message: "one or more objects failed"},
			},
			wantState: kcmv1.ServiceStateFailed,
			wantMsg:   "one or more objects failed",
		},
		{
			name: "degraded",
			status: argocdv1.ApplicationStatus{
				Health: argocdv1.HealthStatus{Status: argocdv1.HealthStatusDegraded, Message: "pod crash looping"},
			},
			wantState: kcmv1.ServiceStateFailed,
			wantMsg:   "pod crash looping",
		},
		{
			// A chart that cannot even be pulled never becomes an operation,
			// so the reason is only in the conditions.
			name: "comparison error with no operation",
			status: argocdv1.ApplicationStatus{
				Health:     argocdv1.HealthStatus{Status: argocdv1.HealthStatusDegraded},
				Conditions: []argocdv1.ApplicationCondition{{Type: "ComparisonError", Message: "chart not found"}},
			},
			wantState: kcmv1.ServiceStateFailed,
			wantMsg:   "chart not found",
		},
		{
			name: "progressing",
			status: argocdv1.ApplicationStatus{
				Health: argocdv1.HealthStatus{Status: argocdv1.HealthStatusProgressing},
				Sync:   argocdv1.SyncStatus{Status: argocdv1.SyncStatusSynced},
			},
			wantState: kcmv1.ServiceStateProvisioning,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, msg := serviceStateFromApplication(&argocdv1.Application{Status: tc.status})
			require.Equal(t, tc.wantState, state)
			require.Equal(t, tc.wantMsg, msg)
		})
	}
}

func TestDeployedChartVersion(t *testing.T) {
	synced := &argocdv1.Application{Status: argocdv1.ApplicationStatus{
		Sync: argocdv1.SyncStatus{Status: argocdv1.SyncStatusSynced, Revision: "41.2.0"},
	}}
	require.Equal(t, "41.2.0", deployedChartVersion(synced))

	// A Git revision is a commit, not a version, and must not be stamped as one.
	git := &argocdv1.Application{Status: argocdv1.ApplicationStatus{
		Sync: argocdv1.SyncStatus{Status: argocdv1.SyncStatusSynced, Revision: "9f2c1ab"},
	}}
	require.Empty(t, deployedChartVersion(git))

	outOfSync := &argocdv1.Application{Status: argocdv1.ApplicationStatus{
		Sync: argocdv1.SyncStatus{Status: argocdv1.SyncStatusOutOfSync, Revision: "41.2.0"},
	}}
	require.Empty(t, deployedChartVersion(outOfSync))
}

// Argo CD takes one values document, so what the other adapters hand to Helm
// as several has to be merged here - in Helm's order, later wins.
func TestMergeValues(t *testing.T) {
	merged, err := mergeValues([]string{
		"controller:\n  replicas: 1\n  image: base\n",
		"",
		"controller:\n  replicas: 3\nextra: true\n",
	})
	require.NoError(t, err)
	require.YAMLEq(t, "controller:\n  replicas: 3\n  image: base\nextra: true\n", merged)
}

func TestMergeValuesEmpty(t *testing.T) {
	merged, err := mergeValues([]string{"", "   \n"})
	require.NoError(t, err)
	require.Empty(t, merged)
}

func TestApplicationNameIsBounded(t *testing.T) {
	set := &kcmv1.ServiceSet{Name: longString(150), Namespace: longString(150)}
	svc := kcmv1.ServiceWithValues{Name: longString(100), Namespace: longString(100)}
	name := applicationName(set, svc)
	require.LessOrEqual(t, len(name), 253)
	require.NotEqual(t, name, applicationName(set, kcmv1.ServiceWithValues{
		Name: longString(100), Namespace: longString(99) + "x",
	}))
}

func TestApplicationNameCarriesTheServiceSetNamespace(t *testing.T) {
	svc := kcmv1.ServiceWithValues{Name: "traefik", Namespace: "traefik"}
	a := applicationName(&kcmv1.ServiceSet{Name: "set", Namespace: "ns-a"}, svc)
	b := applicationName(&kcmv1.ServiceSet{Name: "set", Namespace: "ns-b"}, svc)
	require.NotEqual(t, a, b, "Applications of two namespaces share one Argo CD namespace")
}

func TestSyncPolicyCarriesExtraOptions(t *testing.T) {
	cfg := providerConfig{SyncOptions: []string{"ServerSideApply=true"}}
	policy := syncPolicy(cfg, &kcmv1.ServiceSet{ObjectMeta: metav1.ObjectMeta{}})
	require.Contains(t, policy.SyncOptions, "CreateNamespace=true")
	require.Contains(t, policy.SyncOptions, "ServerSideApply=true")
}

func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
