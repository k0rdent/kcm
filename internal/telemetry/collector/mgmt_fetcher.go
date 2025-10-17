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

package collector

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cenkalti/backoff/v4"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

type mgmtDataFetcher struct {
	expBackoff                                  backoff.BackOff
	tplName2Template                            map[client.ObjectKey]*kcmv1.ClusterTemplate
	mgmtUID                                     types.UID
	mcsList                                     *kcmv1.MultiClusterServiceList
	clusterDeployments                          *kcmv1.ClusterDeploymentList
	partialClustersToMatch, partialCapiClusters []metav1.PartialObjectMetadata

	fetchMgmtCluster      bool
	fetchClusterTemplates bool
}

func newMgmtDataFetcher(fetchMmgtCluster, fetchClusterTemplates bool) *mgmtDataFetcher {
	return &mgmtDataFetcher{
		tplName2Template:      make(map[client.ObjectKey]*kcmv1.ClusterTemplate),
		expBackoff:            backoff.NewExponentialBackOff(backoff.WithInitialInterval(500*time.Millisecond), backoff.WithMaxElapsedTime(10*time.Second)),
		fetchMgmtCluster:      fetchMmgtCluster,
		fetchClusterTemplates: fetchClusterTemplates,
	}
}

func (f *mgmtDataFetcher) retry(ctx context.Context, op func() error) error {
	return backoff.Retry(func() error {
		select {
		case <-ctx.Done():
			return backoff.Permanent(ctx.Err())
		default:
			return op()
		}
	}, backoff.WithContext(f.expBackoff, ctx))
}

func (f *mgmtDataFetcher) fetch(ctx context.Context, mgmtClient client.Client) error {
	if f.fetchMgmtCluster {
		mgmt := new(kcmv1.Management)
		if err := f.retry(ctx, func() error {
			return mgmtClient.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, mgmt)
		}); err != nil {
			return fmt.Errorf("failed to get Management: %w", err)
		}
		f.mgmtUID = mgmt.UID
	}

	if f.fetchClusterTemplates {
		templatesList := new(kcmv1.ClusterTemplateList)
		if err := f.retry(ctx, func() error {
			return mgmtClient.List(ctx, templatesList)
		}); err != nil {
			return fmt.Errorf("failed to list ClusterTemplates: %w", err)
		}

		f.tplName2Template = make(map[client.ObjectKey]*kcmv1.ClusterTemplate, len(templatesList.Items))
		for _, template := range templatesList.Items {
			f.tplName2Template[client.ObjectKey{Namespace: template.Namespace, Name: template.Name}] = &template
		}
	}

	f.clusterDeployments = new(kcmv1.ClusterDeploymentList)
	if err := f.retry(ctx, func() error {
		return mgmtClient.List(ctx, f.clusterDeployments)
	}); err != nil {
		return fmt.Errorf("failed to list ClusterDeployments: %w", err)
	}

	f.mcsList = new(kcmv1.MultiClusterServiceList)
	if err := f.retry(ctx, func() error {
		return mgmtClient.List(ctx, f.mcsList)
	}); err != nil {
		return fmt.Errorf("failed to list MultiClusterServices: %w", err)
	}

	var partialClustersToMatch, partialCapiClusters []metav1.PartialObjectMetadata
	if err := f.retry(ctx, func() error {
		var (
			lerr            error
			sveltosClusters []metav1.PartialObjectMetadata
		)
		partialCapiClusters, sveltosClusters, lerr = getPartialClustersToCountServices(ctx, mgmtClient)
		if lerr != nil {
			return lerr
		}
		partialClustersToMatch = slices.Concat(partialCapiClusters, sveltosClusters)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to list clusters to count services from: %w", err)
	}

	f.partialCapiClusters = partialCapiClusters
	f.partialClustersToMatch = partialClustersToMatch

	return nil
}

func getPartialClustersToCountServices(ctx context.Context, mgmtClient client.Client) (capiClusters, sveltosClusters []metav1.PartialObjectMetadata, _ error) {
	clustersGVK := schema.GroupVersionKind{
		Group:   clusterapiv1.GroupVersion.Group,
		Version: clusterapiv1.GroupVersion.Version,
		Kind:    clusterapiv1.ClusterKind,
	}
	partialCAPIClusters, err := listAsPartial(ctx, mgmtClient, clustersGVK)
	if err != nil {
		if !meta.IsNoMatchError(err) {
			return nil, nil, fmt.Errorf("failed to list all %s: %w", clustersGVK.String(), err)
		}
	}

	sveltosClusterGVK := schema.GroupVersionKind{
		Group:   libsveltosv1beta1.GroupVersion.Group,
		Version: libsveltosv1beta1.GroupVersion.Version,
		Kind:    libsveltosv1beta1.SveltosClusterKind,
	}
	partialSveltosClusters, err := listAsPartial(ctx, mgmtClient, sveltosClusterGVK)
	if err != nil {
		if !meta.IsNoMatchError(err) {
			return nil, nil, fmt.Errorf("failed to list all %s: %w", sveltosClusterGVK.String(), err)
		}
	}

	return partialCAPIClusters, partialSveltosClusters, nil
}
