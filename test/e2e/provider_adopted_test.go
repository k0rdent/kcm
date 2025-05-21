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

package e2e

import (
	"context"
	"fmt"
	"os"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/credential"
	"github.com/K0rdent/kcm/test/e2e/flux"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
	"github.com/K0rdent/kcm/test/e2e/upgrade"
)

var _ = Describe("Adopted Cluster Templates", Label("provider:cloud", "provider:adopted"), Ordered, func() {
	var (
		kc                *kubeclient.KubeClient
		clusterTemplates  []string
		clusterDeleteFunc func() error
		adoptedDeleteFunc func() error
		kubecfgDeleteFunc func() error
		clusterNames      []string

		providerConfigs []config.ProviderTestingConfig

		helmRepositorySpec = sourcev1.HelmRepositorySpec{
			URL: "https://kubernetes.github.io/ingress-nginx",
		}
		serviceTemplateSpec = kcmv1.ServiceTemplateSpec{
			Helm: &kcmv1.HelmSpec{
				ChartSpec: &sourcev1.HelmChartSpec{
					Chart: "ingress-nginx",
					SourceRef: sourcev1.LocalHelmChartSourceReference{
						Kind: sourcev1.HelmRepositoryKind,
						Name: "ingress-nginx",
					},
					Version: "4.12.1",
				},
			},
		}
	)

	const (
		helmRepositoryName  = "ingress-nginx"
		serviceTemplateName = "ingress-nginx-4-12-1"
	)

	BeforeAll(func() {
		By("Get testing configuration")
		providerConfigs = config.Config[config.TestingProviderAdopted]

		if len(providerConfigs) == 0 {
			Skip("Adopted ClusterDeployment testing is skipped")
		}

		By("Creating kube client")
		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)

		var err error
		clusterTemplates, err = templates.GetSortedClusterTemplates(context.Background(), kc.CrClient, internalutils.DefaultSystemNamespace)
		Expect(err).NotTo(HaveOccurred())

		By("Providing cluster identity and credentials")
		credential.Apply("", "aws")

		By("creating HelmRepository and ServiceTemplate", func() {
			flux.CreateHelmRepository(context.Background(), kc.CrClient, internalutils.DefaultSystemNamespace, helmRepositoryName, helmRepositorySpec)
			templates.CreateServiceTemplate(context.Background(), kc.CrClient, internalutils.DefaultSystemNamespace, serviceTemplateName, serviceTemplateSpec)
		})
	})

	AfterAll(func() {
		// If we failed collect the support bundle before the cleanup
		if CurrentSpecReport().Failed() && cleanup() {
			By("Collecting the support bundle from the management cluster")
			logs.SupportBundle("")
		}

		if cleanup() {
			By("Deleting resources")
			for _, deleteFunc := range []func() error{
				adoptedDeleteFunc,
				clusterDeleteFunc,
				kubecfgDeleteFunc,
			} {
				if deleteFunc != nil {
					err := deleteFunc()
					Expect(err).NotTo(HaveOccurred())
				}
			}
		}
	})

	It("should work with an Adopted cluster provider", func() {
		for i, testingConfig := range providerConfigs {
			// Deploy a standalone cluster and verify it is running/ready. Then, delete the management cluster and
			// recreate it. Next "adopt" the cluster we created and verify the services were deployed. Next we delete
			// the adopted cluster and finally the management cluster (AWS standalone).
			GinkgoT().Setenv(clusterdeployment.EnvVarAWSInstanceType, "t3.xlarge")

			_, _ = fmt.Fprintf(GinkgoWriter, "Testing configuration:\n%s\n", testingConfig.String())

			clusterName := clusterdeployment.GenerateClusterName(fmt.Sprintf("aws-%d", i))

			awsTemplates := templates.FindLatestTemplatesWithType(clusterTemplates, templates.TemplateAWSStandaloneCP, 1)
			Expect(awsTemplates).NotTo(BeEmpty())
			clusterTemplate := awsTemplates[0]

			templateBy(templates.TemplateAWSStandaloneCP, fmt.Sprintf("creating a ClusterDeployment %s with template %s", clusterName, clusterTemplate))
			sd := clusterdeployment.Generate(templates.TemplateAWSStandaloneCP, clusterName, clusterTemplate)

			deleteClusterFn := clusterdeployment.Create(context.Background(), kc.CrClient, sd)
			clusterNames = append(clusterNames, clusterName)
			clusterDeleteFunc = func() error { //nolint:unparam // required signature
				By(fmt.Sprintf("Deleting the %s ClusterDeployment", clusterName))
				Expect(deleteClusterFn()).NotTo(HaveOccurred())

				By(fmt.Sprintf("Verifying the %s ClusterDeployment deleted successfully", clusterName))
				deletionValidator := clusterdeployment.NewProviderValidator(
					templates.TemplateAWSStandaloneCP,
					clusterName,
					clusterdeployment.ValidationActionDelete,
				)
				Eventually(func() error {
					return deletionValidator.Validate(context.Background(), kc)
				}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
				return nil
			}

			templateBy(templates.TemplateAWSStandaloneCP, "waiting for infrastructure to deploy successfully")
			deploymentValidator := clusterdeployment.NewProviderValidator(
				templates.TemplateAWSStandaloneCP,
				clusterName,
				clusterdeployment.ValidationActionDeploy,
			)

			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kc)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			// create the adopted cluster using the AWS standalone cluster
			var rawData string
			_, rawData, kubecfgDeleteFunc = kc.WriteKubeconfig(context.Background(), clusterName)
			Expect(os.Setenv(clusterdeployment.EnvVarAdoptedKubeconfigData, rawData)).Should(Succeed())
			credential.Apply("", "adopted")

			adoptedClusterName := clusterdeployment.GenerateClusterName(fmt.Sprintf("adopted-%d", i))
			adoptedClusterTemplate := testingConfig.Template

			adoptedCluster := clusterdeployment.Generate(templates.TemplateAdoptedCluster, adoptedClusterName, adoptedClusterTemplate)
			By(fmt.Sprintf("Creating %s/%s Adopted ClusterDeployment", adoptedCluster.Namespace, adoptedCluster.Name))
			adoptedDeleteFunc = clusterdeployment.Create(context.Background(), kc.CrClient, adoptedCluster)

			// validate the adopted cluster
			templateBy(templates.TemplateAdoptedCluster, fmt.Sprintf("waiting for ClusterDeployment %s/%s to deploy successfully", adoptedCluster.Namespace, adoptedCluster.Name))
			deploymentValidator = clusterdeployment.NewProviderValidator(
				templates.TemplateAdoptedCluster,
				adoptedClusterName,
				clusterdeployment.ValidationActionDeploy,
			)
			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kc)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			if testingConfig.Upgrade {
				standaloneClient := kc.NewFromCluster(context.Background(), internalutils.DefaultSystemNamespace, adoptedClusterName)
				clusterUpgrade := upgrade.NewClusterUpgrade(
					kc.CrClient,
					standaloneClient.CrClient,
					internalutils.DefaultSystemNamespace,
					adoptedClusterName,
					testingConfig.UpgradeTemplate,
					upgrade.NewDefaultClusterValidator(),
				)
				clusterUpgrade.Run(context.Background())

				Eventually(func() error {
					return deploymentValidator.Validate(context.Background(), kc)
				}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
			}

			Expect(os.Unsetenv(clusterdeployment.EnvVarAdoptedKubeconfigData)).Should(Succeed())
		}
	})
})
