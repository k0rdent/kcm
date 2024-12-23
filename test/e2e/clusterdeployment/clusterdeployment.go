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

package clusterdeployment

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/a8m/envsubst"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/utils"
)

type ProviderType string

const (
	ProviderCAPI    ProviderType = "cluster-api"
	ProviderAWS     ProviderType = "infrastructure-aws"
	ProviderAzure   ProviderType = "infrastructure-azure"
	ProviderVSphere ProviderType = "infrastructure-vsphere"
	ProviderAdopted ProviderType = "infrastructure-internal"
	providerLabel                = "cluster.x-k8s.io/provider"
)

type Template string

const (
	TemplateAWSStandaloneCP     Template = "aws-standalone-cp"
	TemplateAWSHostedCP         Template = "aws-hosted-cp"
	TemplateAWSEKS              Template = "aws-eks"
	TemplateAzureHostedCP       Template = "azure-hosted-cp"
	TemplateAzureStandaloneCP   Template = "azure-standalone-cp"
	TemplateVSphereStandaloneCP Template = "vsphere-standalone-cp"
	TemplateVSphereHostedCP     Template = "vsphere-hosted-cp"
	TemplateAdoptedCluster      Template = "adopted-cluster"
)

//go:embed resources/aws-standalone-cp.yaml.tpl
var awsStandaloneCPClusterDeploymentTemplateBytes []byte

//go:embed resources/aws-hosted-cp.yaml.tpl
var awsHostedCPClusterDeploymentTemplateBytes []byte

//go:embed resources/aws-eks.yaml.tpl
var awsEksClusterDeploymentTemplateBytes []byte

//go:embed resources/azure-standalone-cp.yaml.tpl
var azureStandaloneCPClusterDeploymentTemplateBytes []byte

//go:embed resources/azure-hosted-cp.yaml.tpl
var azureHostedCPClusterDeploymentTemplateBytes []byte

//go:embed resources/vsphere-standalone-cp.yaml.tpl
var vsphereStandaloneCPClusterDeploymentTemplateBytes []byte

//go:embed resources/vsphere-hosted-cp.yaml.tpl
var vsphereHostedCPClusterDeploymentTemplateBytes []byte

//go:embed resources/adopted-cluster.yaml.tpl
var adoptedClusterDeploymentTemplateBytes []byte

func FilterAllProviders() []string {
	return []string{
		utils.KCMControllerLabel,
		GetProviderLabel(ProviderAWS),
		GetProviderLabel(ProviderAzure),
		GetProviderLabel(ProviderCAPI),
		GetProviderLabel(ProviderVSphere),
	}
}

func GetProviderLabel(provider ProviderType) string {
	return fmt.Sprintf("%s=%s", providerLabel, provider)
}

func setClusterName(templateName Template) {
	var generatedName string

	mcName := os.Getenv(EnvVarClusterDeploymentName)
	if mcName == "" {
		mcName = "e2e-test-" + uuid.New().String()[:8]
	}

	providerName := strings.Split(string(templateName), "-")[0]

	// Append the provider name to the cluster name to ensure uniqueness between
	// different deployed ClusterDeployments.
	generatedName = fmt.Sprintf("%s-%s", mcName, providerName)
	if strings.Contains(string(templateName), "hosted") {
		generatedName = fmt.Sprintf("%s-%s", mcName, "hosted")
	}
	// TODO: quick w/a. The cluster names' generator will be refactored in https://github.com/k0rdent/kcm/pull/752
	if strings.Contains(string(templateName), "eks") {
		generatedName = fmt.Sprintf("%s-%s", mcName, "eks")
	}

	GinkgoT().Setenv(EnvVarClusterDeploymentName, generatedName)
}

// GetUnstructured returns an unstructured ClusterDeployment object based on the
// provider and template.
func GetUnstructured(templateName Template) *unstructured.Unstructured {
	GinkgoHelper()

	setClusterName(templateName)

	var clusterDeploymentTemplateBytes []byte
	switch templateName {
	case TemplateAWSStandaloneCP:
		clusterDeploymentTemplateBytes = awsStandaloneCPClusterDeploymentTemplateBytes
	case TemplateAWSHostedCP:
		// Validate environment vars that do not have defaults are populated.
		// We perform this validation here instead of within a Before block
		// since we populate the vars from standalone prior to this step.
		ValidateDeploymentVars([]string{
			EnvVarAWSVPCID,
			EnvVarAWSSubnets,
			EnvVarAWSSecurityGroupID,
		})
		clusterDeploymentTemplateBytes = awsHostedCPClusterDeploymentTemplateBytes
	case TemplateAWSEKS:
		clusterDeploymentTemplateBytes = awsEksClusterDeploymentTemplateBytes
	case TemplateVSphereStandaloneCP:
		clusterDeploymentTemplateBytes = vsphereStandaloneCPClusterDeploymentTemplateBytes
	case TemplateVSphereHostedCP:
		clusterDeploymentTemplateBytes = vsphereHostedCPClusterDeploymentTemplateBytes
	case TemplateAzureHostedCP:
		clusterDeploymentTemplateBytes = azureHostedCPClusterDeploymentTemplateBytes
	case TemplateAzureStandaloneCP:
		clusterDeploymentTemplateBytes = azureStandaloneCPClusterDeploymentTemplateBytes
	case TemplateAdoptedCluster:
		clusterDeploymentTemplateBytes = adoptedClusterDeploymentTemplateBytes
	default:
		Fail(fmt.Sprintf("Unsupported template: %s", templateName))
	}

	clusterDeploymentConfigBytes, err := envsubst.Bytes(clusterDeploymentTemplateBytes)
	Expect(err).NotTo(HaveOccurred(), "failed to substitute environment variables")

	var clusterDeploymentConfig map[string]any

	err = yaml.Unmarshal(clusterDeploymentConfigBytes, &clusterDeploymentConfig)
	Expect(err).NotTo(HaveOccurred(), "failed to unmarshal deployment config")

	return &unstructured.Unstructured{Object: clusterDeploymentConfig}
}

func ValidateDeploymentVars(v []string) {
	GinkgoHelper()

	for _, envVar := range v {
		Expect(os.Getenv(envVar)).NotTo(BeEmpty(), envVar+" must be set")
	}
}

func ValidateClusterTemplates(ctx context.Context, client *kubeclient.KubeClient) error {
	templates, err := client.ListClusterTemplates(ctx)
	if err != nil {
		return fmt.Errorf("failed to list cluster templates: %w", err)
	}

	for _, template := range templates {
		valid, found, err := unstructured.NestedBool(template.Object, "status", "valid")
		if err != nil {
			return fmt.Errorf("failed to get valid flag for template %s: %w", template.GetName(), err)
		}

		if !found {
			return fmt.Errorf("valid flag for template %s not found", template.GetName())
		}

		if !valid {
			return fmt.Errorf("template %s is still invalid", template.GetName())
		}
	}

	return nil
}
