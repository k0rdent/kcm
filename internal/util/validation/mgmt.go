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
	"errors"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// ErrProviderIsNotReady signals if the corresponding [github.com/K0rdent/kcm/api/v1beta1.ProviderTemplate] is not yet ready.
var ErrProviderIsNotReady = errors.New("provider is not yet ready")

// GetIncompatibleContracts validates if all of the providers specified in the given [github.com/K0rdent/kcm/api/v1beta1.Management]
// have compatible CAPI [contract versions]. Returns [ErrProviderIsNotReady] is the corresponding [github.com/K0rdent/kcm/api/v1beta1.ProviderTemplate]
// is not yet ready and the validation cannot proceed further.
//
// [contract versions]: https://cluster-api.sigs.k8s.io/developer/providers/contracts
func GetIncompatibleContracts(ctx context.Context, cl client.Client, release *kcmv1.Release, mgmt *kcmv1.Management) (string, error) {
	capiTplName := release.Spec.CAPI.Template
	if mgmt.Spec.Core != nil && mgmt.Spec.Core.CAPI.Template != "" {
		capiTplName = mgmt.Spec.Core.CAPI.Template
	}

	capiTpl := new(kcmv1.ProviderTemplate)
	if err := cl.Get(ctx, client.ObjectKey{Name: capiTplName}, capiTpl); err != nil {
		return "", fmt.Errorf("failed to get ProviderTemplate %s: %w", capiTplName, err)
	}

	if len(capiTpl.Status.CAPIContracts) > 0 && !capiTpl.Status.Valid {
		return "", fmt.Errorf("not valid ProviderTemplate %s: %w", capiTpl.Name, ErrProviderIsNotReady)
	}

	incompatibleContracts := strings.Builder{}
	for _, p := range mgmt.Spec.Providers {
		tplName := p.Template
		if tplName == "" {
			tplName = release.ProviderTemplate(p.Name)
		}

		if tplName == capiTpl.Name || tplName == "" {
			continue
		}

		pTpl := new(kcmv1.ProviderTemplate)
		if err := cl.Get(ctx, client.ObjectKey{Name: tplName}, pTpl); err != nil {
			return "", fmt.Errorf("failed to get ProviderTemplate %s: %w", tplName, err)
		}

		if len(pTpl.Status.CAPIContracts) == 0 {
			continue
		}

		if !pTpl.Status.Valid {
			return "", fmt.Errorf("not valid ProviderTemplate %s: %w", tplName, ErrProviderIsNotReady)
		}

		inUseProviders, err := GetInUseProvidersWithContracts(ctx, cl, pTpl)
		if err != nil {
			return "", fmt.Errorf("failed to get in-use providers for the template %s: %w", pTpl.Name, err)
		}

		exposedContracts := make(map[string]struct{})
		for capiVersion, providerContracts := range pTpl.Status.CAPIContracts {
			for contract := range strings.SplitSeq(providerContracts, "_") {
				exposedContracts[contract] = struct{}{}
			}

			if len(capiTpl.Status.CAPIContracts) > 0 {
				if _, ok := capiTpl.Status.CAPIContracts[capiVersion]; !ok {
					_, _ = incompatibleContracts.WriteString(fmt.Sprintf("core CAPI contract versions does not support %s version in the ProviderTemplate %s, ", capiVersion, pTpl.Name))
				}
			}
		}

		if len(inUseProviders) == 0 {
			continue
		}

		for provider, contracts := range inUseProviders {
			for _, contract := range contracts {
				if _, ok := exposedContracts[contract]; !ok {
					_, _ = incompatibleContracts.WriteString(fmt.Sprintf("missing contract version %s for %s provider that is required by one or more ClusterDeployment, ", contract, provider))
				}
			}
		}
	}

	return strings.TrimSuffix(incompatibleContracts.String(), ", "), nil
}

func ManagementDeletionAllowed(ctx context.Context, mgmtClient client.Client) error {
	regions := &kcmv1.RegionList{}
	err := mgmtClient.List(ctx, regions, client.Limit(1))
	if err != nil {
		return err
	}
	if len(regions.Items) > 0 {
		return errors.New("the Management object can't be removed if Region objects still exist")
	}
	clusterDeployments := new(kcmv1.ClusterDeploymentList)
	if err := mgmtClient.List(ctx, clusterDeployments, client.Limit(1)); err != nil {
		return fmt.Errorf("failed to list ClusterDeployments: %w", err)
	}

	if len(clusterDeployments.Items) > 0 {
		return errors.New("the Management object can't be removed if ClusterDeployment objects still exist")
	}
	return nil
}
