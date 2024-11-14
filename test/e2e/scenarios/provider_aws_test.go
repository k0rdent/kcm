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

package scenarios

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	internalutils "github.com/Mirantis/hmc/internal/utils"
	"github.com/Mirantis/hmc/test/e2e/config"
	"github.com/Mirantis/hmc/test/e2e/kubeclient"
	"github.com/Mirantis/hmc/test/e2e/managedcluster"
	"github.com/Mirantis/hmc/test/e2e/managedcluster/aws"
	"github.com/Mirantis/hmc/test/e2e/managedcluster/clusteridentity"
	"github.com/Mirantis/hmc/test/e2e/templates"
	"github.com/Mirantis/hmc/test/utils"
)

var _ = Describe("AWS Templates", Label("provider:cloud", "provider:aws"), Ordered, func() {
	ctx := context.Background()

	var (
		kc                   *kubeclient.KubeClient
		standaloneClient     *kubeclient.KubeClient
		standaloneDeleteFunc func() error
		hostedDeleteFunc     func() error
		kubecfgDeleteFunc    func() error
		clusterName          string

		testingConfig config.ProviderTestingConfig
	)

	BeforeAll(func() {
		By("get testing configuration")
		testingConfig = config.Config[config.TestingProviderAWS]

		By("set defaults and validate testing configuration")
		err := testingConfig.Standalone.SetDefaults(clusterTemplates, templates.TemplateAWSStandaloneCP)
		Expect(err).NotTo(HaveOccurred())

		err = testingConfig.Hosted.SetDefaults(clusterTemplates, templates.TemplateAWSHostedCP)
		Expect(err).NotTo(HaveOccurred())

		_, _ = fmt.Fprintf(GinkgoWriter, "Final AWS testing configuration:\n%s\n", testingConfig.String())

		By("providing cluster identity")
		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)
		ci := clusteridentity.New(kc, managedcluster.ProviderAWS, managedcluster.Namespace)
		Expect(os.Setenv(managedcluster.EnvVarAWSClusterIdentity, ci.IdentityName)).Should(Succeed())
	})

	AfterAll(func() {
		// If we failed collect logs from each of the affiliated controllers
		// as well as the output of clusterctl to store as artifacts.
		if CurrentSpecReport().Failed() && !noCleanup() {
			if standaloneClient != nil {
				By("collecting failure logs from hosted controllers")
				collectLogArtifacts(standaloneClient, clusterName, managedcluster.ProviderAWS, managedcluster.ProviderCAPI)
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
		GinkgoT().Setenv(managedcluster.EnvVarAWSInstanceType, "t3.xlarge")

		templateBy(templates.TemplateAWSStandaloneCP, fmt.Sprintf("creating a ManagedCluster with %s template", testingConfig.Standalone.Template))
		sd := managedcluster.GetUnstructured(templates.TemplateAWSStandaloneCP, testingConfig.Standalone.Template)

		clusterName = sd.GetName()

		standaloneDeleteFunc = kc.CreateManagedCluster(context.Background(), sd, managedcluster.Namespace)

		templateBy(templates.TemplateAWSStandaloneCP, "waiting for infrastructure to deploy successfully")
		deploymentValidator := managedcluster.NewProviderValidator(
			templates.TemplateAWSStandaloneCP,
			managedcluster.Namespace,
			clusterName,
			managedcluster.ValidationActionDeploy,
		)

		Eventually(func() error {
			return deploymentValidator.Validate(context.Background(), kc)
		}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		templateBy(templates.TemplateAWSHostedCP, "installing controller and templates on standalone cluster")

		// Download the KUBECONFIG for the standalone cluster and load it
		// so we can call Make targets against this cluster.
		// TODO(#472): Ideally we shouldn't use Make here and should just
		// convert these Make targets into Go code, but this will require a
		// helmclient.
		var kubeCfgPath string
		kubeCfgPath, kubecfgDeleteFunc = kc.WriteKubeconfig(context.Background(), managedcluster.Namespace, clusterName)

		GinkgoT().Setenv("KUBECONFIG", kubeCfgPath)
		cmd := exec.Command("make", "test-apply")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Unsetenv("KUBECONFIG")).To(Succeed())

		templateBy(templates.TemplateAWSHostedCP, "validating that the controller is ready")
		standaloneClient = kc.NewFromCluster(context.Background(), managedcluster.Namespace, clusterName)
		Eventually(func() error {
			err := verifyControllersUp(standaloneClient)
			if err != nil {
				_, _ = fmt.Fprintf(
					GinkgoWriter, "[%s] controller validation failed: %v\n",
					string(templates.TemplateAWSHostedCP), err)
				return err
			}
			return nil
		}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		By(fmt.Sprintf("applying access rules for ClusterTemplates in %s namespace", managedcluster.Namespace))
		templates.ApplyClusterTemplateAccessRules(ctx, standaloneClient.CrClient, managedcluster.Namespace)

		// Ensure AWS credentials are set in the standalone cluster.
		clusteridentity.New(standaloneClient, managedcluster.ProviderAWS, managedcluster.Namespace)

		// Populate the environment variables required for the hosted
		// cluster.
		aws.PopulateHostedTemplateVars(context.Background(), kc, clusterName)

		templateBy(templates.TemplateAWSHostedCP, fmt.Sprintf("creating a ManagedCluster with %s template", testingConfig.Hosted.Template))
		hd := managedcluster.GetUnstructured(templates.TemplateAWSHostedCP, testingConfig.Hosted.Template)
		hdName := hd.GetName()

		// Deploy the hosted cluster on top of the standalone cluster.
		hostedDeleteFunc = standaloneClient.CreateManagedCluster(context.Background(), hd, managedcluster.Namespace)

		templateBy(templates.TemplateAWSHostedCP, "Patching AWSCluster to ready")
		managedcluster.PatchHostedClusterReady(standaloneClient, managedcluster.ProviderAWS, managedcluster.Namespace, hdName)

		// Verify the hosted cluster is running/ready.
		templateBy(templates.TemplateAWSHostedCP, "waiting for infrastructure to deploy successfully")
		deploymentValidator = managedcluster.NewProviderValidator(
			templates.TemplateAWSHostedCP,
			managedcluster.Namespace,
			hdName,
			managedcluster.ValidationActionDeploy,
		)
		Eventually(func() error {
			return deploymentValidator.Validate(context.Background(), standaloneClient)
		}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		if testingConfig.Standalone.Upgrade {
			managedcluster.Upgrade(ctx, kc.CrClient, managedcluster.Namespace, clusterName, testingConfig.Standalone.UpgradeTemplate)
			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kc)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			// Validate hosted deployment
			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), standaloneClient)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		}
		if testingConfig.Hosted.Upgrade {
			managedcluster.Upgrade(ctx, standaloneClient.CrClient, managedcluster.Namespace, hdName, testingConfig.Hosted.UpgradeTemplate)
			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), standaloneClient)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		}

		// Delete the hosted ManagedCluster and verify it is removed.
		templateBy(templates.TemplateAWSHostedCP, "deleting the ManagedCluster")
		err = hostedDeleteFunc()
		Expect(err).NotTo(HaveOccurred())

		deletionValidator := managedcluster.NewProviderValidator(
			templates.TemplateAWSHostedCP,
			managedcluster.Namespace,
			hdName,
			managedcluster.ValidationActionDelete,
		)
		Eventually(func() error {
			return deletionValidator.Validate(context.Background(), standaloneClient)
		}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		// Now delete the standalone ManagedCluster and verify it is
		// removed, it is deleted last since it is the basis for the hosted
		// cluster.
		/*
			FIXME(#339): This is currently disabled as the deletion of the
			standalone cluster is failing due to outstanding issues.
			templateBy(managedcluster.TemplateAWSStandaloneCP, "deleting the ManagedCluster")
			err = standaloneDeleteFunc()
			Expect(err).NotTo(HaveOccurred())

			deletionValidator = managedcluster.NewProviderValidator(
				managedcluster.TemplateAWSStandaloneCP,
				clusterName,
				managedcluster.ValidationActionDelete,
			)
			Eventually(func() error {
				return deletionValidator.Validate(context.Background(), kc)
			}).WithTimeout(10 * time.Minute).WithPolling(10 *
				time.Second).Should(Succeed())
		*/
	})
})
