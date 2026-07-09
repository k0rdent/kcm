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
	"errors"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/helm"
)

var ErrMissingClusterIdentityRef = errors.New("cluster identity reference is not found")

// clusterParent is a ClusterDeployment parent object, either Management or Region
type clusterParent interface {
	HelmReleasePrefix() string
	GetComponentsStatus() *kcmv1.ComponentsCommonStatus
}

// FindClusterIdentity gets the first found ProviderInterface with the specified ClusterIdentity
// and returns the ClusterIdentity definition if specified
func FindClusterIdentity(ctx context.Context, rgnClient client.Client, clusterIdentityObjRef *corev1.ObjectReference) (*kcmv1.ClusterIdentity, error) {
	providerInterfaces := &kcmv1.ProviderInterfaceList{}
	if err := rgnClient.List(ctx, providerInterfaces); err != nil {
		return nil, err
	}
	if len(providerInterfaces.Items) == 0 {
		return nil, ErrMissingClusterIdentityRef
	}

	for _, pi := range providerInterfaces.Items {
		for _, ci := range pi.Spec.ClusterIdentities {
			gv := schema.GroupVersion{
				Group:   ci.Group,
				Version: ci.Version,
			}
			if gv.String() == clusterIdentityObjRef.APIVersion && ci.Kind == clusterIdentityObjRef.Kind {
				return &ci, nil
			}
		}
	}
	return nil, ErrMissingClusterIdentityRef
}

// FindProviderInterfaceForInfra gets the first found ProviderInterface that is a part of the component exposing
// the given infrastructure provider.
//
// It first tries the standard CAPI provider label ("cluster.x-k8s.io/provider"); on miss it falls back
// to the flux helm-chart-name label for backward compatibility with ProviderInterfaces installed via
// their provider's own HelmRelease that don't yet carry the CAPI label
func FindProviderInterfaceForInfra(ctx context.Context, rgnClient client.Client, parent clusterParent, infra string) *kcmv1.ProviderInterface {
	ll := ctrl.LoggerFrom(ctx).WithName("providerinterface").WithValues("infra provider", infra)

	ll.V(1).Info("Finding ProviderInterface for the given infrastructure provider")

	providerInterfaces := new(kcmv1.ProviderInterfaceList)
	if err := rgnClient.List(
		ctx, providerInterfaces,
		client.MatchingLabels{clusterapiv1.ProviderNameLabel: infra},
		client.Limit(1),
	); err != nil {
		ll.Error(err, "Failed to list ProviderInterfaces by CAPI provider label, falling back")
	} else if len(providerInterfaces.Items) > 0 {
		pi := &providerInterfaces.Items[0]
		ll.V(1).Info("Found ProviderInterface for the given infrastructure provider", "providerinterface", client.ObjectKeyFromObject(pi))
		return pi
	}

	// fallback: look up by the flux helm-chart-name label
	ll.V(1).Info("Falling back to flux helm-chart name")

	componentName := findComponentForInfra(parent.GetComponentsStatus().Components, infra)
	if componentName == "" {
		ll.Info("No component name found, skipping ProviderInterface search")
		return nil
	}

	providerInterfaces = &kcmv1.ProviderInterfaceList{}
	if err := rgnClient.List(
		ctx, providerInterfaces,
		client.MatchingLabels{kcmv1.FluxHelmChartNameKey: helm.ReleaseName(parent.HelmReleasePrefix(), componentName)},
		client.Limit(1),
	); err != nil {
		ll.Error(err, "Failed to list ProviderInterfaces by flux helm-chart name", "component name", componentName)
	} else if len(providerInterfaces.Items) > 0 {
		pi := &providerInterfaces.Items[0]
		ll.V(1).Info("Found ProviderInterface for the given infrastructure provider from old flux helm-chart name", "component name", componentName, "providerinterface", client.ObjectKeyFromObject(pi))
		return pi
	}

	ll.Info("No ProviderInterface has been found")

	return nil
}

func findComponentForInfra(exposedComponents map[string]kcmv1.ComponentStatus, infra string) string {
	for name, components := range exposedComponents {
		if slices.Contains(components.ExposedProviders, infra) {
			return name
		}
	}
	return ""
}
