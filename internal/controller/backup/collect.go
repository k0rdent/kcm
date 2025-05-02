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
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func getBackupTemplateSpec(ctx context.Context, cl client.Client) (*velerov1.BackupSpec, error) {
	bs := &velerov1.BackupSpec{
		IncludedNamespaces: []string{"*"},
		ExcludedResources:  []string{"clusters.cluster.x-k8s.io"},
		TTL:                metav1.Duration{Duration: 30 * 24 * time.Hour}, // velero's default, set it for the sake of UX
	}

	orSelectors := []*metav1.LabelSelector{
		// fixed ones
		selector(kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM),
		selector(certmanagerv1.PartOfCertManagerControllerLabelKey, "true"),
		selector(clusterapiv1beta1.ProviderNameLabel, "cluster-api"),
	}

	clusterTemplates := new(kcmv1.ClusterTemplateList)
	if err := cl.List(ctx, clusterTemplates); err != nil {
		return nil, fmt.Errorf("failed to list ClusterTemplates: %w", err)
	}

	if len(clusterTemplates.Items) == 0 { // just collect child clusters names
		cldSelectors, err := getClusterDeploymentsSelectors(ctx, cl, "")
		if err != nil {
			return nil, fmt.Errorf("failed to get selectors for all clusterdeployments: %w", err)
		}

		bs.OrLabelSelectors = sortDedup(append(orSelectors, cldSelectors...))

		return bs, nil
	}

	for _, cltpl := range clusterTemplates.Items {
		cldSelectors, err := getClusterDeploymentsSelectors(ctx, cl, cltpl.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get selectors for clusterdeployments referencing %s clustertemplate: %w", client.ObjectKeyFromObject(&cltpl), err)
		}

		// add only enabled providers
		if len(cldSelectors) > 0 {
			for _, provider := range cltpl.Status.Providers {
				orSelectors = append(orSelectors, selector(clusterapiv1beta1.ProviderNameLabel, provider))
			}
		}

		orSelectors = append(orSelectors, cldSelectors...)
	}

	bs.OrLabelSelectors = sortDedup(orSelectors)

	return bs, nil
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

func getClusterDeploymentsSelectors(ctx context.Context, cl client.Client, clusterTemplateRef string) ([]*metav1.LabelSelector, error) {
	cldeploys := new(kcmv1.ClusterDeploymentList)
	opts := []client.ListOption{}
	if clusterTemplateRef != "" {
		opts = append(opts, client.MatchingFields{kcmv1.ClusterDeploymentTemplateIndexKey: clusterTemplateRef})
	}

	if err := cl.List(ctx, cldeploys, opts...); err != nil {
		return nil, fmt.Errorf("failed to list ClusterDeployments: %w", err)
	}

	selectors := make([]*metav1.LabelSelector, len(cldeploys.Items)*2)
	for i, cldeploy := range cldeploys.Items {
		selectors[i*2] = selector(kcmv1.FluxHelmChartNameKey, cldeploy.Name)
		selectors[i*2+1] = selector(clusterapiv1beta1.ClusterNameLabel, cldeploy.Name)
	}

	return selectors, nil
}

func selector(k, v string) *metav1.LabelSelector {
	return &metav1.LabelSelector{
		MatchLabels: map[string]string{k: v},
	}
}
