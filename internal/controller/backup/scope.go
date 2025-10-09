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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	schemeutil "github.com/K0rdent/kcm/internal/util/scheme"
)

// RegionalClientFactory is a function type for creating regional clients
type RegionalClientFactory func(context.Context, client.Client, string, *kcmv1.Region, func() (*runtime.Scheme, error)) (client.Client, *rest.Config, error)

// defaultRegionalClientFactory uses the real implementation
var defaultRegionalClientFactory RegionalClientFactory = kubeutil.GetRegionalClient

type (
	scope struct {
		clusterTemplates   map[string]*kcmv1.ClusterTemplate
		regionClients      map[string]loadedClient
		mgmtBackup         *kcmv1.ManagementBackup
		clusterDeployments []*kcmv1.ClusterDeployment
	}

	// loadedClient holds the Client instance and an indicator whether the client
	// has actually been instantiated.
	loadedClient struct {
		cl     client.Client
		loaded bool
	}
)

func getScope(ctx context.Context, mgmtCl client.Client, systemNamespace string, clientFactory RegionalClientFactory) (*scope, error) {
	if clientFactory == nil {
		clientFactory = defaultRegionalClientFactory
	}

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
		clusterDeployments: clusterDeployments,
	}

	regionClients := make(map[string]loadedClient)
	l := ctrl.LoggerFrom(ctx)
	for _, cld := range clusterDeployments {
		if cld.Spec.Credential == "" {
			continue
		}

		cred := new(kcmv1.Credential)
		credKey := client.ObjectKey{Name: cld.Spec.Credential, Namespace: cld.Namespace}
		if err := mgmtCl.Get(ctx, credKey, cred); err != nil {
			return nil, fmt.Errorf("failed to get Credential %s: %w", credKey, err)
		}

		regionName := cred.Spec.Region
		if regionName == "" {
			continue
		}

		if _, ok := regionClients[regionName]; ok {
			continue
		}

		rgn := new(kcmv1.Region)
		if err := mgmtCl.Get(ctx, client.ObjectKey{Name: regionName}, rgn); err != nil {
			// NOTE: just in case, if for some reason Region object has gone, we don't have to fail everything
			// as long as we can still back up the management cluster and other regions
			if apierrors.IsNotFound(err) {
				l.Info("Region not found, skipping regional client creation", "region", regionName, "cred", client.ObjectKeyFromObject(cred))
				regionClients[regionName] = loadedClient{}
				continue
			}

			return nil, fmt.Errorf("failed to get Region %s: %w", regionName, err)
		}

		regionalCl, _, err := clientFactory(ctx, mgmtCl, systemNamespace, rgn, schemeutil.GetRegionalScheme)
		if err != nil {
			return nil, fmt.Errorf("failed to get regional client for the Region %s: %w", rgn.Name, err)
		}

		regionClients[regionName] = loadedClient{cl: regionalCl, loaded: true}
	}

	cltpls := make(map[string]*kcmv1.ClusterTemplate, len(clusterTemplates.Items))
	for _, v := range clusterTemplates.Items {
		cltpls[client.ObjectKeyFromObject(&v).String()] = &v
	}

	cs.clusterTemplates = cltpls

	return cs, nil
}
