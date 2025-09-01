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
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/vsphere"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/credential"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
	"github.com/K0rdent/kcm/test/e2e/upgrade"
	"github.com/K0rdent/kcm/test/utils"
)

var _ = Context("vSphere Templates", Label("provider:onprem", "provider:vsphere"), Ordered, ContinueOnFailure, func() {
	var (
		kc                     *kubeclient.KubeClient
		standaloneClusterNames []string
		standaloneDeleteFuncs  []func() error
		hostedDeleteFuncs      []func() error
		kubeconfigDeleteFuncs  []func() error
	)

	BeforeAll(func() {
		By("Ensuring that env vars are set correctly")
		vsphere.CheckEnv()

		By("Creating kube client")
		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)

		By("Providing cluster identity and credentials")
		credential.Apply("", "vsphere")
	})

	AfterAll(func() {
		// If we failed collect the support bundle before the cleanup
		if CurrentSpecReport().Failed() && cleanup() {
			By("Collecting the support bundle from the management cluster")
			logs.SupportBundle(kc, "")

			for _, clusterName := range standaloneClusterNames {
				By(fmt.Sprintf("Collecting the support bundle from the %s cluster", clusterName))
				logs.SupportBundle(kc, clusterName)
			}
		}

		// Run the deletion as part of the cleanup and validate it here.
		// VSphere doesn't have any form of cleanup outside of reconciling a
		// cluster deletion so we need to keep the test active while we wait
		// for CAPV to clean up the resources.
		// TODO(#473) Add an exterior cleanup mechanism for VSphere like
		// 'dev-aws-nuke' to clean up resources in the event that the test
		// fails to do so.
		if cleanup() {
			By("Deleting resources")
			deleteFuncs := slices.Concat(hostedDeleteFuncs, standaloneDeleteFuncs, kubeconfigDeleteFuncs)
			for _, deleteFunc := range deleteFuncs {
				if deleteFunc != nil {
					Expect(deleteFunc()).NotTo(HaveOccurred())
				}
			}
		}
	})

	for i, testingConfig := range config.Config[config.TestingProviderVsphere] {
		It(fmt.Sprintf("Verifying Vsphere cluster deployment. Iteration: %d", i), func() {
			defer GinkgoRecover()
			testingConfig.SetDefaults(clusterTemplates, config.TestingProviderVsphere)

			By(testingConfig.Description())

			sdName := clusterdeployment.GenerateClusterName(fmt.Sprintf("vsphere-%d", i))
			sdTemplate := testingConfig.Template

			if i > 0 { // renew the envvar if not a single config
				templateBy(templates.TemplateVSphereStandaloneCP, fmt.Sprintf("Setting a new controlplane endpoint environment variable required for the standalone cluster %s with template %s", sdName, sdTemplate))
				Expect(vsphere.SetControlPlaneEndpointEnv()).NotTo(HaveOccurred())
			}

			// Supported architecture for Vsphere standalone deployment: amd64
			Expect(testingConfig.Architecture).To(Equal(config.ArchitectureAmd64),
				fmt.Sprintf("expected architecture %s", config.ArchitectureAmd64),
			)

			templateBy(templates.TemplateVSphereStandaloneCP, fmt.Sprintf("Creating a ClusterDeployment %s with template %s", sdName, sdTemplate))
			sd := clusterdeployment.Generate(templates.TemplateVSphereStandaloneCP, sdName, sdTemplate)

			sdDeleteFn := clusterdeployment.Create(context.Background(), kc.CrClient, sd)
			standaloneClusterNames = append(standaloneClusterNames, sdName)
			standaloneDeleteFuncs = append(standaloneDeleteFuncs, func() error {
				By(fmt.Sprintf("Deleting the %s ClusterDeployment", sdName))
				Expect(sdDeleteFn()).NotTo(HaveOccurred())

				By(fmt.Sprintf("Verifying the %s ClusterDeployment deleted successfully", sdName))
				deploymentValidator := clusterdeployment.NewProviderValidator(
					templates.TemplateVSphereStandaloneCP,
					sdName,
					clusterdeployment.ValidationActionDelete,
				)

				Eventually(func() error {
					return deploymentValidator.Validate(context.Background(), kc)
				}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
				return nil
			})

			templateBy(templates.TemplateVSphereStandaloneCP, "Waiting for infrastructure providers to deploy successfully")
			deploymentValidator := clusterdeployment.NewProviderValidator(
				templates.TemplateVSphereStandaloneCP,
				sdName,
				clusterdeployment.ValidationActionDeploy,
			)
			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kc)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			if !testingConfig.Upgrade && testingConfig.Hosted == nil {
				return
			}

			standaloneClient := new(kubeclient.KubeClient)
			var hdName string
			if testingConfig.Hosted != nil {
				kubeCfgPath, _, kubecfgDeleteFunc, err := kc.WriteKubeconfig(context.Background(), sdName)
				Expect(err).To(Succeed())
				kubeconfigDeleteFuncs = append(kubeconfigDeleteFuncs, kubecfgDeleteFunc)

				By("Deploy onto standalone cluster")
				GinkgoT().Setenv("KUBECONFIG", kubeCfgPath)
				_, err = utils.Run(exec.Command("make", "test-apply"))
				Expect(err).NotTo(HaveOccurred())
				Expect(os.Unsetenv("KUBECONFIG")).To(Succeed())

				By("Verifying the cluster is ready prior to creating credentials")
				standaloneClient = kc.NewFromCluster(context.Background(), internalutils.DefaultSystemNamespace, sdName)

				Eventually(func() error {
					if err := verifyManagementReadiness(standaloneClient); err != nil {
						_, _ = fmt.Fprintf(GinkgoWriter, "%v\n", err)
						return err
					}

					return nil
				}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				if testingConfig.Hosted.Upgrade {
					By("Installing stable templates for further hosted upgrade testing")
					_, err = utils.Run(exec.Command("make", "stable-templates"))
					Expect(err).NotTo(HaveOccurred())
				}

				By("Ensuring ClusterTemplates in the standalone cluster are valid")
				Eventually(func() error {
					if err := clusterdeployment.ValidateClusterTemplates(context.Background(), standaloneClient); err != nil {
						_, _ = fmt.Fprintf(GinkgoWriter, "cluster template validation failed: %v\n", err)
						return err
					}

					return nil
				}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				// Supported architecture for Vsphere hosted deployment: amd64
				Expect(testingConfig.Hosted.Architecture).To(Equal(config.ArchitectureAmd64),
					fmt.Sprintf("expected architecture %s", config.ArchitectureAmd64),
				)

				By("Providing cluster identity and credentials in the standalone cluster")
				credential.Apply(kubeCfgPath, "vsphere")

				By("Setting the controlplane endpoint environment variable required for the hosted cluster")
				Expect(vsphere.SetHostedControlPlaneEndpointEnv()).NotTo(HaveOccurred())

				hdName = clusterdeployment.GenerateClusterName(fmt.Sprintf("vsphere-hosted-%d", i))
				hdTemplate := testingConfig.Hosted.Template
				templateBy(templates.TemplateVSphereHostedCP, fmt.Sprintf("Creating a hosted ClusterDeployment %s with template %s", hdName, hdTemplate))

				hd := clusterdeployment.Generate(templates.TemplateVSphereHostedCP, hdName, hdTemplate)

				templateBy(templates.TemplateVSphereHostedCP, "Creating a ClusterDeployment")
				hdDeleteFn := clusterdeployment.Create(context.Background(), standaloneClient.CrClient, hd)
				hostedDeleteFuncs = append(hostedDeleteFuncs, func() error {
					By(fmt.Sprintf("Deleting the %s ClusterDeployment", hdName))
					Expect(hdDeleteFn()).NotTo(HaveOccurred())

					By(fmt.Sprintf("Verifying the %s ClusterDeployment deleted successfully", hdName))
					deploymentValidator = clusterdeployment.NewProviderValidator(
						templates.TemplateVSphereHostedCP,
						hdName,
						clusterdeployment.ValidationActionDelete,
					)
					Eventually(func() error {
						return deploymentValidator.Validate(context.Background(), standaloneClient)
					}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
					return nil
				})

				templateBy(templates.TemplateVSphereHostedCP, "Waiting for infrastructure to deploy successfully")
				deploymentValidator = clusterdeployment.NewProviderValidator(
					templates.TemplateVSphereHostedCP,
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
				By(fmt.Sprintf("Updating hosted cluster to the %s template", testingConfig.Hosted.UpgradeTemplate))

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
		})
	}
})
