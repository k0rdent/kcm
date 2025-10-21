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

func (f *parentDataFetcher) fetch(ctx context.Context, parentClient client.Client, dataScope scope) error {
	if dataScope.isOnline() && dataScope.isMgmt() {
		templatesList := new(kcmv1.ClusterTemplateList)
		if err := f.retry(ctx, func() error {
			return parentClient.List(ctx, templatesList)
		}); err != nil {
			return fmt.Errorf("failed to list ClusterTemplates: %w", err)
		}

		f.mgmtTemplateName2Template = make(map[client.ObjectKey]*kcmv1.ClusterTemplate, len(templatesList.Items))
		for _, template := range templatesList.Items {
			f.mgmtTemplateName2Template[client.ObjectKey{Namespace: template.Namespace, Name: template.Name}] = &template
		}
	}

	if dataScope.isMgmt() {
		clds := new(kcmv1.ClusterDeploymentList)
		if err := f.retry(ctx, func() error {
			return parentClient.List(ctx, clds)
		}); err != nil {
			return fmt.Errorf("failed to list ClusterDeployments: %w", err)
		}

		f.clusters = make([]client.Object, len(clds.Items))
		for i, v := range clds.Items {
			f.clusters[i] = &v
		}
	}

	f.profilesList = new(addoncontrollerv1beta1.ProfileList)
	if err := f.retry(ctx, func() error {
		return parentClient.List(ctx, f.profilesList)
	}); err != nil {
		return fmt.Errorf("failed to list Profiles: %w", err)
	}

	var partialCapiClusters []metav1.PartialObjectMetadata
	if err := f.retry(ctx, func() error {
		var lerr error
		partialCapiClusters, lerr = getPartialCAPIClusters(ctx, parentClient)
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

func getPartialCAPIClusters(ctx context.Context, mgmtClient client.Client) ([]metav1.PartialObjectMetadata, error) {
	clustersGVK := clusterapiv1.GroupVersion.WithKind(clusterapiv1.ClusterKind)
	partialCAPIClusters, err := listAsPartial(ctx, mgmtClient, clustersGVK)
	if err != nil {
		if !meta.IsNoMatchError(err) {
			return nil, fmt.Errorf("failed to list all %s: %w", clustersGVK.String(), err)
		}
	}

	return partialCAPIClusters, nil
}
