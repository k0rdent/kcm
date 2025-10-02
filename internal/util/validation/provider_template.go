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

package validation

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// InUseProviderParams describes in which regions this provider is currently in use
// and which Cluster API contract versions it supports
type InUseProviderParams struct {
	// Regions maps region names to a boolean indicating whether
	// this provider is in use in that region
	Regions map[string]bool
	// ProviderContracts is the list of supported Cluster API contract versions,
	// e.g. infrastructure-aws: []{v1alpha3, v1alpha4, v1beta1}
	// [contract versions]: https://cluster-api.sigs.k8s.io/developer/providers/contracts
	ProviderContracts []string
}

// GetInUseProvidersWithContracts constructs a map based on the given [github.com/K0rdent/kcm/api/v1beta1.ProviderTemplate]
// where keys are a provider name in the format (bootstrap|control-plane|infrastructure)-<name> and values are the
// corresponding CAPI [contract versions], e.g. infrastructure-aws: []{v1alpha3, v1alpha4, v1beta1}
//
// [contract versions]: https://cluster-api.sigs.k8s.io/developer/providers/contracts
func GetInUseProvidersWithContracts(ctx context.Context, cl client.Client, pTpl *kcmv1.ProviderTemplate) (map[string]InUseProviderParams, error) {
	inUseProviders := make(map[string]InUseProviderParams)
	for _, providerName := range pTpl.Status.Providers {
		clusterTemplates := new(kcmv1.ClusterTemplateList)
		if err := cl.List(ctx, clusterTemplates, client.MatchingFields{kcmv1.ClusterTemplateProvidersIndexKey: providerName}); err != nil {
			return nil, fmt.Errorf("failed to list ClusterTemplates: %w", err)
		}

		if len(clusterTemplates.Items) == 0 {
			continue
		}

		regions := make(map[string]bool)
		var providerContracts []string
		for _, cltpl := range clusterTemplates.Items {
			clds := new(kcmv1.ClusterDeploymentList)
			if err := cl.List(ctx, clds,
				client.MatchingFields{kcmv1.ClusterDeploymentTemplateIndexKey: cltpl.Name},
				client.Limit(1)); err != nil {
				return nil, fmt.Errorf("failed to list ClusterDeployments: %w", err)
			}

			if len(clds.Items) == 0 {
				continue
			}

			for _, cld := range clds.Items {
				regions[cld.Status.Region] = true
			}
			providerContracts = append(providerContracts, cltpl.Status.ProviderContracts[providerName])
		}
		inUseProviders[providerName] = InUseProviderParams{Regions: regions, ProviderContracts: providerContracts}
	}
	return inUseProviders, nil
}

func GetInUseProvidersWithContractsForParent(ctx context.Context, cl client.Client, pTpl *kcmv1.ProviderTemplate, obj ParentCluster) (map[string][]string, error) {
	kind := obj.GetObjectKind().GroupVersionKind().Kind

	var regionName string
	switch kind {
	case kcmv1.ManagementKind:
		// Parent object is a Management cluster: regionName remains empty
	case kcmv1.RegionKind:
		regionName = obj.GetName()
	default:
		return nil, fmt.Errorf("unsupported object kind %s; supported kinds are %s and %s", kind, kcmv1.ManagementKind, kcmv1.RegionKind)
	}

	inUseProviders, err := GetInUseProvidersWithContracts(ctx, cl, pTpl)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]string, len(inUseProviders))
	for providerName, providerParams := range inUseProviders {
		if providerParams.Regions[regionName] {
			result[providerName] = providerParams.ProviderContracts
		}
	}
	return result, nil
}
