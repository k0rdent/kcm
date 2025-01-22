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
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/azure"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/clusteridentity"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
	"github.com/K0rdent/kcm/test/utils"
)

var _ = Context("Azure Templates", Label("provider:cloud", "provider:azure"), Ordered, func() {
	var (
		kc                    *kubeclient.KubeClient
		standaloneClusters    = make(map[string]clusterInfo)
		hostedDeleteFuncs     []func() error
		standaloneDeleteFuncs []func() error
		kubeconfigDeleteFuncs []func() error

		providerConfigs []config.ProviderTestingConfig
	)

	BeforeAll(func() {
		By("get testing configuration")
		providerConfigs = config.Config[config.TestingProviderAzure]

		if len(providerConfigs) == 0 {
			Skip("Azure ClusterDeployment testing is skipped")
		}

		By("ensuring Azure credentials are set")
		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)
		ci := clusteridentity.New(kc, clusterdeployment.ProviderAzure)
		ci.WaitForValidCredential(kc)
		Expect(os.Setenv(clusterdeployment.EnvVarAzureClusterIdentity, ci.IdentityName)).Should(Succeed())
	})

	AfterAll(func() {
		// If we failed collect logs from each of the affiliated controllers
		// as well as the output of clusterctl to store as artifacts.
		if CurrentSpecReport().Failed() && cleanup() {
			if kc != nil {
				By("collecting failure logs from the management controllers")
				var standaloneClusterNames []string
				for clusterName := range standaloneClusters {
					standaloneClusterNames = append(standaloneClusterNames, clusterName)
				}
				logs.Collector{
					Client:        kc,
					ProviderTypes: []clusterdeployment.ProviderType{clusterdeployment.ProviderAzure, clusterdeployment.ProviderCAPI},
					ClusterNames:  standaloneClusterNames,
				}.CollectAll()
			}
			for clusterName, clusterInfo := range standaloneClusters {
				if clusterInfo.client != nil {
					By(fmt.Sprintf("collecting failure logs from controllers of the %s cluster", clusterName))
					logs.Collector{
						Client:        clusterInfo.client,
						ProviderTypes: []clusterdeployment.ProviderType{clusterdeployment.ProviderAzure, clusterdeployment.ProviderCAPI},
						ClusterNames:  []string{clusterInfo.hostedClusterName},
					}.CollectAll()
				}
			}
		}

		if cleanup() {
			By("deleting resources")
			deleteFuncs := append(hostedDeleteFuncs, append(standaloneDeleteFuncs, kubeconfigDeleteFuncs...)...)
			for _, deleteFunc := range deleteFuncs {
				err := deleteFunc()
				Expect(err).NotTo(HaveOccurred())
			}
		}
	})

	It("should work with an Azure provider", func() {
		for i, testingConfig := range providerConfigs {
			_, _ = fmt.Fprintf(GinkgoWriter, "Testing configuration:\n%s\n", testingConfig.String())

			sdName := clusterdeployment.GenerateClusterName(fmt.Sprintf("azure-%d", i))
			sdTemplate := testingConfig.Template
			templateBy(templates.TemplateAzureStandaloneCP, fmt.Sprintf("creating a ClusterDeployment %s with template %s", sdName, sdTemplate))

			sd := clusterdeployment.GetUnstructured(templates.TemplateAzureStandaloneCP, sdName, sdTemplate)

			standaloneDeleteFunc := kc.CreateClusterDeployment(context.Background(), sd)
			standaloneClusters[sdName] = clusterInfo{}
			standaloneDeleteFuncs = append(standaloneDeleteFuncs, func() error {
				By(fmt.Sprintf("Deleting the %s ClusterDeployment", sdName))
				err := standaloneDeleteFunc()
				Expect(err).NotTo(HaveOccurred())

				By(fmt.Sprintf("Verifying the %s ClusterDeployment deleted successfully", sdName))
				deploymentValidator := clusterdeployment.NewProviderValidator(
					templates.TemplateAzureStandaloneCP,
					sdName,
					clusterdeployment.ValidationActionDelete,
				)

				Eventually(func() error {
					return deploymentValidator.Validate(context.Background(), kc)
				}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
				return nil
			})

			// verify the standalone cluster is deployed correctly
			deploymentValidator := clusterdeployment.NewProviderValidator(
				templates.TemplateAzureStandaloneCP,
				sdName,
				clusterdeployment.ValidationActionDeploy,
			)

			templateBy(templates.TemplateAzureStandaloneCP, "waiting for infrastructure provider to deploy successfully")
			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kc)
			}).WithTimeout(90 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			if testingConfig.Hosted == nil {
				continue
			}

			// setup environment variables for deploying the hosted template (subnet name, etc)
			azure.SetAzureEnvironmentVariables(sdName, kc)

			kubeCfgPath, kubecfgDeleteFunc := kc.WriteKubeconfig(context.Background(), sdName)
			kubeconfigDeleteFuncs = append(kubeconfigDeleteFuncs, kubecfgDeleteFunc)

			By("Deploy onto standalone cluster")
			GinkgoT().Setenv("KUBECONFIG", kubeCfgPath)
			cmd := exec.Command("make", "test-apply")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.Unsetenv("KUBECONFIG")).To(Succeed())

			standaloneClient := kc.NewFromCluster(context.Background(), internalutils.DefaultSystemNamespace, sdName)
			// verify the cluster is ready prior to creating credentials
			Eventually(func() error {
				err := verifyControllersUp(standaloneClient)
				if err != nil {
					_, _ = fmt.Fprintf(GinkgoWriter, "Controller validation failed: %v\n", err)
					return err
				}
				return nil
			}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			Eventually(func() error {
				err = clusterdeployment.ValidateClusterTemplates(context.Background(), standaloneClient)
				if err != nil {
					_, _ = fmt.Fprintf(GinkgoWriter, "cluster template validation failed: %v\n", err)
					return err
				}
				return nil
			}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("Create azure credential secret")
			standaloneCi := clusteridentity.New(standaloneClient, clusterdeployment.ProviderAzure)
			standaloneCi.WaitForValidCredential(standaloneClient)

			By("Create azure credential secret")
			clusteridentity.New(standaloneClient, clusterdeployment.ProviderAzure)

			By("Create default storage class for azure-disk CSI driver")
			azure.CreateDefaultStorageClass(standaloneClient)

			hdName := clusterdeployment.GenerateClusterName(fmt.Sprintf("azure-hosted-%d", i))
			hdTemplate := testingConfig.Hosted.Template
			templateBy(templates.TemplateAzureHostedCP, fmt.Sprintf("creating a hosted ClusterDeployment %s with template %s", hdName, hdTemplate))

			hd := clusterdeployment.GetUnstructured(templates.TemplateAzureHostedCP, hdName, hdTemplate)

			templateBy(templates.TemplateAzureHostedCP, "creating a ClusterDeployment")
			hostedDeleteFunc := standaloneClient.CreateClusterDeployment(context.Background(), hd)
			hostedDeleteFuncs = append(hostedDeleteFuncs, func() error {
				By(fmt.Sprintf("Deleting the %s ClusterDeployment", hdName))
				err = hostedDeleteFunc()
				Expect(err).NotTo(HaveOccurred())

				By(fmt.Sprintf("Verifying the %s ClusterDeployment deleted successfully", hdName))
				deploymentValidator = clusterdeployment.NewProviderValidator(
					templates.TemplateAzureHostedCP,
					hdName,
					clusterdeployment.ValidationActionDelete,
				)
				Eventually(func() error {
					return deploymentValidator.Validate(context.Background(), standaloneClient)
				}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
				return nil
			})

			standaloneClusters[sdName] = clusterInfo{
				client:            standaloneClient,
				hostedClusterName: hd.GetName(),
			}
			templateBy(templates.TemplateAzureHostedCP, "Patching AzureCluster to ready")
			clusterdeployment.PatchHostedClusterReady(standaloneClient, clusterdeployment.ProviderAzure, hdName)

			templateBy(templates.TemplateAzureHostedCP, "waiting for infrastructure to deploy successfully")
			deploymentValidator = clusterdeployment.NewProviderValidator(
				templates.TemplateAzureHostedCP,
				hdName,
				clusterdeployment.ValidationActionDeploy,
			)

			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), standaloneClient)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		}
	})
})
