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

package backup

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/utils/kube"
	schemeutil "github.com/K0rdent/kcm/internal/utils/scheme"
)

type (
	scope struct {
		clientsByDeployment map[string]deployClient
		clusterTemplates    map[string]*kcmv1.ClusterTemplate
		mgmtBackup          *kcmv1.ManagementBackup
		hasRegions          bool
	}

	deployClient struct {
		cld *kcmv1.ClusterDeployment
		cl  client.Client
	}
)

func getScope(ctx context.Context, mgmtCl client.Client, systemNamespace string) (*scope, error) {
	clusterTemplates := new(kcmv1.ClusterTemplateList)
	if err := mgmtCl.List(ctx, clusterTemplates); err != nil {
		return nil, fmt.Errorf("failed to list ClusterTemplates: %w", err)
	}

	var clusterDeployments []*kcmv1.ClusterDeployment
	// collect either all of the deployments, or only those that are being used
	if len(clusterTemplates.Items) == 0 {
		cldeploys := new(kcmv1.ClusterDeploymentList)
		if err := mgmtCl.List(ctx, cldeploys); err != nil {
			return nil, fmt.Errorf("failed to list ClusterDeployments: %w", err)
		}

		for _, v := range cldeploys.Items {
			clusterDeployments = append(clusterDeployments, &v)
		}
	} else {
		for _, cltpl := range clusterTemplates.Items {
			cldeploys := new(kcmv1.ClusterDeploymentList)
			if err := mgmtCl.List(ctx, cldeploys, client.MatchingFields{kcmv1.ClusterDeploymentTemplateIndexKey: cltpl.Name}); err != nil {
				return nil, fmt.Errorf("failed to list ClusterDeployments used by the ClusterTemplate %s: %w", client.ObjectKeyFromObject(&cltpl), err)
			}

			for _, v := range cldeploys.Items {
				clusterDeployments = append(clusterDeployments, &v)
			}
		}
	}

	cs := &scope{
		clientsByDeployment: make(map[string]deployClient, len(clusterDeployments)),
	}

	eg, gctx := errgroup.WithContext(ctx)

	mu := sync.Mutex{}

	for _, cld := range clusterDeployments {
		cldName := client.ObjectKeyFromObject(cld).String()

		eg.Go(func() error {
			cl := mgmtCl

			if cld.Status.Region != "" {
				rgn := new(kcmv1.Region)
				if err := mgmtCl.Get(gctx, client.ObjectKey{Name: cld.Status.Region}, rgn); err != nil {
					return fmt.Errorf("failed to get Region %s: %w", cld.Status.Region, err)
				}

				regionalCl, _, err := kube.GetRegionalClient(gctx, mgmtCl, systemNamespace, rgn, schemeutil.GetRegionalScheme)
				if err != nil {
					return fmt.Errorf("failed to get regional client for the Region %s: %w", rgn.Name, err)
				}

				cl = regionalCl
			}

			mu.Lock()
			cs.clientsByDeployment[cldName] = deployClient{
				cl:  cl,
				cld: cld,
			}
			mu.Unlock()

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err // already wrapped
	}

	cltpls := make(map[string]*kcmv1.ClusterTemplate, len(clusterTemplates.Items))
	for _, v := range clusterTemplates.Items {
		cltpls[client.ObjectKeyFromObject(&v).String()] = &v
	}

	cs.clusterTemplates = cltpls
	cs.hasRegions = slices.ContainsFunc(clusterDeployments, func(d *kcmv1.ClusterDeployment) bool { return d.Status.Region != "" })

	return cs, nil
}
