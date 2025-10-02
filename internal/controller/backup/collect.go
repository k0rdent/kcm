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
	"maps"
	"slices"
	"strings"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// getBackupTemplateSpec creates a Velero backup specification that is region-aware.
// For management backups (empty region), it only includes non-regional resources.
// For regional backups, it only includes resources specific to that region.
func getBackupTemplateSpec(s *scope, region string) *velerov1.BackupSpec {
	bs := &velerov1.BackupSpec{
		IncludedNamespaces: []string{"*"},
		ExcludedResources:  []string{"clusters.cluster.x-k8s.io"},
		TTL:                metav1.Duration{Duration: 30 * 24 * time.Hour}, // velero's default, set it for the sake of UX
	}

	orSelectors := []*metav1.LabelSelector{
		// fixed ones
		selector(kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM),
		selector(certmanagerv1.PartOfCertManagerControllerLabelKey, "true"),
		selector(clusterapiv1.ProviderNameLabel, "cluster-api"),
	}

	bs.OrLabelSelectors = sortDedup(append(orSelectors, getClusterDeploymentsSelectors(s, region)...))

	return bs
}

func sortDedup(selectors []*metav1.LabelSelector) []*metav1.LabelSelector {
	const nonKubeSep = "_"

	kvs := make([]string, len(selectors))
	for i, s := range selectors {
		for k, v := range s.MatchLabels { // expect only one kv pair
			kvs[i] = k + nonKubeSep + v
		}
	}
	slices.Sort(kvs)

	for i, kv := range kvs {
		sepIdx := strings.Index(kv, nonKubeSep)
		if sepIdx < 0 {
			continue // make compiler happy
		}
		k := kv[:sepIdx]
		v := kv[sepIdx+len(nonKubeSep):]
		selectors[i] = selector(k, v)
	}

	return slices.Clip(
		slices.CompactFunc(selectors, func(a, b *metav1.LabelSelector) bool {
			return maps.Equal(a.MatchLabels, b.MatchLabels)
		}),
	)
}

// getClusterDeploymentsSelectors returns label selectors for ClusterDeployments
// filtered by region. If region is empty, only includes management cluster deployments.
// If region is specified, only includes deployments for that specific region.
func getClusterDeploymentsSelectors(s *scope, region string) []*metav1.LabelSelector {
	selectors := make([]*metav1.LabelSelector, 0, len(s.clientsByDeployment)*2)

	// Filter deployments based on region
	for _, v := range s.clientsByDeployment {
		// for management backup (empty region), skip deployments with a region
		if region == "" && v.cld.Status.Region != "" {
			continue
		}

		// for regional backup, only include deployments for this specific region
		if region != "" && v.cld.Status.Region != region {
			continue
		}

		selectors = append(selectors,
			selector(kcmv1.FluxHelmChartNameKey, v.cld.Name),
			selector(clusterapiv1.ClusterNameLabel, v.cld.Name),
		)

		// check if template is in-use, and add an in-use provider selector
		tpl := v.cld.Namespace + "/" + v.cld.Spec.Template
		if cltpl, ok := s.clusterTemplates[tpl]; ok {
			for _, provider := range cltpl.Status.Providers {
				selectors = append(selectors, selector(clusterapiv1.ProviderNameLabel, provider))
			}
		}
	}

	return selectors
}

func selector(k, v string) *metav1.LabelSelector {
	return &metav1.LabelSelector{
		MatchLabels: map[string]string{k: v},
	}
}
