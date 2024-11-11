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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/clusteridentity"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/vsphere"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
)

var _ = Context("vSphere Templates", Label("provider:onprem", "provider:vsphere"), Ordered, func() {
	var (
		kc                     *kubeclient.KubeClient
		standaloneDeleteFuncs  map[string]func() error
		standaloneClusterNames []string
		err                    error

		providerConfigs []config.ProviderTestingConfig
	)

	BeforeAll(func() {
		By("get testing configuration")
		providerConfigs = config.Config[config.TestingProviderVsphere]

		if len(providerConfigs) == 0 {
			Skip("Vsphere ClusterDeployment testing is skipped")
		}

		By("ensuring that env vars are set correctly")
		vsphere.CheckEnv()
		By("creating kube client")
		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)
		By("providing cluster identity")
		ci := clusteridentity.New(kc, clusterdeployment.ProviderVSphere)
		ci.WaitForValidCredential(kc)
		By("setting VSPHERE_CLUSTER_IDENTITY env variable")
		Expect(os.Setenv(clusterdeployment.EnvVarVSphereClusterIdentity, ci.IdentityName)).Should(Succeed())
	})

	AfterAll(func() {
		// If we failed collect logs from each of the affiliated controllers
		// as well as the output of clusterctl to store as artifacts.
		if CurrentSpecReport().Failed() {
			By("collecting failure logs from controllers")
			if kc != nil {
				logs.Collector{
					Client:        kc,
					ProviderTypes: []clusterdeployment.ProviderType{clusterdeployment.ProviderVSphere, clusterdeployment.ProviderCAPI},
					ClusterNames:  standaloneClusterNames,
				}.CollectAll()
			}
		}

		// Run the deletion as part of the cleanup and validate it here.
		// VSphere doesn't have any form of cleanup outside of reconciling a
		// cluster deletion so we need to keep the test active while we wait
		// for CAPV to clean up the resources.
		// TODO(#473) Add an exterior cleanup mechanism for VSphere like
		// 'dev-aws-nuke' to clean up resources in the event that the test
		// fails to do so.
		if !cleanup() {
			for clusterName, deleteFunc := range standaloneDeleteFuncs {
				if deleteFunc != nil {
					deletionValidator := clusterdeployment.NewProviderValidator(
						templates.TemplateVSphereStandaloneCP,
						clusterName,
						clusterdeployment.ValidationActionDelete,
					)

					err = deleteFunc()
					Expect(err).NotTo(HaveOccurred())
					Eventually(func() error {
						return deletionValidator.Validate(context.Background(), kc)
					}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
				}
			}
		}
	})

	It("should work with Vsphere provider", func() {
		for i, testingConfig := range providerConfigs {
			sdName := clusterdeployment.GenerateClusterName(fmt.Sprintf("vsphere-%d", i))
			sdTemplate := testingConfig.Template
			templateBy(templates.TemplateVSphereStandaloneCP, fmt.Sprintf("creating a ClusterDeployment %s with template %s", sdName, sdTemplate))

			d := clusterdeployment.GetUnstructured(templates.TemplateVSphereStandaloneCP, sdName, sdTemplate)
			clusterName := d.GetName()

			deleteFunc := kc.CreateClusterDeployment(context.Background(), d)
			standaloneDeleteFuncs[clusterName] = deleteFunc
			standaloneClusterNames = append(standaloneClusterNames, clusterName)

			By("waiting for infrastructure providers to deploy successfully")
			deploymentValidator := clusterdeployment.NewProviderValidator(
				templates.TemplateVSphereStandaloneCP,
				clusterName,
				clusterdeployment.ValidationActionDeploy,
			)
			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kc)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		}
	})
})
