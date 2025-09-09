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
	"slices"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/controller/region"
	"github.com/K0rdent/kcm/internal/providerinterface"
)

// ClusterDeployCredential validates a [github.com/K0rdent/kcm/api/v1beta1.Credential] object referred
// in the given [github.com/K0rdent/kcm/api/v1beta1.ClusterDeployment] is ready and
// supported by the given [github.com/K0rdent/kcm/api/v1beta1.ClusterTemplate].
func ClusterDeployCredential(ctx context.Context, cl client.Client, systemNamespace string, cd *kcmv1.ClusterDeployment, clusterTemplate *kcmv1.ClusterTemplate) error {
	if len(clusterTemplate.Status.Providers) == 0 {
		return fmt.Errorf("no providers have been found in the ClusterTemplate %s", client.ObjectKeyFromObject(clusterTemplate))
	}

	hasInfra := false
	for _, v := range clusterTemplate.Status.Providers {
		if strings.HasPrefix(v, kcmv1.InfrastructureProviderPrefix) {
			hasInfra = true
			break
		}
	}

	if !hasInfra {
		return fmt.Errorf("no infrastructure providers have been found in the ClusterTemplate %s", client.ObjectKeyFromObject(clusterTemplate))
	}

	cred := new(kcmv1.Credential)
	credKey := client.ObjectKey{Namespace: cd.Namespace, Name: cd.Spec.Credential}
	if err := cl.Get(ctx, credKey, cred); err != nil {
		return fmt.Errorf("failed to get Credential %s referred in the ClusterDeployment %s: %w", credKey, client.ObjectKeyFromObject(cd), err)
	}

	if !cred.Status.Ready {
		return fmt.Errorf("the Credential %s is not Ready", credKey)
	}

	return isCredIdentitySupportsClusterTemplate(ctx, cl, systemNamespace, cred, clusterTemplate)
}

func getProviderClusterIdentityKinds(ctx context.Context, rgnClient client.Client, parent clusterParent, infraProviderName string) []string {
	pi := providerinterface.FindProviderInterfaceForInfra(ctx, rgnClient, parent, infraProviderName)
	if pi == nil {
		return nil
	}

	return pi.Spec.ClusterIdentityKinds
}

func isCredIdentitySupportsClusterTemplate(ctx context.Context, mgmtClient client.Client, systemNamespace string, cred *kcmv1.Credential, clusterTemplate *kcmv1.ClusterTemplate) error {
	idtyKind := cred.Spec.IdentityRef.Kind

	errMsg := func(provider string) error {
		return fmt.Errorf("provider %s does not support ClusterIdentity Kind %s from the Credential %s", provider, idtyKind, client.ObjectKeyFromObject(cred))
	}

	const secretKind = "Secret"
	rgnClient, err := region.GetClientFromRegionName(ctx, mgmtClient, systemNamespace, cred.Spec.Region)
	if err != nil {
		return fmt.Errorf("failed to get client for %s region: %w", cred.Spec.Region, err)
	}

	parent, err := getParent(ctx, mgmtClient, cred)
	if err != nil {
		return fmt.Errorf("failed to get parent cluster for %s credential: %w", client.ObjectKeyFromObject(cred), err)
	}

	for _, providerName := range clusterTemplate.Status.Providers {
		if !strings.HasPrefix(providerName, kcmv1.InfrastructureProviderPrefix) {
			continue
		}

		if providerName == kcmv1.InfrastructureProviderPrefix+"internal" {
			if idtyKind != secretKind {
				return errMsg(providerName)
			}

			continue
		}

		idtys := getProviderClusterIdentityKinds(ctx, rgnClient, parent, providerName)
		if len(idtys) == 0 {
			return fmt.Errorf("unsupported infrastructure provider %s", providerName)
		}

		if !slices.Contains(idtys, idtyKind) {
			return errMsg(providerName)
		}
	}

	return nil
}
