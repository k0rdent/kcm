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

package components

import (
	"context"
	"testing"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// fakeCluster implements clusterInterface for testing Cleanup.
type fakeCluster struct {
	client.Object
	components       kcmv1.ComponentsCommonSpec
	status           kcmv1.ComponentsCommonStatus
	kcmComponentInfo kcmv1.KCMComponentInfo
	hrPrefix         string
}

func (f *fakeCluster) Components() kcmv1.ComponentsCommonSpec {
	return f.components
}

func (f *fakeCluster) GetComponentsStatus() *kcmv1.ComponentsCommonStatus {
	return &f.status
}

func (f *fakeCluster) KCMComponentInfo(_ *kcmv1.Release, _ string) kcmv1.KCMComponentInfo {
	return f.kcmComponentInfo
}

func (f *fakeCluster) HelmReleasePrefix() string {
	return f.hrPrefix
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, helmcontrollerv2.AddToScheme(s))
	require.NoError(t, sourcev1.AddToScheme(s))
	require.NoError(t, kcmv1.AddToScheme(s))
	return s
}

func makeHelmRelease(name, namespace string, labels map[string]string, ownerRefs []metav1.OwnerReference) *helmcontrollerv2.HelmRelease { //nolint:unparam
	return &helmcontrollerv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Spec: helmcontrollerv2.HelmReleaseSpec{
			ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind:      sourcev1.HelmChartKind,
				Name:      "chart-" + name,
				Namespace: namespace,
			},
		},
	}
}

func makeSecret(name, namespace string, labels map[string]string) *corev1.Secret { //nolint:unparam
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Data: map[string][]byte{"key": []byte("value")},
	}
}

func Test_Cleanup(t *testing.T) {
	const (
		namespace = "test-ns"
		kcmChart  = "kcm"
	)

	managedLabels := map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue}

	tests := []struct {
		name string
		// Objects present in the management cluster client.
		mgmtObjects []client.Object
		// Objects present in the regional cluster client.
		rgnObjects []client.Object
		// Cluster spec providers (components still desired).
		providers []kcmv1.Provider
		// Label selector to pass to `Cleanup`.
		labelSelector *metav1.LabelSelector
		// HelmRelease prefix for the cluster.
		hrPrefix string

		wantErr bool
		// HelmReleases expected to remain after `Cleanup`.
		expectHRsRemain []string
		// HelmReleases expected to be deleted after `Cleanup`.
		expectHRsDeleted []string
		// Secrets expected to remain on the regional client.
		expectSecretsRemain []string
		// Secrets expected to be deleted from the regional client.
		expectSecretsDeleted []string
	}{
		{
			name: "deletes orphaned HelmRelease and its KCM-managed provider config secret",
			mgmtObjects: []client.Object{
				makeHelmRelease("orphaned-provider", namespace, managedLabels, nil),
			},
			rgnObjects: []client.Object{
				makeSecret("orphaned-provider-variables", namespace, managedLabels),
			},
			expectHRsDeleted:     []string{"orphaned-provider"},
			expectSecretsDeleted: []string{"orphaned-provider-variables"},
		},
		{
			name: "skips provider config secret without KCM managed label",
			mgmtObjects: []client.Object{
				makeHelmRelease("orphaned-provider", namespace, managedLabels, nil),
			},
			rgnObjects: []client.Object{
				makeSecret("orphaned-provider-variables", namespace, nil), // no managed label
			},
			expectHRsDeleted:    []string{"orphaned-provider"},
			expectSecretsRemain: []string{"orphaned-provider-variables"},
		},
		{
			name: "skips HelmRelease with owner references (non-orphaned)",
			mgmtObjects: []client.Object{
				makeHelmRelease("owned-provider", namespace, managedLabels, []metav1.OwnerReference{
					{APIVersion: kcmv1.GroupVersion.String(), Kind: kcmv1.ClusterDeploymentKind, Name: "some-owner", UID: types.UID("uid")},
				}),
			},
			rgnObjects: []client.Object{
				makeSecret("owned-provider-variables", namespace, managedLabels),
			},
			expectHRsRemain:     []string{"owned-provider"},
			expectSecretsRemain: []string{"owned-provider-variables"},
		},
		{
			name: "no error when provider config secret does not exist",
			mgmtObjects: []client.Object{
				makeHelmRelease("orphaned-no-secret", namespace, managedLabels, nil),
			},
			rgnObjects:       []client.Object{}, // no secret
			expectHRsDeleted: []string{"orphaned-no-secret"},
		},
		{
			name: "uses rgnClient for secret operations (secret only on regional)",
			mgmtObjects: []client.Object{
				makeHelmRelease("rgn-orphaned", namespace, managedLabels, nil),
			},
			rgnObjects: []client.Object{
				makeSecret("rgn-orphaned-variables", namespace, managedLabels),
			},
			expectHRsDeleted:     []string{"rgn-orphaned"},
			expectSecretsDeleted: []string{"rgn-orphaned-variables"},
		},
		{
			name: "skips HelmRelease matching core CAPI name",
			mgmtObjects: []client.Object{
				makeHelmRelease(kcmv1.CoreCAPIName, namespace, managedLabels, nil),
			},
			rgnObjects: []client.Object{
				makeSecret(kcmv1.CoreCAPIName+"-variables", namespace, managedLabels),
			},
			expectHRsRemain:     []string{kcmv1.CoreCAPIName},
			expectSecretsRemain: []string{kcmv1.CoreCAPIName + "-variables"},
		},
		{
			name: "skips HelmRelease matching KCM chart name",
			mgmtObjects: []client.Object{
				makeHelmRelease(kcmChart, namespace, managedLabels, nil),
			},
			rgnObjects: []client.Object{
				makeSecret(kcmChart+"-variables", namespace, managedLabels),
			},
			expectHRsRemain:     []string{kcmChart},
			expectSecretsRemain: []string{kcmChart + "-variables"},
		},
		{
			name: "skips HelmRelease matching a provider still in spec",
			mgmtObjects: []client.Object{
				makeHelmRelease("my-infra-provider", namespace, managedLabels, nil),
			},
			rgnObjects: []client.Object{
				makeSecret("my-infra-provider-variables", namespace, managedLabels),
			},
			providers:           []kcmv1.Provider{{Name: "my-infra-provider"}},
			expectHRsRemain:     []string{"my-infra-provider"},
			expectSecretsRemain: []string{"my-infra-provider-variables"},
		},
		{
			name: "respects HelmRelease prefix when matching providers",
			mgmtObjects: []client.Object{
				makeHelmRelease("region1-my-provider", namespace, managedLabels, nil),
				makeHelmRelease("region1-orphaned", namespace, managedLabels, nil),
			},
			rgnObjects: []client.Object{
				makeSecret("region1-my-provider-variables", namespace, managedLabels),
				makeSecret("region1-orphaned-variables", namespace, managedLabels),
			},
			providers:            []kcmv1.Provider{{Name: "my-provider"}},
			hrPrefix:             "region1",
			expectHRsRemain:      []string{"region1-my-provider"},
			expectSecretsRemain:  []string{"region1-my-provider-variables"},
			expectHRsDeleted:     []string{"region1-orphaned"},
			expectSecretsDeleted: []string{"region1-orphaned-variables"},
		},
		{
			name: "deletes multiple orphaned HelmReleases and their secrets",
			mgmtObjects: []client.Object{
				makeHelmRelease("orphan-1", namespace, managedLabels, nil),
				makeHelmRelease("orphan-2", namespace, managedLabels, nil),
			},
			rgnObjects: []client.Object{
				makeSecret("orphan-1-variables", namespace, managedLabels),
				makeSecret("orphan-2-variables", namespace, managedLabels),
			},
			expectHRsDeleted:     []string{"orphan-1", "orphan-2"},
			expectSecretsDeleted: []string{"orphan-1-variables", "orphan-2-variables"},
		},
		{
			name: "filters by label selector",
			mgmtObjects: []client.Object{
				// This HR has the managed label + the extra label that matches the selector.
				makeHelmRelease("labeled-orphan", namespace, map[string]string{
					kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
					"env":                    "test",
				}, nil),
				// This HR has the managed label but NOT the extra label.
				makeHelmRelease("unlabeled-orphan", namespace, managedLabels, nil),
			},
			rgnObjects: []client.Object{
				makeSecret("labeled-orphan-variables", namespace, managedLabels),
				makeSecret("unlabeled-orphan-variables", namespace, managedLabels),
			},
			labelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "test"},
			},
			expectHRsDeleted:     []string{"labeled-orphan"},
			expectSecretsDeleted: []string{"labeled-orphan-variables"},
			// The one without the extra label won't be listed due to selector filtering.
			expectHRsRemain:     []string{"unlabeled-orphan"},
			expectSecretsRemain: []string{"unlabeled-orphan-variables"},
		},
		{
			name:        "no HelmReleases present, no error",
			mgmtObjects: []client.Object{},
			rgnObjects:  []client.Object{},
			wantErr:     false,
		},
		{
			name: "skips HelmRelease matching a Release templates chart name",
			mgmtObjects: []client.Object{
				// "my-release-tpl" is the TemplatesChartFromReleaseName for "my-release"
				makeHelmRelease("my-release-tpl", namespace, managedLabels, nil),
				// Release object whose TemplatesChartFromReleaseName("my-release") == "my-release-tpl"
				&kcmv1.Release{
					ObjectMeta: metav1.ObjectMeta{
						Name: "my-release",
					},
					Spec: kcmv1.ReleaseSpec{
						Version: "v0.0.1",
						KCM:     kcmv1.CoreProviderTemplate{Template: "kcm-template"},
						CAPI:    kcmv1.CoreProviderTemplate{Template: "capi-template"},
					},
				},
			},
			rgnObjects: []client.Object{
				makeSecret("my-release-tpl-variables", namespace, managedLabels),
			},
			expectHRsRemain:     []string{"my-release-tpl"},
			expectSecretsRemain: []string{"my-release-tpl-variables"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newScheme(t)

			mgmtClient := fake.NewClientBuilder().WithScheme(s).
				WithObjects(tt.mgmtObjects...).
				Build()
			rgnClient := fake.NewClientBuilder().WithScheme(s).
				WithObjects(tt.rgnObjects...).
				Build()

			release := &kcmv1.Release{
				ObjectMeta: metav1.ObjectMeta{Name: "test-release"},
				Spec: kcmv1.ReleaseSpec{
					KCM:  kcmv1.CoreProviderTemplate{Template: "kcm-template"},
					CAPI: kcmv1.CoreProviderTemplate{Template: "capi-template"},
				},
			}

			cluster := &fakeCluster{
				Object: &kcmv1.Management{
					ObjectMeta: metav1.ObjectMeta{Name: "test-mgmt"},
				},
				components: kcmv1.ComponentsCommonSpec{
					Providers: tt.providers,
				},
				kcmComponentInfo: kcmv1.KCMComponentInfo{ChartName: kcmChart},
				hrPrefix:         tt.hrPrefix,
			}

			err := Cleanup(context.Background(), mgmtClient, rgnClient, cluster, release, tt.labelSelector, namespace)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			ctx := context.Background()

			for _, name := range tt.expectHRsDeleted {
				hr := &helmcontrollerv2.HelmRelease{}
				err := mgmtClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, hr)
				require.Error(t, err, "HelmRelease %q should have been deleted", name)
			}

			for _, name := range tt.expectHRsRemain {
				hr := &helmcontrollerv2.HelmRelease{}
				err := mgmtClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, hr)
				require.NoError(t, err, "HelmRelease %q should still exist", name)
			}

			for _, name := range tt.expectSecretsDeleted {
				secret := &corev1.Secret{}
				err := rgnClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, secret)
				require.Error(t, err, "Secret %q should have been deleted from rgnClient", name)
			}

			for _, name := range tt.expectSecretsRemain {
				secret := &corev1.Secret{}
				err := rgnClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, secret)
				assert.NoError(t, err, "Secret %q should still exist on rgnClient", name)
			}
		})
	}
}
