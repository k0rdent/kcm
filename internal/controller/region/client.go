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

package region

import (
	"context"
	"errors"
	"fmt"

	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/utils/kube"
	schemeutil "github.com/K0rdent/kcm/internal/utils/scheme"
)

func GetKubeConfigSecretRef(region *kcmv1.Region) (*fluxmeta.SecretKeyReference, error) {
	if region.Spec.KubeConfig == nil && region.Spec.ClusterDeployment == nil {
		return nil, errors.New("either spec.kubeConfig or spec.clusterDeployment must be set")
	}
	if region.Spec.KubeConfig != nil && region.Spec.ClusterDeployment != nil {
		return nil, errors.New("only one of spec.kubeConfig and spec.clusterDeployment is allowed")
	}

	// Currently, only spec.KubeConfig is supported.
	// TODO: Add support for spec.ClusterDeployment reference. See https://github.com/k0rdent/kcm/issues/1903
	// for tracking.
	if region.Spec.KubeConfig != nil {
		return &fluxmeta.SecretKeyReference{Name: region.Spec.KubeConfig.Name, Key: region.Spec.KubeConfig.Key}, nil
	}
	return nil, errors.New("spec.kubeConfig is unset")
}

func GetClientFromRegionName(ctx context.Context, mgmtClient client.Client, systemNamespace, region string) (client.Client, error) {
	if region == "" {
		return mgmtClient, nil
	}
	rgn := &kcmv1.Region{}
	err := mgmtClient.Get(ctx, client.ObjectKey{Name: region}, rgn)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s region: %w", region, err)
	}
	rgnClient, _, err := GetClient(ctx, mgmtClient, systemNamespace, rgn)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for %s region %w", region, err)
	}
	return rgnClient, nil
}

func GetClient(
	ctx context.Context,
	mgmtClient client.Client,
	systemNamespace string,
	region *kcmv1.Region,
) (client.Client, *rest.Config, error) {
	scheme, err := schemeutil.GetRegionalScheme()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get regional scheme for the %s region: %w", region.Name, err)
	}

	kubeConfigSecretRef, err := GetKubeConfigSecretRef(region)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get kubeconfig secret reference: %w", err)
	}
	secretRef := client.ObjectKey{Namespace: systemNamespace, Name: kubeConfigSecretRef.Name}
	factory, restCfg := kube.DefaultClientFactoryWithRestConfig()
	rgnlClient, err := kube.GetChildClient(ctx, mgmtClient, secretRef, kubeConfigSecretRef.Key, scheme, factory)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get rest config for the %s region: %w", region.Name, err)
	}

	return rgnlClient, restCfg, nil
}
