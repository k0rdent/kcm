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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/clusteridentity"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
)

var _ = Describe("Adopted Cluster Templates", Label("provider:cloud", "provider:adopted"), Ordered, func() {
	var (
		kc                *kubeclient.KubeClient
		standaloneClient  *kubeclient.KubeClient
		clusterDeleteFunc func() error
		adoptedDeleteFunc func() error
		kubecfgDeleteFunc func() error
		clusterName       string
	)

	BeforeAll(func() {
		By("providing cluster identity")
		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)
		ci := clusteridentity.New(kc, clusterdeployment.ProviderAWS)
		Expect(os.Setenv(clusterdeployment.EnvVarAWSClusterIdentity, ci.IdentityName)).Should(Succeed())
		ci.WaitForValidCredential(kc)
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
			adoptedDeleteFunc,
			clusterDeleteFunc,
		} {
			if deleteFunc != nil {
				err := deleteFunc()
				Expect(err).NotTo(HaveOccurred())
			}
		}
	})

	It("should work with an Adopted cluster provider", func() {
		// Deploy a standalone cluster and verify it is running/ready. Then, delete the management cluster and
		// recreate it. Next "adopt" the cluster we created and verify the services were deployed. Next we delete
		// the adopted cluster and finally the management cluster (AWS standalone).
		GinkgoT().Setenv(clusterdeployment.EnvVarAWSInstanceType, "t3.xlarge")

		templateBy(clusterdeployment.TemplateAWSStandaloneCP, "creating a ClusterDeployment")
		sd := clusterdeployment.GetUnstructured(clusterdeployment.TemplateAWSStandaloneCP)
		clusterName = sd.GetName()

		clusterDeleteFunc = kc.CreateClusterDeployment(context.Background(), sd)

		templateBy(clusterdeployment.TemplateAWSStandaloneCP, "waiting for infrastructure to deploy successfully")
		deploymentValidator := clusterdeployment.NewProviderValidator(
			clusterdeployment.TemplateAWSStandaloneCP,
			clusterName,
			clusterdeployment.ValidationActionDeploy,
		)

		Eventually(func() error {
			return deploymentValidator.Validate(context.Background(), kc)
		}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		// create the adopted cluster using the AWS standalone cluster
		var kubeCfgFile string
		kubeCfgFile, kubecfgDeleteFunc = kc.WriteKubeconfig(context.Background(), clusterName)
		GinkgoT().Setenv(clusterdeployment.EnvVarAdoptedKubeconfigPath, kubeCfgFile)
		ci := clusteridentity.New(kc, clusterdeployment.ProviderAdopted)
		Expect(os.Setenv(clusterdeployment.EnvVarAdoptedCredential, ci.CredentialName)).Should(Succeed())

		ci.WaitForValidCredential(kc)

		adoptedCluster := clusterdeployment.GetUnstructured(clusterdeployment.TemplateAdoptedCluster)
		adoptedClusterName := adoptedCluster.GetName()
		adoptedDeleteFunc = kc.CreateClusterDeployment(context.Background(), adoptedCluster)

		// validate the adopted cluster
		deploymentValidator = clusterdeployment.NewProviderValidator(
			clusterdeployment.TemplateAdoptedCluster,
			adoptedClusterName,
			clusterdeployment.ValidationActionDeploy,
		)
		Eventually(func() error {
			return deploymentValidator.Validate(context.Background(), kc)
		}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		// delete the adopted cluster
		err := adoptedDeleteFunc()
		Expect(err).NotTo(HaveOccurred())
		adoptedDeleteFunc = nil

		// finally delete the aws standalone cluster and verify it's deleted correctly
		err = clusterDeleteFunc()
		Expect(err).NotTo(HaveOccurred())
		clusterDeleteFunc = nil

		deletionValidator := clusterdeployment.NewProviderValidator(
			clusterdeployment.TemplateAWSStandaloneCP,
			clusterName,
			clusterdeployment.ValidationActionDelete,
		)
		Eventually(func() error {
			return deletionValidator.Validate(context.Background(), kc)
		}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	})
})
