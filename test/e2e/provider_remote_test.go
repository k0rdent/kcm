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
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/remote"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/credential"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
	"github.com/K0rdent/kcm/test/e2e/upgrade"
	"github.com/K0rdent/kcm/test/utils"
)

var _ = Describe("Remote Cluster Templates", Label("provider:cloud", "provider:remote"), Ordered, func() {
	var (
		kc                    *kubeclient.KubeClient
		clusterDeleteFuncs    []func() error
		kubeconfigDeleteFuncs []func() error
		publicKey             string

		providerConfigs []config.ProviderTestingConfig
	)

	BeforeAll(func() {
		By("get testing configuration")
		providerConfigs = config.Config[config.TestingProviderRemote]

		if len(providerConfigs) == 0 {
			Skip("Remote ClusterDeployment testing is skipped")
		}

		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)

		By("Generating SSH key for the remote cluster")
		var privateKey string
		var err error
		privateKey, publicKey, err = remote.GenerateSSHKeyPair()
		Expect(err).NotTo(HaveOccurred())

		privateKeyBase64 := base64.StdEncoding.EncodeToString([]byte(privateKey))
		Expect(os.Setenv(clusterdeployment.EnvVarPrivateSSHKeyB64, privateKeyBase64)).Should(Succeed())

		By("Providing cluster identity")
		credential.Apply("", "remote")

		By("Installing KubeVirt and CDI")
		cmd := exec.Command("make", "kubevirt")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		// If we failed collect the support bundle before the cleanup
		if CurrentSpecReport().Failed() && cleanup() {
			By("collecting the support bundle from the management cluster")
			logs.SupportBundle("")
		}

		if cleanup() {
			By("deleting resources")
			deleteFuncs := append(clusterDeleteFuncs, kubeconfigDeleteFuncs...)
			for _, deleteFunc := range deleteFuncs {
				if deleteFunc != nil {
					err := deleteFunc()
					Expect(err).NotTo(HaveOccurred())
				}
			}
		}
	})

	It("should work with Remote cluster provider", func() {
		for i, testingConfig := range providerConfigs {
			_, _ = fmt.Fprintf(GinkgoWriter, "Testing configuration:\n%s\n", testingConfig.String())

			clusterName := clusterdeployment.GenerateClusterName(fmt.Sprintf("remote-%d", i))
			clusterTemplate := testingConfig.Template

			By("Preparing Virtual Machines using KubeVirt")
			ports, err := remote.PrepareVMs(context.Background(), kc.CrClient, internalutils.DefaultSystemNamespace, clusterName, publicKey, 2)
			Expect(err).NotTo(HaveOccurred())

			address, err := remote.GetAddress(context.Background(), kc.CrClient)
			Expect(err).NotTo(HaveOccurred())

			Expect(os.Setenv("MACHINE_0_ADDRESS", address)).Should(Succeed())
			Expect(os.Setenv("MACHINE_1_ADDRESS", address)).Should(Succeed())

			Expect(os.Setenv("MACHINE_0_PORT", strconv.Itoa(ports[0]))).Should(Succeed())
			Expect(os.Setenv("MACHINE_1_PORT", strconv.Itoa(ports[1]))).Should(Succeed())

			templateBy(templates.TemplateRemoteCluster, fmt.Sprintf("creating a ClusterDeployment %s with template %s", clusterName, clusterTemplate))
			cd := clusterdeployment.Generate(templates.TemplateRemoteCluster, clusterName, clusterTemplate)

			clusterDeleteFunc := clusterdeployment.Create(context.Background(), kc.CrClient, cd)
			clusterDeleteFuncs = append(clusterDeleteFuncs, func() error {
				By(fmt.Sprintf("Deleting the %s ClusterDeployment", clusterName))
				err := clusterDeleteFunc()
				Expect(err).NotTo(HaveOccurred())

				By(fmt.Sprintf("Verifying the %s ClusterDeployment deleted successfully", clusterName))
				deletionValidator := clusterdeployment.NewProviderValidator(
					templates.TemplateRemoteCluster,
					clusterName,
					clusterdeployment.ValidationActionDelete,
				)
				Eventually(func() error {
					return deletionValidator.Validate(context.Background(), kc)
				}).WithTimeout(20 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
				return nil
			})

			templateBy(templates.TemplateRemoteCluster, "waiting for infrastructure to deploy successfully")
			deploymentValidator := clusterdeployment.NewProviderValidator(
				templates.TemplateRemoteCluster,
				clusterName,
				clusterdeployment.ValidationActionDeploy,
			)

			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kc)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			if testingConfig.Upgrade {
				standaloneClient := kc.NewFromCluster(context.Background(), internalutils.DefaultSystemNamespace, clusterName)
				clusterUpgrade := upgrade.NewClusterUpgrade(
					kc.CrClient,
					standaloneClient.CrClient,
					internalutils.DefaultSystemNamespace,
					clusterName,
					testingConfig.UpgradeTemplate,
					upgrade.NewDefaultClusterValidator(),
				)
				clusterUpgrade.Run(context.Background())

				Eventually(func() error {
					return deploymentValidator.Validate(context.Background(), kc)
				}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
			}
		}
	})
})
