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

package providerinterface

import (
	"context"
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// clusterParent is a ClusterDeployment parent object, either Management or Region
type clusterParent interface {
	HelmReleaseName(string) string
	GetComponentsStatus() *kcmv1.ComponentsCommonStatus
}

// FindProviderInterfaceForInfra gets the first found ProviderInterface that is a part of the component exposing
// given infrastructure provider
func FindProviderInterfaceForInfra(ctx context.Context, rgnClient client.Client, parent clusterParent, infra string) *kcmv1.ProviderInterface {
	componentName := findComponentForInfra(parent.GetComponentsStatus().Components, infra)
	if componentName == "" {
		return nil
	}
	// Get the first found ProviderInterface from the <componentName> helm chart
	providerInterfaces := &kcmv1.ProviderInterfaceList{}
	if err := rgnClient.List(ctx, providerInterfaces,
		client.MatchingLabels{kcmv1.FluxHelmChartNameKey: parent.HelmReleaseName(componentName)},
		client.Limit(1)); err != nil {
		return nil
	}
	if len(providerInterfaces.Items) == 0 {
		return nil
	}
	return &providerInterfaces.Items[0]
}

func findComponentForInfra(exposedComponents map[string]kcmv1.ComponentStatus, infra string) string {
	for name, components := range exposedComponents {
		if slices.Contains(components.ExposedProviders, infra) {
			return name
		}
	}
	return ""
}
