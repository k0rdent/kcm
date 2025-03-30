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
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
	"github.com/K0rdent/kcm/test/e2e/upgrade"
	"github.com/K0rdent/kcm/test/utils"
)

var _ = Context("GCP Templates", Label("provider:cloud", "provider:gcp"), Ordered, func() {
	var (
		kc                    *kubeclient.KubeClient
		standaloneClusters    []string
		hostedDeleteFuncs     []func() error
		standaloneDeleteFuncs []func() error
		kubeconfigDeleteFuncs []func() error

		providerConfigs []config.ProviderTestingConfig
	)

	BeforeAll(func() {
		By("get testing configuration")
		providerConfigs = config.Config[config.TestingProviderGCP]

		if len(providerConfigs) == 0 {
			Skip("GCP ClusterDeployment testing is skipped")
		}

		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)

		By("ensuring GCP credentials are set")
		cmd := exec.Command("make", "dev-gcp-creds")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		// If we failed collect the support bundle before the cleanup
		if CurrentSpecReport().Failed() && cleanup() {
			By("collecting the support bundle from the management cluster")
			logs.SupportBundle("")

			for _, clusterName := range standaloneClusters {
				By(fmt.Sprintf("collecting the support bundle from the %s cluster", clusterName))
				logs.SupportBundle(clusterName)
			}
		}

		if cleanup() {
			By("deleting resources")
			deleteFuncs := append(hostedDeleteFuncs, append(standaloneDeleteFuncs, kubeconfigDeleteFuncs...)...)
			for _, deleteFunc := range deleteFuncs {
				if deleteFunc != nil {
					err := deleteFunc()
					Expect(err).NotTo(HaveOccurred())
				}
			}
		}
	})

	It("should work with GCP provider", func() {
		for i, testingConfig := range providerConfigs {
			_, _ = fmt.Fprintf(GinkgoWriter, "Testing configuration:\n%s\n", testingConfig.String())

			sdName := clusterdeployment.GenerateClusterName(fmt.Sprintf("gcp-%d", i))
			sdTemplate := testingConfig.Template
			sdTemplateType := templates.GetType(sdTemplate)

			// Supported template types for GCP standalone deployment: gcp-gke, gcp-standalone-cp
			Expect(sdTemplateType).To(SatisfyAny(
				Equal(templates.TemplateGCPStandaloneCP),
				Equal(templates.TemplateGCPGKE)),
				fmt.Sprintf("template type should be either %s or %s", templates.TemplateGCPGKE, templates.TemplateGCPStandaloneCP))

			templateBy(sdTemplateType, fmt.Sprintf("creating a ClusterDeployment %s with template %s", sdName, sdTemplate))

			sd := clusterdeployment.GetUnstructured(sdTemplateType, sdName, sdTemplate)

			standaloneDeleteFunc := kc.CreateClusterDeployment(context.Background(), sd)
			standaloneClusters = append(standaloneClusters, sdName)
			standaloneDeleteFuncs = append(standaloneDeleteFuncs, func() error {
				By(fmt.Sprintf("Deleting the %s ClusterDeployment", sdName))
				err := standaloneDeleteFunc()
				Expect(err).NotTo(HaveOccurred())

				By(fmt.Sprintf("Verifying the %s ClusterDeployment deleted successfully", sdName))
				deploymentValidator := clusterdeployment.NewProviderValidator(
					templates.TemplateGCPStandaloneCP,
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
				sdTemplateType,
				sdName,
				clusterdeployment.ValidationActionDeploy,
			)

			templateBy(sdTemplateType, "waiting for infrastructure provider to deploy successfully")
			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kc)
			}).WithTimeout(90 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			if !testingConfig.Upgrade && testingConfig.Hosted == nil {
				continue
			}

			standaloneClient := new(kubeclient.KubeClient)
			var hdName string
			if testingConfig.Hosted != nil {
				kubeCfgPath, kubecfgDeleteFunc := kc.WriteKubeconfig(context.Background(), sdName)
				kubeconfigDeleteFuncs = append(kubeconfigDeleteFuncs, kubecfgDeleteFunc)

				By("Deploy onto standalone cluster")
				GinkgoT().Setenv("KUBECONFIG", kubeCfgPath)
				cmd := exec.Command("make", "test-apply")
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())

				By("Ensuring GCP credentials are set")
				cmd = exec.Command("make", "dev-gcp-creds")
				_, err = utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())

				Expect(os.Unsetenv("KUBECONFIG")).To(Succeed())

				standaloneClient = kc.NewFromCluster(context.Background(), internalutils.DefaultSystemNamespace, sdName)
				// verify the cluster is ready prior to creating credentials
				Eventually(func() error {
					err := verifyControllersUp(standaloneClient)
					if err != nil {
						_, _ = fmt.Fprintf(GinkgoWriter, "Controller validation failed: %v\n", err)
						return err
					}
					return nil
				}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				if testingConfig.Hosted.Upgrade {
					By("installing stable templates for further hosted upgrade testing")
					_, err = utils.Run(exec.Command("make", "stable-templates"))
					Expect(err).NotTo(HaveOccurred())
				}

				// Ensure Cluster Templates in the standalone cluster are valid
				Eventually(func() error {
					err = clusterdeployment.ValidateClusterTemplates(context.Background(), standaloneClient)
					if err != nil {
						_, _ = fmt.Fprintf(GinkgoWriter, "cluster template validation failed: %v\n", err)
						return err
					}
					return nil
				}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				hdName = clusterdeployment.GenerateClusterName(fmt.Sprintf("gcp-hosted-%d", i))
				hdTemplate := testingConfig.Hosted.Template
				templateBy(templates.TemplateGCPHostedCP, fmt.Sprintf("creating a hosted ClusterDeployment %s with template %s", hdName, hdTemplate))

				hd := clusterdeployment.GetUnstructured(templates.TemplateGCPHostedCP, hdName, hdTemplate)

				templateBy(templates.TemplateGCPHostedCP, "creating a ClusterDeployment")
				hostedDeleteFunc := standaloneClient.CreateClusterDeployment(context.Background(), hd)
				hostedDeleteFuncs = append(hostedDeleteFuncs, func() error {
					By(fmt.Sprintf("Deleting the %s ClusterDeployment", hdName))
					err = hostedDeleteFunc()
					Expect(err).NotTo(HaveOccurred())

					By(fmt.Sprintf("Verifying the %s ClusterDeployment deleted successfully", hdName))
					deploymentValidator = clusterdeployment.NewProviderValidator(
						templates.TemplateGCPHostedCP,
						hdName,
						clusterdeployment.ValidationActionDelete,
					)
					Eventually(func() error {
						return deploymentValidator.Validate(context.Background(), standaloneClient)
					}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
					return nil
				})

				templateBy(templates.TemplateGCPHostedCP, "waiting for infrastructure to deploy successfully")
				deploymentValidator = clusterdeployment.NewProviderValidator(
					templates.TemplateGCPHostedCP,
					hdName,
					clusterdeployment.ValidationActionDeploy,
				)

				Eventually(func() error {
					return deploymentValidator.Validate(context.Background(), standaloneClient)
				}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
			}

			if testingConfig.Upgrade {
				clusterUpgrade := upgrade.NewClusterUpgrade(
					kc.CrClient,
					standaloneClient.CrClient,
					internalutils.DefaultSystemNamespace,
					sdName,
					testingConfig.UpgradeTemplate,
					upgrade.NewDefaultClusterValidator(),
				)
				clusterUpgrade.Run(context.Background())

				Eventually(func() error {
					return deploymentValidator.Validate(context.Background(), kc)
				}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				if testingConfig.Hosted != nil {
					// Validate hosted deployment after the standalone upgrade
					Eventually(func() error {
						return deploymentValidator.Validate(context.Background(), standaloneClient)
					}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
				}
			}
			if testingConfig.Hosted != nil && testingConfig.Hosted.Upgrade {
				By(fmt.Sprintf("updating hosted cluster to the %s template", testingConfig.Hosted.UpgradeTemplate))

				hostedClient := standaloneClient.NewFromCluster(context.Background(), internalutils.DefaultSystemNamespace, hdName)
				clusterUpgrade := upgrade.NewClusterUpgrade(
					standaloneClient.CrClient,
					hostedClient.CrClient,
					internalutils.DefaultSystemNamespace,
					hdName,
					testingConfig.Hosted.UpgradeTemplate,
					upgrade.NewDefaultClusterValidator(),
				)
				clusterUpgrade.Run(context.Background())

				Eventually(func() error {
					return deploymentValidator.Validate(context.Background(), standaloneClient)
				}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
			}
		}
	})
})
