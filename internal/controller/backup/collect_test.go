// Copyright 2024
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

package backup

import (
	"reflect"
	"testing"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func Test_sortDedup(t *testing.T) {
	type testInput struct {
		name      string
		selectors []*metav1.LabelSelector
		expected  []*metav1.LabelSelector
	}

	tests := []testInput{
		{
			name: "no dups unordered",
			selectors: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
				{
					MatchLabels: map[string]string{"app": "bar"},
				},
			},
			expected: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "bar"},
				},
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
			},
		},
		{
			name: "some dups in keys",
			selectors: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
				{
					MatchLabels: map[string]string{"app": "bar"},
				},
			},
			expected: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "bar"},
				},
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
			},
		},
		{
			name: "all dups",
			selectors: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
			},
			expected: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
			},
		},
		{
			name: "huge dups unordered",
			selectors: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"hmc.mirantis.com/component": "hmc"},
				},
				{
					MatchLabels: map[string]string{"controller.cert-manager.io/fao": "true"},
				},
				{
					MatchLabels: map[string]string{"helm.toolkit.fluxcd.io/name": "hmc"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "cluster-api"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"helm.toolkit.fluxcd.io/name": "unusual-cluster-name"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/cluster-name": "unusual-cluster-name"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-internal"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
			},
			expected: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/cluster-name": "unusual-cluster-name"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "cluster-api"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-internal"},
				},
				{
					MatchLabels: map[string]string{"controller.cert-manager.io/fao": "true"},
				},
				{
					MatchLabels: map[string]string{"helm.toolkit.fluxcd.io/name": "hmc"},
				},
				{
					MatchLabels: map[string]string{"helm.toolkit.fluxcd.io/name": "unusual-cluster-name"},
				},
				{
					MatchLabels: map[string]string{"hmc.mirantis.com/component": "hmc"},
				},
			},
		},
	}

	for _, test := range tests {
		actual := sortDedup(test.selectors)
		if !reflect.DeepEqual(actual, test.expected) {
			t.Errorf("sortDedup(%s): \n\tactual:\n\t%v\n\n\twant:\n\t%v", test.name, actual, test.expected)
		}
	}
}

func Test_selector(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
		want *metav1.LabelSelector
	}{
		{
			name: "basic selector",
			key:  "app",
			val:  "nginx",
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "nginx"},
			},
		},
		{
			name: "empty key",
			key:  "",
			val:  "value",
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{"": "value"},
			},
		},
		{
			name: "empty value",
			key:  "key",
			val:  "",
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{"key": ""},
			},
		},
		{
			name: "kubernetes specific labels",
			key:  "kubernetes.io/role",
			val:  "master",
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kubernetes.io/role": "master"},
			},
		},
		{
			name: "cluster-api provider",
			key:  clusterapiv1.ProviderNameLabel,
			val:  "bootstrap-k0smotron",
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{clusterapiv1.ProviderNameLabel: "bootstrap-k0smotron"},
			},
		},
		{
			name: "component label",
			key:  kcmv1.GenericComponentNameLabel,
			val:  kcmv1.GenericComponentLabelValueKCM,
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selector(tt.key, tt.val)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("selector() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getClusterDeploymentsSelectors(t *testing.T) {
	tests := []struct {
		name      string
		scope     *scope
		region    string
		wantCount int
		checkFunc func(t *testing.T, selectors []*metav1.LabelSelector)
	}{
		{
			name: "empty scope",
			scope: &scope{
				clientsByDeployment: map[string]deployClient{},
				clusterTemplates:    map[string]*kcmv1.ClusterTemplate{},
			},
			region:    "",
			wantCount: 0,
			checkFunc: func(t *testing.T, selectors []*metav1.LabelSelector) {
				t.Helper()
				if len(selectors) != 0 {
					t.Errorf("expected empty selectors, got %d", len(selectors))
				}
			},
		},
		{
			name: "management cluster only",
			scope: &scope{
				clientsByDeployment: map[string]deployClient{
					"ns/on-mgmt-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "on-mgmt-cluster",
								Namespace: "ns",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "",
							},
						},
					},
				},
				clusterTemplates: map[string]*kcmv1.ClusterTemplate{},
			},
			region:    "",
			wantCount: 2,
			checkFunc: func(t *testing.T, selectors []*metav1.LabelSelector) {
				t.Helper()
				expectSelector(t, selectors, kcmv1.FluxHelmChartNameKey, "on-mgmt-cluster")
				expectSelector(t, selectors, clusterapiv1.ClusterNameLabel, "on-mgmt-cluster")
			},
		},
		{
			name: "regional cluster only",
			scope: &scope{
				clientsByDeployment: map[string]deployClient{
					"ns/region-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "region-cluster",
								Namespace: "ns",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "region1",
							},
						},
					},
				},
				clusterTemplates: map[string]*kcmv1.ClusterTemplate{},
			},
			region:    "region1",
			wantCount: 2,
			checkFunc: func(t *testing.T, selectors []*metav1.LabelSelector) {
				t.Helper()
				expectSelector(t, selectors, kcmv1.FluxHelmChartNameKey, "region-cluster")
				expectSelector(t, selectors, clusterapiv1.ClusterNameLabel, "region-cluster")
			},
		},
		{
			name: "mixed deployments, filter for management",
			scope: &scope{
				clientsByDeployment: map[string]deployClient{
					"ns/on-mgmt-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "on-mgmt-cluster",
								Namespace: "ns",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "",
							},
						},
					},
					"ns/region-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "region-cluster",
								Namespace: "ns",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "region1",
							},
						},
					},
				},
				clusterTemplates: map[string]*kcmv1.ClusterTemplate{},
			},
			region:    "",
			wantCount: 2,
			checkFunc: func(t *testing.T, selectors []*metav1.LabelSelector) {
				t.Helper()
				expectSelector(t, selectors, kcmv1.FluxHelmChartNameKey, "on-mgmt-cluster")
				expectSelector(t, selectors, clusterapiv1.ClusterNameLabel, "on-mgmt-cluster")
				expectNotSelector(t, selectors, kcmv1.FluxHelmChartNameKey, "region-cluster")
				expectNotSelector(t, selectors, clusterapiv1.ClusterNameLabel, "region-cluster")
			},
		},
		{
			name: "mixed deployments, filter for region",
			scope: &scope{
				clientsByDeployment: map[string]deployClient{
					"ns/on-mgmt-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "on-mgmt-cluster",
								Namespace: "ns",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "",
							},
						},
					},
					"ns/region1-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "region1-cluster",
								Namespace: "ns",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "region1",
							},
						},
					},
					"ns/region2-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "region2-cluster",
								Namespace: "ns",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "region2",
							},
						},
					},
				},
				clusterTemplates: map[string]*kcmv1.ClusterTemplate{},
			},
			region:    "region1",
			wantCount: 2,
			checkFunc: func(t *testing.T, selectors []*metav1.LabelSelector) {
				t.Helper()
				expectSelector(t, selectors, kcmv1.FluxHelmChartNameKey, "region1-cluster")
				expectSelector(t, selectors, clusterapiv1.ClusterNameLabel, "region1-cluster")
				expectNotSelector(t, selectors, kcmv1.FluxHelmChartNameKey, "on-mgmt-cluster")
				expectNotSelector(t, selectors, clusterapiv1.ClusterNameLabel, "on-mgmt-cluster")
				expectNotSelector(t, selectors, kcmv1.FluxHelmChartNameKey, "region2-cluster")
				expectNotSelector(t, selectors, clusterapiv1.ClusterNameLabel, "region2-cluster")
			},
		},
		{
			name: "with template providers",
			scope: &scope{
				clientsByDeployment: map[string]deployClient{
					"ns/region-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "region-cluster",
								Namespace: "ns",
							},
							Spec: kcmv1.ClusterDeploymentSpec{
								Template: "template1",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "region1",
							},
						},
					},
				},
				clusterTemplates: map[string]*kcmv1.ClusterTemplate{
					"ns/template1": {
						Status: kcmv1.ClusterTemplateStatus{
							Providers: []string{"provider1", "provider2"},
						},
					},
				},
			},
			region:    "region1",
			wantCount: 4, // 2 for cluster + 2 for providers
			checkFunc: func(t *testing.T, selectors []*metav1.LabelSelector) {
				t.Helper()
				expectSelector(t, selectors, kcmv1.FluxHelmChartNameKey, "region-cluster")
				expectSelector(t, selectors, clusterapiv1.ClusterNameLabel, "region-cluster")
				expectSelector(t, selectors, clusterapiv1.ProviderNameLabel, "provider1")
				expectSelector(t, selectors, clusterapiv1.ProviderNameLabel, "provider2")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getClusterDeploymentsSelectors(tt.scope, tt.region)

			if len(got) != tt.wantCount {
				t.Errorf("getClusterDeploymentsSelectors() returned %d selectors, want %d", len(got), tt.wantCount)
			}

			tt.checkFunc(t, got)
		})
	}
}

func Test_getBackupTemplateSpec(t *testing.T) {
	tests := []struct {
		name      string
		scope     *scope
		region    string
		checkFunc func(t *testing.T, got *velerov1.BackupSpec)
	}{
		{
			name: "empty scope",
			scope: &scope{
				clientsByDeployment: map[string]deployClient{},
				clusterTemplates:    map[string]*kcmv1.ClusterTemplate{},
			},
			region: "",
			checkFunc: func(t *testing.T, got *velerov1.BackupSpec) {
				t.Helper()
				// Check basic fields
				if got.TTL.Duration != 30*24*time.Hour {
					t.Errorf("expected TTL %v, got %v", 30*24*time.Hour, got.TTL.Duration)
				}
				if !reflect.DeepEqual(got.IncludedNamespaces, []string{"*"}) {
					t.Errorf("expected IncludedNamespaces %v, got %v", []string{"*"}, got.IncludedNamespaces)
				}
				if !reflect.DeepEqual(got.ExcludedResources, []string{"clusters.cluster.x-k8s.io"}) {
					t.Errorf("expected ExcludedResources %v, got %v", []string{"clusters.cluster.x-k8s.io"}, got.ExcludedResources)
				}

				// Verify standard selectors are present
				expectSelector(t, got.OrLabelSelectors, kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM)
				expectSelector(t, got.OrLabelSelectors, certmanagerv1.PartOfCertManagerControllerLabelKey, "true")
				expectSelector(t, got.OrLabelSelectors, clusterapiv1.ProviderNameLabel, "cluster-api")

				// Check no extra selectors
				if len(got.OrLabelSelectors) != 3 {
					t.Errorf("expected exactly 3 selectors, got %d", len(got.OrLabelSelectors))
				}
			},
		},
		{
			name: "management cluster only",
			scope: &scope{
				clientsByDeployment: map[string]deployClient{
					"ns/on-mgmt-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "on-mgmt-cluster",
								Namespace: "ns",
							},
							Spec: kcmv1.ClusterDeploymentSpec{
								Template: "template1",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "",
							},
						},
					},
				},
				clusterTemplates: map[string]*kcmv1.ClusterTemplate{},
			},
			region: "",
			checkFunc: func(t *testing.T, got *velerov1.BackupSpec) {
				t.Helper()
				// Verify standard selectors
				expectSelector(t, got.OrLabelSelectors, kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM)
				expectSelector(t, got.OrLabelSelectors, certmanagerv1.PartOfCertManagerControllerLabelKey, "true")
				expectSelector(t, got.OrLabelSelectors, clusterapiv1.ProviderNameLabel, "cluster-api")

				// Verify management cluster selectors
				expectSelector(t, got.OrLabelSelectors, kcmv1.FluxHelmChartNameKey, "on-mgmt-cluster")
				expectSelector(t, got.OrLabelSelectors, clusterapiv1.ClusterNameLabel, "on-mgmt-cluster")

				// Expect exactly 5 selectors (3 standard + 2 for mgmt cluster)
				if len(got.OrLabelSelectors) != 5 {
					t.Errorf("expected exactly 5 selectors, got %d", len(got.OrLabelSelectors))
				}
			},
		},
		{
			name: "regional cluster only",
			scope: &scope{
				clientsByDeployment: map[string]deployClient{
					"ns/on-mgmt-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "on-mgmt-cluster",
								Namespace: "ns",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "",
							},
						},
					},
					"ns/region1-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "region1-cluster",
								Namespace: "ns",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "region1",
							},
						},
					},
				},
				clusterTemplates: map[string]*kcmv1.ClusterTemplate{},
			},
			region: "region1",
			checkFunc: func(t *testing.T, got *velerov1.BackupSpec) {
				t.Helper()
				// Verify standard selectors
				expectSelector(t, got.OrLabelSelectors, kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM)
				expectSelector(t, got.OrLabelSelectors, certmanagerv1.PartOfCertManagerControllerLabelKey, "true")
				expectSelector(t, got.OrLabelSelectors, clusterapiv1.ProviderNameLabel, "cluster-api")

				// Verify region1 cluster selectors
				expectSelector(t, got.OrLabelSelectors, kcmv1.FluxHelmChartNameKey, "region1-cluster")
				expectSelector(t, got.OrLabelSelectors, clusterapiv1.ClusterNameLabel, "region1-cluster")

				// Should NOT contain management cluster selectors
				expectNotSelector(t, got.OrLabelSelectors, kcmv1.FluxHelmChartNameKey, "on-mgmt-cluster")
				expectNotSelector(t, got.OrLabelSelectors, clusterapiv1.ClusterNameLabel, "on-mgmt-cluster")

				// Expect exactly 5 selectors (3 standard + 2 for regional cluster)
				if len(got.OrLabelSelectors) != 5 {
					t.Errorf("expected exactly 5 selectors, got %d", len(got.OrLabelSelectors))
				}
			},
		},
		{
			name: "with providers from template",
			scope: &scope{
				clientsByDeployment: map[string]deployClient{
					"ns/region1-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "region1-cluster",
								Namespace: "ns",
							},
							Spec: kcmv1.ClusterDeploymentSpec{
								Template: "template1",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "region1",
							},
						},
					},
				},
				clusterTemplates: map[string]*kcmv1.ClusterTemplate{
					"ns/template1": {
						Status: kcmv1.ClusterTemplateStatus{
							Providers: []string{"provider1", "provider2"},
						},
					},
				},
			},
			region: "region1",
			checkFunc: func(t *testing.T, got *velerov1.BackupSpec) {
				t.Helper()
				// Verify standard selectors
				expectSelector(t, got.OrLabelSelectors, kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM)
				expectSelector(t, got.OrLabelSelectors, certmanagerv1.PartOfCertManagerControllerLabelKey, "true")
				expectSelector(t, got.OrLabelSelectors, clusterapiv1.ProviderNameLabel, "cluster-api")

				// Verify region1 cluster selectors
				expectSelector(t, got.OrLabelSelectors, kcmv1.FluxHelmChartNameKey, "region1-cluster")
				expectSelector(t, got.OrLabelSelectors, clusterapiv1.ClusterNameLabel, "region1-cluster")

				// Verify template providers
				expectSelector(t, got.OrLabelSelectors, clusterapiv1.ProviderNameLabel, "provider1")
				expectSelector(t, got.OrLabelSelectors, clusterapiv1.ProviderNameLabel, "provider2")

				// Check total selectors (3 standard + 2 for cluster + 2 providers)
				if len(got.OrLabelSelectors) != 7 {
					t.Errorf("expected exactly 7 selectors, got %d", len(got.OrLabelSelectors))
				}
			},
		},
		{
			name: "multiple regional deployments, filtered by region",
			scope: &scope{
				clientsByDeployment: map[string]deployClient{
					"ns/region1-cluster1": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "region1-cluster1",
								Namespace: "ns",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "region1",
							},
						},
					},
					"ns/region1-cluster2": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "region1-cluster2",
								Namespace: "ns",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "region1",
							},
						},
					},
					"ns/region2-cluster": {
						cld: &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "region2-cluster",
								Namespace: "ns",
							},
							Status: kcmv1.ClusterDeploymentStatus{
								Region: "region2",
							},
						},
					},
				},
				clusterTemplates: map[string]*kcmv1.ClusterTemplate{},
			},
			region: "region1",
			checkFunc: func(t *testing.T, got *velerov1.BackupSpec) {
				t.Helper()
				// Verify standard selectors
				expectSelector(t, got.OrLabelSelectors, kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM)
				expectSelector(t, got.OrLabelSelectors, certmanagerv1.PartOfCertManagerControllerLabelKey, "true")
				expectSelector(t, got.OrLabelSelectors, clusterapiv1.ProviderNameLabel, "cluster-api")

				// Verify region1 clusters are included
				expectSelector(t, got.OrLabelSelectors, kcmv1.FluxHelmChartNameKey, "region1-cluster1")
				expectSelector(t, got.OrLabelSelectors, clusterapiv1.ClusterNameLabel, "region1-cluster1")
				expectSelector(t, got.OrLabelSelectors, kcmv1.FluxHelmChartNameKey, "region1-cluster2")
				expectSelector(t, got.OrLabelSelectors, clusterapiv1.ClusterNameLabel, "region1-cluster2")

				// Verify region2 cluster is excluded
				expectNotSelector(t, got.OrLabelSelectors, kcmv1.FluxHelmChartNameKey, "region2-cluster")
				expectNotSelector(t, got.OrLabelSelectors, clusterapiv1.ClusterNameLabel, "region2-cluster")

				// Check total selectors (3 standard + 4 for two region1 clusters)
				if len(got.OrLabelSelectors) != 7 {
					t.Errorf("expected exactly 7 selectors, got %d", len(got.OrLabelSelectors))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getBackupTemplateSpec(tt.scope, tt.region)
			tt.checkFunc(t, got)
		})
	}
}

// Helper functions to verify selectors
func expectSelector(t *testing.T, selectors []*metav1.LabelSelector, key, value string) {
	t.Helper()
	for _, selector := range selectors {
		if v, ok := selector.MatchLabels[key]; ok && v == value {
			return
		}
	}
	t.Errorf("expected selector with key=%s, value=%s not found", key, value)
}

func expectNotSelector(t *testing.T, selectors []*metav1.LabelSelector, key, value string) {
	t.Helper()
	for _, selector := range selectors {
		if v, ok := selector.MatchLabels[key]; ok && v == value {
			t.Errorf("unexpected selector found: key=%s, value=%s", key, value)
			return
		}
	}
}
