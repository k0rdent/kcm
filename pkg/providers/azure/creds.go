// Copyright 2024
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

package azure

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	capz "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/K0rdent/kcm/pkg/credspropagation"
)

func PropagateAzureSecrets(ctx context.Context, cfg *credspropagation.PropagationCfg) error {
	azureCluster := &capz.AzureCluster{}
	if err := cfg.Client.Get(ctx, client.ObjectKey{
		Name:      cfg.ClusterDeployment.Name,
		Namespace: cfg.ClusterDeployment.Namespace,
	}, azureCluster); err != nil {
		return fmt.Errorf("failed to get AzureCluster %s: %w", cfg.ClusterDeployment.Name, err)
	}

	azureClIdty := &capz.AzureClusterIdentity{}
	if err := cfg.Client.Get(ctx, client.ObjectKey{
		Name:      azureCluster.Spec.IdentityRef.Name,
		Namespace: azureCluster.Spec.IdentityRef.Namespace,
	}, azureClIdty); err != nil {
		return fmt.Errorf("failed to get AzureClusterIdentity %s: %w", azureCluster.Spec.IdentityRef.Name, err)
	}

	azureSecret := &corev1.Secret{}
	if err := cfg.Client.Get(ctx, client.ObjectKey{
		Name:      azureClIdty.Spec.ClientSecret.Name,
		Namespace: azureClIdty.Spec.ClientSecret.Namespace,
	}, azureSecret); err != nil {
		return fmt.Errorf("failed to get azure Secret %s: %w", azureClIdty.Spec.ClientSecret.Name, err)
	}

	ccmSecret, err := generateAzureCCMSecret(azureCluster, azureClIdty, azureSecret)
	if err != nil {
		return fmt.Errorf("failed to generate Azure CCM secret: %w", err)
	}

	if err := credspropagation.ApplyCCMConfigs(ctx, cfg.KubeconfSecret, ccmSecret); err != nil {
		return fmt.Errorf("failed to apply Azure CCM secret: %w", err)
	}

	return nil
}

func generateAzureCCMSecret(azureCluster *capz.AzureCluster, azureClIdty *capz.AzureClusterIdentity, azureSecret *corev1.Secret) (*corev1.Secret, error) {
	subnetName, secGroup, routeTable := getAzureSubnetData(azureCluster)
	azureJSONMap := map[string]any{
		"cloud":                        azureCluster.Spec.AzureEnvironment,
		"tenantId":                     azureClIdty.Spec.TenantID,
		"subscriptionId":               azureCluster.Spec.SubscriptionID,
		"aadClientId":                  azureClIdty.Spec.ClientID,
		"aadClientSecret":              string(azureSecret.Data["clientSecret"]),
		"resourceGroup":                azureCluster.Spec.ResourceGroup,
		"securityGroupName":            secGroup,
		"securityGroupResourceGroup":   azureCluster.Spec.NetworkSpec.Vnet.ResourceGroup,
		"location":                     azureCluster.Spec.Location,
		"vmType":                       "vmss",
		"vnetName":                     azureCluster.Spec.NetworkSpec.Vnet.Name,
		"vnetResourceGroup":            azureCluster.Spec.NetworkSpec.Vnet.ResourceGroup,
		"subnetName":                   subnetName,
		"routeTableName":               routeTable,
		"loadBalancerSku":              "Standard",
		"loadBalancerName":             "",
		"maximumLoadBalancerRuleCount": 250,
		"useManagedIdentityExtension":  false,
		"useInstanceMetadata":          true,
	}
	azureJSON, err := json.Marshal(azureJSONMap)
	if err != nil {
		return nil, fmt.Errorf("error marshalling azure.json: %w", err)
	}

	secretData := map[string][]byte{
		"cloud-config": azureJSON,
	}

	return credspropagation.MakeSecret("azure-cloud-provider", secretData), nil
}

func getAzureSubnetData(azureCluster *capz.AzureCluster) (subnetName, secGroup, routeTable string) {
	for _, sn := range azureCluster.Spec.NetworkSpec.Subnets {
		if sn.Role == "node" {
			subnetName = sn.Name
			secGroup = sn.SecurityGroup.Name
			routeTable = sn.RouteTable.Name
			break
		}
	}
	return subnetName, secGroup, routeTable
}
