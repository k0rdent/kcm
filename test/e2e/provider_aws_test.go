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
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/aws"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/clusteridentity"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/utils"
)

var _ = Describe("AWS Templates", Label("provider:cloud", "provider:aws"), Ordered, func() {
	var (
		kc                   *kubeclient.KubeClient
		standaloneClient     *kubeclient.KubeClient
		standaloneDeleteFunc func() error
		hostedDeleteFunc     func() error
		kubecfgDeleteFunc    func() error
		clusterName          string
	)

	BeforeAll(func() {
		By("providing cluster identity")
		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)
		ci := clusteridentity.New(kc, clusterdeployment.ProviderAWS)
		ci.WaitForValidCredential(kc)
		Expect(os.Setenv(clusterdeployment.EnvVarAWSClusterIdentity, ci.IdentityName)).Should(Succeed())
	})

	AfterAll(func() {
		// If we failed collect logs from each of the affiliated controllers
		// as well as the output of clusterctl to store as artifacts.
		if CurrentSpecReport().Failed() && cleanup() {
			if standaloneClient != nil {
				By("collecting failure logs from hosted controllers")
				collectLogArtifacts(standaloneClient, clusterName, clusterdeployment.ProviderAWS, clusterdeployment.ProviderCAPI)
			}
		}

		By("deleting resources")
		for _, deleteFunc := range []func() error{
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

	It("should work with an AWS provider", func() {
		// Deploy a standalone cluster and verify it is running/ready.
		// Deploy standalone with an xlarge instance since it will also be
		// hosting the hosted cluster.
		GinkgoT().Setenv(clusterdeployment.EnvVarAWSInstanceType, "t3.xlarge")

		templateBy(clusterdeployment.TemplateAWSStandaloneCP, "creating a ClusterDeployment")
		sd := clusterdeployment.GetUnstructured(clusterdeployment.TemplateAWSStandaloneCP)
		clusterName = sd.GetName()

		standaloneDeleteFunc = kc.CreateClusterDeployment(context.Background(), sd)

		templateBy(clusterdeployment.TemplateAWSStandaloneCP, "waiting for infrastructure to deploy successfully")
		deploymentValidator := clusterdeployment.NewProviderValidator(
			clusterdeployment.TemplateAWSStandaloneCP,
			clusterName,
			clusterdeployment.ValidationActionDeploy,
		)

		Eventually(func() error {
			return deploymentValidator.Validate(context.Background(), kc)
		}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		templateBy(clusterdeployment.TemplateAWSHostedCP, "installing controller and templates on standalone cluster")

		// Download the KUBECONFIG for the standalone cluster and load it
		// so we can call Make targets against this cluster.
		// TODO(#472): Ideally we shouldn't use Make here and should just
		// convert these Make targets into Go code, but this will require a
		// helmclient.
		var kubeCfgPath string
		kubeCfgPath, kubecfgDeleteFunc = kc.WriteKubeconfig(context.Background(), clusterName)

		GinkgoT().Setenv("KUBECONFIG", kubeCfgPath)
		cmd := exec.Command("make", "test-apply")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Unsetenv("KUBECONFIG")).To(Succeed())

		templateBy(clusterdeployment.TemplateAWSHostedCP, "validating that the controller is ready")
		standaloneClient = kc.NewFromCluster(context.Background(), internalutils.DefaultSystemNamespace, clusterName)
		Eventually(func() error {
			err := verifyControllersUp(standaloneClient)
			if err != nil {
				_, _ = fmt.Fprintf(
					GinkgoWriter, "[%s] controller validation failed: %v\n",
					string(clusterdeployment.TemplateAWSHostedCP), err)
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

		// Ensure AWS credentials are set in the standalone cluster.
		standaloneCi := clusteridentity.New(standaloneClient, clusterdeployment.ProviderAWS)
		standaloneCi.WaitForValidCredential(standaloneClient)

		// Populate the environment variables required for the hosted
		// cluster.
		aws.PopulateHostedTemplateVars(context.Background(), kc, clusterName)

		templateBy(clusterdeployment.TemplateAWSHostedCP, "creating a clusterdeployment")
		hd := clusterdeployment.GetUnstructured(clusterdeployment.TemplateAWSHostedCP)
		hdName := hd.GetName()

		// Deploy the hosted cluster on top of the standalone cluster.
		hostedDeleteFunc = standaloneClient.CreateClusterDeployment(context.Background(), hd)

		templateBy(clusterdeployment.TemplateAWSHostedCP, "Patching AWSCluster to ready")
		clusterdeployment.PatchHostedClusterReady(standaloneClient, clusterdeployment.ProviderAWS, hdName)

		// Verify the hosted cluster is running/ready.
		templateBy(clusterdeployment.TemplateAWSHostedCP, "waiting for infrastructure to deploy successfully")
		deploymentValidator = clusterdeployment.NewProviderValidator(
			clusterdeployment.TemplateAWSHostedCP,
			hdName,
			clusterdeployment.ValidationActionDeploy,
		)
		Eventually(func() error {
			return deploymentValidator.Validate(context.Background(), standaloneClient)
		}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		// Delete the hosted clusterdeployment and verify it is removed.
		templateBy(clusterdeployment.TemplateAWSHostedCP, "deleting the clusterdeployment")
		err = hostedDeleteFunc()
		Expect(err).NotTo(HaveOccurred())
		hostedDeleteFunc = nil

		deletionValidator := clusterdeployment.NewProviderValidator(
			clusterdeployment.TemplateAWSHostedCP,
			hdName,
			clusterdeployment.ValidationActionDelete,
		)
		Eventually(func() error {
			return deletionValidator.Validate(context.Background(), standaloneClient)
		}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		// Now delete the standalone clusterdeployment and verify it is
		// removed, it is deleted last since it is the basis for the hosted
		// cluster.

		templateBy(clusterdeployment.TemplateAWSStandaloneCP, "deleting the clusterdeployment")
		err = standaloneDeleteFunc()
		Expect(err).NotTo(HaveOccurred())

		standaloneDeleteFunc = nil
		deletionValidator = clusterdeployment.NewProviderValidator(
			clusterdeployment.TemplateAWSStandaloneCP,
			clusterName,
			clusterdeployment.ValidationActionDelete,
		)
		Eventually(func() error {
			return deletionValidator.Validate(context.Background(), kc)
		}).WithTimeout(10 * time.Minute).WithPolling(10 *
			time.Second).Should(Succeed())
	})

	It("should work with an AWS provider (EKS)", func() {
		templateBy(clusterdeployment.TemplateAWSEKS, "creating a ClusterDeployment")
		cd := clusterdeployment.GetUnstructured(clusterdeployment.TemplateAWSEKS)
		clusterName = cd.GetName() + "-eks"

		eksDeleteFunc := kc.CreateClusterDeployment(context.Background(), cd)

		templateBy(clusterdeployment.TemplateAWSEKS, "waiting for infrastructure to deploy successfully")
		deploymentValidator := clusterdeployment.NewProviderValidator(
			clusterdeployment.TemplateAWSEKS,
			clusterName,
			clusterdeployment.ValidationActionDeploy,
		)

		Eventually(func() error {
			return deploymentValidator.Validate(context.Background(), kc)
		}, 30*time.Minute, 10*time.Second).Should(Succeed())

		if cleanup() {
			templateBy(clusterdeployment.TemplateAWSEKS, "deleting the ClusterDeployment")
			Expect(eksDeleteFunc()).NotTo(HaveOccurred())

			deletionValidator := clusterdeployment.NewProviderValidator(
				clusterdeployment.TemplateAWSEKS,
				clusterName,
				clusterdeployment.ValidationActionDelete,
			)
			Eventually(func() error {
				return deletionValidator.Validate(context.Background(), kc)
			}, 20*time.Minute, 10*time.Second).Should(Succeed())
		}
	})
})
