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
	"time"

	"github.com/cenkalti/backoff/v4"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

// parentDataFetcher fetches data from the local cluster, either mgmt or regional.
type parentDataFetcher struct {
	expBackoff                backoff.BackOff
	mgmtTemplateName2Template map[client.ObjectKey]*kcmv1.ClusterTemplate // clustertemplates only for the mgmt+online case
	profilesList              *addoncontrollerv1beta1.ProfileList         // profiles to count number of child services
	partialCapiClusters       *[]metav1.PartialObjectMetadata             // used to count number of child services and as a main loop if regional
	clusters                  []client.Object                             // main loop items (clusterdeployments if mgmt; partial capi cluster if regional)
}

func newParentDataFetcher() *parentDataFetcher {
	return &parentDataFetcher{
		mgmtTemplateName2Template: make(map[client.ObjectKey]*kcmv1.ClusterTemplate),
		expBackoff:                backoff.NewExponentialBackOff(backoff.WithInitialInterval(500*time.Millisecond), backoff.WithMaxElapsedTime(10*time.Second)),
	}
}

func (f *parentDataFetcher) getScope(ctx context.Context, cl client.Client, onlineOrLocal scope) (scope, error) {
	isMgmtCluster, err := isMgmtCluster(ctx, cl)
	if err != nil {
		return -1, fmt.Errorf("failed to determine if management cluster: %w", err)
	}

	return f.getScopeExplicit(isMgmtCluster, onlineOrLocal)
}

func (f *parentDataFetcher) getScopeExplicit(isMgmtCluster bool, onlineOrLocal scope) (scope, error) { //nolint: revive // cannot be avoided
	kind := onlineOrLocal & (scopeOnline | scopeLocal)
	if kind != scopeOnline && kind != scopeLocal {
		return 0, fmt.Errorf("must be online or local scope (got %04b)", onlineOrLocal)
	}

	cluster := scopeManagement
	if !isMgmtCluster {
		cluster = scopeRegional
	}

	return cluster | kind, nil
}

func (f *parentDataFetcher) retry(ctx context.Context, op func() error) error {
	return backoff.Retry(func() error {
		select {
		case <-ctx.Done():
			return backoff.Permanent(ctx.Err())
		default:
			return op()
		}
	}, backoff.WithContext(f.expBackoff, ctx))
}

// fetch reads the parent cluster data page by page, which only pays off for an uncached
// parentReader, see [kubeutil.EachPage].
func (f *parentDataFetcher) fetch(ctx context.Context, parentReader client.Reader, dataScope scope) error {
	if dataScope.isOnline() && dataScope.isMgmt() {
		f.mgmtTemplateName2Template = make(map[client.ObjectKey]*kcmv1.ClusterTemplate)
		if err := f.retry(ctx, func() error {
			clear(f.mgmtTemplateName2Template) // a retried fetch starts over
			return kubeutil.EachPage(ctx, parentReader, func() *kcmv1.ClusterTemplateList { return new(kcmv1.ClusterTemplateList) },
				func(page *kcmv1.ClusterTemplateList) (bool, error) {
					for _, template := range page.Items {
						f.mgmtTemplateName2Template[client.ObjectKey{Namespace: template.Namespace, Name: template.Name}] = &template
					}
					return true, nil
				})
		}); err != nil {
			return fmt.Errorf("failed to list ClusterTemplates: %w", err)
		}
	}

	if dataScope.isMgmt() {
		if err := f.retry(ctx, func() error {
			f.clusters = nil // a retried fetch starts over
			return kubeutil.EachPage(ctx, parentReader, func() *kcmv1.ClusterDeploymentList { return new(kcmv1.ClusterDeploymentList) },
				func(page *kcmv1.ClusterDeploymentList) (bool, error) {
					for _, v := range page.Items {
						if v.Status.Region != "" {
							continue
						}

						f.clusters = append(f.clusters, &v)
					}
					return true, nil
				})
		}); err != nil {
			return fmt.Errorf("failed to list ClusterDeployments: %w", err)
		}
	}

	f.profilesList = new(addoncontrollerv1beta1.ProfileList)
	if err := f.retry(ctx, func() error {
		f.profilesList.Items = nil // a retried fetch starts over
		return kubeutil.EachPage(ctx, parentReader, func() *addoncontrollerv1beta1.ProfileList { return new(addoncontrollerv1beta1.ProfileList) },
			func(page *addoncontrollerv1beta1.ProfileList) (bool, error) {
				f.profilesList.Items = append(f.profilesList.Items, page.Items...)
				return true, nil
			})
	}); err != nil {
		return fmt.Errorf("failed to list Profiles: %w", err)
	}

	var partialCapiClusters []metav1.PartialObjectMetadata
	if err := f.retry(ctx, func() error {
		var lerr error
		partialCapiClusters, lerr = getPartialCAPIClusters(ctx, parentReader)
		if lerr != nil {
			return lerr
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to list clusters to count services from: %w", err)
	}

	f.partialCapiClusters = &partialCapiClusters

	// for non-mgmt (regional) case we have only CAPI Clusters (meta)
	if !dataScope.isMgmt() {
		f.clusters = make([]client.Object, len(partialCapiClusters))
		for i, v := range partialCapiClusters {
			f.clusters[i] = &v
		}
	}

	return nil
}

func getPartialCAPIClusters(ctx context.Context, mgmtReader client.Reader) ([]metav1.PartialObjectMetadata, error) {
	clustersGVK := clusterapiv1.GroupVersion.WithKind(clusterapiv1.ClusterKind)
	partialCAPIClusters, err := listAsPartial(ctx, mgmtReader, clustersGVK)
	if err != nil {
		if !meta.IsNoMatchError(err) {
			return nil, fmt.Errorf("failed to list all %s: %w", clustersGVK.String(), err)
		}
	}

	return partialCAPIClusters, nil
}
