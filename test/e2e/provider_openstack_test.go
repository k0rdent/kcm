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

	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	ostk "github.com/K0rdent/kcm/test/e2e/clusterdeployment/openstack"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/credential"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
	"github.com/K0rdent/kcm/test/e2e/upgrade"
	executil "github.com/K0rdent/kcm/test/util/exec"
)

var _ = Context("OpenStack Templates", Label("provider:onprem", "provider:openstack"), Ordered, ContinueOnFailure, func() {
	var (
		kc                    *kubeclient.KubeClient
		standaloneClusters    []string
		hostedDeleteFuncs     []func() error
		standaloneDeleteFuncs []func() error
		kubeconfigDeleteFuncs []func() error
	)

	BeforeAll(func() {
		ostk.CheckEnv()

		kc = kubeclient.NewFromLocal(kubeutil.DefaultSystemNamespace)

		// Provide credentials to the management cluster
		credential.Apply("", "openstack")
	})

	AfterAll(func() {
		if CurrentSpecReport().Failed() && cleanup() {
			By("collecting the support bundle from the management cluster")
			logs.SupportBundle(kc, "")

			for _, clusterName := range standaloneClusters {
				By(fmt.Sprintf("collecting the support bundle from the %s cluster", clusterName))
				logs.SupportBundle(kc, clusterName)
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

	for i, testingConfig := range config.Config[config.TestingProviderOpenstack] {
		It(fmt.Sprintf("Verifying OpenStack cluster deployment. Iteration: %d", i), func() {
			defer GinkgoRecover()
			testingConfig.SetDefaults(clusterTemplates, config.TestingProviderOpenstack)

			By(testingConfig.Description())

			sdName := clusterdeployment.GenerateClusterName(fmt.Sprintf("openstack-%d", i))
			sdTemplate := testingConfig.Template
			sdTemplateType := templates.GetType(sdTemplate)

			// Ensure required OpenStack template envs
			ostk.PopulateEnvVars(testingConfig.Architecture)

			templateBy(sdTemplateType, fmt.Sprintf("creating a ClusterDeployment %s with template %s", sdName, sdTemplate))
			sd := clusterdeployment.Generate(sdTemplateType, sdName, sdTemplate)

			standaloneDeleteFunc := clusterdeployment.Create(context.Background(), kc.CrClient, sd)
			standaloneClusters = append(standaloneClusters, sdName)
			standaloneDeleteFuncs = append(standaloneDeleteFuncs, func() error {
				By(fmt.Sprintf("Deleting the %s ClusterDeployment", sdName))
				err := standaloneDeleteFunc()
				Expect(err).NotTo(HaveOccurred())

				By(fmt.Sprintf("Verifying the %s ClusterDeployment deleted successfully", sdName))
				deletionValidator := clusterdeployment.NewProviderValidator(
					sdTemplateType,
					sdName,
					clusterdeployment.ValidationActionDelete,
				)
				Eventually(func() error {
					return deletionValidator.Validate(context.Background(), kc)
				}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
				return nil
			})

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
				return
			}

			standaloneClient := new(kubeclient.KubeClient)
			var hdName string
			if testingConfig.Hosted != nil {
				// Write kubeconfig for the standalone cluster and install controller/templates
				kubeCfgPath, _, kubecfgDeleteFunc, err := kc.WriteKubeconfig(context.Background(), sdName)
				Expect(err).To(Succeed())
				kubeconfigDeleteFuncs = append(kubeconfigDeleteFuncs, kubecfgDeleteFunc)

				By("Deploy onto standalone cluster")
				GinkgoT().Setenv("KUBECONFIG", kubeCfgPath)
				cmd := exec.Command("make", "test-apply")
				_, err = executil.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
				Expect(os.Unsetenv("KUBECONFIG")).To(Succeed())

				standaloneClient = kc.NewFromCluster(context.Background(), kubeutil.DefaultSystemNamespace, sdName)

				Eventually(func() error {
					err := verifyManagementReadiness(standaloneClient)
					if err != nil {
						_, _ = fmt.Fprintf(GinkgoWriter, "%v\n", err)
						return err
					}
					return nil
				}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				if testingConfig.Hosted.Upgrade {
					By("installing stable templates for further hosted upgrade testing")
					_, err = executil.Run(exec.Command("make", "stable-templates"))
					Expect(err).NotTo(HaveOccurred())
				}

				Eventually(func() error {
					err = clusterdeployment.ValidateClusterTemplates(context.Background(), standaloneClient)
					if err != nil {
						_, _ = fmt.Fprintf(GinkgoWriter, "cluster template validation failed: %v\n", err)
						return err
					}
					return nil
				}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				// Provide credentials to the standalone cluster
				credential.Apply(kubeCfgPath, "openstack")

				// Auto-populate hosted template vars from the standalone cluster
				ostk.PopulateHostedTemplateVars(context.Background(), standaloneClient, sdName)

				hdName = clusterdeployment.GenerateClusterName(fmt.Sprintf("openstack-hosted-%d", i))
				hdTemplate := testingConfig.Hosted.Template
				templateBy(templates.TemplateOpenStackHostedCP, fmt.Sprintf("creating a hosted ClusterDeployment %s with template %s", hdName, hdTemplate))

				hd := clusterdeployment.Generate(templates.TemplateOpenStackHostedCP, hdName, hdTemplate)

				hostedDeleteFunc := clusterdeployment.Create(context.Background(), standaloneClient.CrClient, hd)
				hostedDeleteFuncs = append(hostedDeleteFuncs, func() error {
					By(fmt.Sprintf("Deleting the %s ClusterDeployment", hdName))
					err = hostedDeleteFunc()
					Expect(err).NotTo(HaveOccurred())

					By(fmt.Sprintf("Verifying the %s ClusterDeployment deleted successfully", hdName))
					deletionValidator := clusterdeployment.NewProviderValidator(
						templates.TemplateOpenStackHostedCP,
						hdName,
						clusterdeployment.ValidationActionDelete,
					)
					Eventually(func() error {
						return deletionValidator.Validate(context.Background(), standaloneClient)
					}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
					return nil
				})

				templateBy(templates.TemplateOpenStackHostedCP, "Patching OpenStackCluster to ready")
				clusterdeployment.PatchHostedClusterReady(standaloneClient, clusterdeployment.ProviderOpenStack, hdName)

				templateBy(templates.TemplateOpenStackHostedCP, "waiting for infrastructure to deploy successfully")
				deploymentValidator = clusterdeployment.NewProviderValidator(
					templates.TemplateOpenStackHostedCP,
					hdName,
					clusterdeployment.ValidationActionDeploy,
				)
				Eventually(func() error {
					return deploymentValidator.Validate(context.Background(), standaloneClient)
				}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
			}

			if testingConfig.Upgrade {
				clusterUpgrade := upgrade.NewClusterUpgrade(
					kc.CrClient,
					standaloneClient.CrClient,
					kubeutil.DefaultSystemNamespace,
					sdName,
					testingConfig.UpgradeTemplate,
					upgrade.NewDefaultClusterValidator(),
				)
				clusterUpgrade.Run(context.Background())

				Eventually(func() error {
					return deploymentValidator.Validate(context.Background(), kc)
				}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				if testingConfig.Hosted != nil {
					Eventually(func() error {
						return deploymentValidator.Validate(context.Background(), standaloneClient)
					}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
				}
			}
			if testingConfig.Hosted != nil && testingConfig.Hosted.Upgrade {
				By(fmt.Sprintf("updating hosted cluster to the %s template", testingConfig.Hosted.UpgradeTemplate))

				hostedClient := standaloneClient.NewFromCluster(context.Background(), kubeutil.DefaultSystemNamespace, hdName)
				clusterUpgrade := upgrade.NewClusterUpgrade(
					standaloneClient.CrClient,
					hostedClient.CrClient,
					kubeutil.DefaultSystemNamespace,
					hdName,
					testingConfig.Hosted.UpgradeTemplate,
					upgrade.NewDefaultClusterValidator(),
				)
				clusterUpgrade.Run(context.Background())

				Eventually(func() error {
					return deploymentValidator.Validate(context.Background(), standaloneClient)
				}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
			}
		})
	}
})
