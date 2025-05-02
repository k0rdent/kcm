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

// GetInUseProvidersWithContracts constructs a map based on the given [github.com/K0rdent/kcm/api/v1beta1.ProviderTemplate]
// where keys are a provider name in the format (bootstrap|control-plane|infrastructure)-<name> and values are the
// corresponding CAPI [contract versions], e.g. infrastructure-aws: []{v1alpha3, v1alpha4, v1beta1}
//
// [contract versions]: https://cluster-api.sigs.k8s.io/developer/providers/contracts
func GetInUseProvidersWithContracts(ctx context.Context, cl client.Client, pTpl *kcmv1.ProviderTemplate) (map[string][]string, error) {
	inUseProviders := make(map[string][]string)
	for _, providerName := range pTpl.Status.Providers {
		clusterTemplates := new(kcmv1.ClusterTemplateList)
		if err := cl.List(ctx, clusterTemplates, client.MatchingFields{kcmv1.ClusterTemplateProvidersIndexKey: providerName}); err != nil {
			return nil, fmt.Errorf("failed to list ClusterTemplates: %w", err)
		}

		if len(clusterTemplates.Items) == 0 {
			continue
		}

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

			inUseProviders[providerName] = append(inUseProviders[providerName], cltpl.Status.ProviderContracts[providerName])
		}
	}
	return inUseProviders, nil
}
