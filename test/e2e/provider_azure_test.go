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
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/utils"
)

var _ = Context("Azure Templates", Label("provider:cloud", "provider:azure"), Ordered, func() {
	var (
		kc                      *kubeclient.KubeClient
		standaloneClient        *kubeclient.KubeClient
		standaloneDeleteFunc    func() error
		hostedDeleteFunc        func() error
		kubecfgDeleteFunc       func() error
		hostedKubecfgDeleteFunc func() error
		sdName                  string
	)

	BeforeAll(func() {
		By("ensuring Azure credentials are set")
		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)
		ci := clusteridentity.New(kc, clusterdeployment.ProviderAzure)
		ci.WaitForValidCredential(kc)
		Expect(os.Setenv(clusterdeployment.EnvVarAzureClusterIdentity, ci.IdentityName)).Should(Succeed())
	})

	AfterEach(func() {
		// If we failed collect logs from each of the affiliated controllers
		// as well as the output of clusterctl to store as artifacts.
		if CurrentSpecReport().Failed() && cleanup() {
			By("collecting failure logs from controllers")
			if kc != nil {
				collectLogArtifacts(kc, sdName, clusterdeployment.ProviderAzure, clusterdeployment.ProviderCAPI)
			}
			if standaloneClient != nil {
				collectLogArtifacts(standaloneClient, sdName, clusterdeployment.ProviderAzure, clusterdeployment.ProviderCAPI)
			}
		}

		By("deleting resources")
		for _, deleteFunc := range []func() error{
			hostedKubecfgDeleteFunc,
			kubecfgDeleteFunc,
			hostedDeleteFunc,
			standaloneDeleteFunc,
		} {
			if deleteFunc != nil {
				err := deleteFunc()
				Expect(err).NotTo(HaveOccurred())
			}
		}
	})

	It("should work with an Azure provider", func() {
		templateBy(clusterdeployment.TemplateAzureStandaloneCP, "creating a clusterdeployment")
		sd := clusterdeployment.GetUnstructured(clusterdeployment.TemplateAzureStandaloneCP)
		sdName = sd.GetName()

		standaloneDeleteFunc = kc.CreateClusterDeployment(context.Background(), sd)

		// verify the standalone cluster is deployed correctly
		deploymentValidator := clusterdeployment.NewProviderValidator(
			clusterdeployment.TemplateAzureStandaloneCP,
			sdName,
			clusterdeployment.ValidationActionDeploy,
		)

		templateBy(clusterdeployment.TemplateAzureStandaloneCP, "waiting for infrastructure provider to deploy successfully")
		Eventually(func() error {
			return deploymentValidator.Validate(context.Background(), kc)
		}).WithTimeout(90 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		// setup environment variables for deploying the hosted template (subnet name, etc)
		azure.SetAzureEnvironmentVariables(sdName, kc)

		hd := clusterdeployment.GetUnstructured(clusterdeployment.TemplateAzureHostedCP)
		hdName := hd.GetName()

		var kubeCfgPath string
		kubeCfgPath, kubecfgDeleteFunc = kc.WriteKubeconfig(context.Background(), sdName)

		By("Deploy onto standalone cluster")
		GinkgoT().Setenv("KUBECONFIG", kubeCfgPath)
		cmd := exec.Command("make", "test-apply")
		_, err := utils.Run(cmd)
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

		By("Create default storage class for azure-disk CSI driver")
		azure.CreateDefaultStorageClass(standaloneClient)

		templateBy(clusterdeployment.TemplateAzureHostedCP, "creating a clusterdeployment")
		hostedDeleteFunc = standaloneClient.CreateClusterDeployment(context.Background(), hd)

		templateBy(clusterdeployment.TemplateAzureHostedCP, "Patching AzureCluster to ready")
		clusterdeployment.PatchHostedClusterReady(standaloneClient, clusterdeployment.ProviderAzure, hdName)

		templateBy(clusterdeployment.TemplateAzureHostedCP, "waiting for infrastructure to deploy successfully")
		deploymentValidator = clusterdeployment.NewProviderValidator(
			clusterdeployment.TemplateAzureHostedCP,
			hdName,
			clusterdeployment.ValidationActionDeploy,
		)

		Eventually(func() error {
			return deploymentValidator.Validate(context.Background(), standaloneClient)
		}).WithTimeout(90 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		By("verify the deployment deletes successfully")
		err = hostedDeleteFunc()
		Expect(err).NotTo(HaveOccurred())
		hostedDeleteFunc = nil
		deploymentValidator = clusterdeployment.NewProviderValidator(
			clusterdeployment.TemplateAzureHostedCP,
			hdName,
			clusterdeployment.ValidationActionDelete,
		)

		Eventually(func() error {
			return deploymentValidator.Validate(context.Background(), standaloneClient)
		}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		err = standaloneDeleteFunc()
		Expect(err).NotTo(HaveOccurred())
		standaloneDeleteFunc = nil

		deploymentValidator = clusterdeployment.NewProviderValidator(
			clusterdeployment.TemplateAzureStandaloneCP,
			hdName,
			clusterdeployment.ValidationActionDelete,
		)

		Eventually(func() error {
			return deploymentValidator.Validate(context.Background(), kc)
		}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	})
})
