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

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/aws"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/credential"
	"github.com/K0rdent/kcm/test/e2e/flux"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
	"github.com/K0rdent/kcm/test/e2e/upgrade"
	"github.com/K0rdent/kcm/test/utils"
)

var _ = Describe("AWS Templates", Label("provider:cloud", "provider:aws"), Ordered, ContinueOnFailure, func() {
	var (
		kc                    *kubeclient.KubeClient
		standaloneClusters    []string
		hostedDeleteFuncs     []func() error
		standaloneDeleteFuncs []func() error
		kubeconfigDeleteFuncs []func() error

		helmRepositorySpec = sourcev1.HelmRepositorySpec{
			URL: "https://kubernetes.github.io/ingress-nginx",
		}
		serviceTemplateSpec = kcmv1.ServiceTemplateSpec{
			Helm: &kcmv1.HelmSpec{
				ChartSpec: &sourcev1.HelmChartSpec{
					Chart: "ingress-nginx",
					SourceRef: sourcev1.LocalHelmChartSourceReference{
						Kind: sourcev1.HelmRepositoryKind,
						Name: "ingress-nginx",
					},
					Version: "4.12.1",
				},
			},
		}
	)

	const (
		helmRepositoryName  = "ingress-nginx"
		serviceTemplateName = "ingress-nginx-4-12-1"
	)

	BeforeAll(func() {
		By("Ensuring that env vars are set correctly")
		aws.CheckEnv()

		By("Creating kube client")
		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)

		By("Providing cluster identity and credentials")
		credential.Apply("", "aws")

		By("Creating HelmRepository and ServiceTemplate", func() {
			flux.CreateHelmRepository(context.Background(), kc.CrClient, internalutils.DefaultSystemNamespace, helmRepositoryName, helmRepositorySpec)
			templates.CreateServiceTemplate(context.Background(), kc.CrClient, internalutils.DefaultSystemNamespace, serviceTemplateName, serviceTemplateSpec)
		})
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

	for i, testingConfig := range config.Config[config.TestingProviderAWS] {
		It(fmt.Sprintf("Verifying AWS cluster deployment. Iteration: %d", i), func() {
			defer GinkgoRecover()
			testingConfig.SetDefaults(clusterTemplates, config.TestingProviderAWS)

			By(testingConfig.Description())

			// Deploy a standalone cluster and verify it is running/ready.
			// Deploy standalone with an xlarge instance since it will also be
			// hosting the hosted cluster.
			GinkgoT().Setenv(clusterdeployment.EnvVarAWSInstanceType, "t3.xlarge")

			sdName := clusterdeployment.GenerateClusterName(fmt.Sprintf("aws-%d", i))
			sdTemplate := testingConfig.Template
			sdTemplateType := templates.GetType(sdTemplate)

			// Supported template types for AWS standalone deployment: aws-eks, aws-standalone-cp
			Expect(sdTemplateType).To(SatisfyAny(
				Equal(templates.TemplateAWSEKS),
				Equal(templates.TemplateAWSStandaloneCP)),
				fmt.Sprintf("template type should be either %s or %s", templates.TemplateAWSEKS, templates.TemplateAWSStandaloneCP))

			// Supported architectures for AWS standalone deployment: amd64, arm64
			Expect(testingConfig.Architecture).To(SatisfyAny(
				Equal(config.ArchitectureAmd64),
				Equal(config.ArchitectureArm64)),
				fmt.Sprintf("architecture should be either %s or %s", config.ArchitectureAmd64, config.ArchitectureArm64),
			)

			aws.PopulateEnvVars(testingConfig.Architecture)

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
				}).WithTimeout(10 * time.Minute).WithPolling(10 *
					time.Second).Should(Succeed())
				return nil
			})

			templateBy(sdTemplateType, "waiting for infrastructure to deploy successfully")
			deploymentValidator := clusterdeployment.NewProviderValidator(
				sdTemplateType,
				sdName,
				clusterdeployment.ValidationActionDeploy,
			)

			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kc)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			// validating service included in the cluster deployment is deployed
			if len(sd.Spec.ServiceSpec.Services) > 0 {
				svcName := os.Getenv("AWS_SERVICE_NAME")
				if svcName == "" {
					svcName = "managed-ingress-nginx"
				}

				if slices.ContainsFunc(sd.Spec.ServiceSpec.Services, func(a kcmv1.Service) bool {
					return a.Name == svcName
				}) {
					serviceDeployedValidator := clusterdeployment.NewServiceValidator(sdName, svcName, "default").
						WithResourceValidation("service", clusterdeployment.ManagedServiceResource{
							ResourceNameSuffix: "controller",
							ValidationFunc:     clusterdeployment.ValidateService,
						}).
						WithResourceValidation("deployment", clusterdeployment.ManagedServiceResource{
							ResourceNameSuffix: "controller",
							ValidationFunc:     clusterdeployment.ValidateDeployment,
						})
					Eventually(func() error {
						return serviceDeployedValidator.Validate(context.Background(), kc)
					}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
				}
			}

			if !testingConfig.Upgrade && testingConfig.Hosted == nil {
				return
			}

			standaloneClient := kc.NewFromCluster(context.Background(), internalutils.DefaultSystemNamespace, sdName)

			var hdName string
			if testingConfig.Hosted != nil {
				templateBy(templates.TemplateAWSHostedCP, "installing controller and templates on standalone cluster")

				// Download the KUBECONFIG for the standalone cluster and load it
				// so we can call Make targets against this cluster.
				// TODO(#472): Ideally we shouldn't use Make here and should just
				// convert these Make targets into Go code, but this will require a
				// helmclient.
				kubeCfgPath, _, kubecfgDeleteFunc := kc.WriteKubeconfig(context.Background(), sdName)
				kubeconfigDeleteFuncs = append(kubeconfigDeleteFuncs, kubecfgDeleteFunc)

				GinkgoT().Setenv("KUBECONFIG", kubeCfgPath)
				cmd := exec.Command("make", "test-apply")
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
				Expect(os.Unsetenv("KUBECONFIG")).To(Succeed())

				templateBy(templates.TemplateAWSHostedCP, "validating that the controller is ready")

				// TODO: remove after https://github.com/k0rdent/kcm/issues/1575 is fixed
				if testingConfig.Architecture == config.ArchitectureArm64 {
					removeInfobloxProvider(standaloneClient.CrClient)
				}

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
					_, err = utils.Run(exec.Command("make", "stable-templates"))
					Expect(err).NotTo(HaveOccurred())
				}

				// Ensure Cluster Templates in the standalone cluster are valid
				Eventually(func() error {
					err := clusterdeployment.ValidateClusterTemplates(context.Background(), standaloneClient)
					if err != nil {
						_, _ = fmt.Fprintf(GinkgoWriter, "cluster template validation failed: %v\n", err)
						return err
					}
					return nil
				}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				// Ensure AWS credentials are set in the standalone cluster.
				credential.Apply(kubeCfgPath, "aws")

				// Supported architectures for AWS hosted deployment: amd64, arm64
				Expect(testingConfig.Hosted.Architecture).To(SatisfyAny(
					Equal(config.ArchitectureAmd64),
					Equal(config.ArchitectureArm64)),
					fmt.Sprintf("architecture should be either %s or %s", config.ArchitectureAmd64, config.ArchitectureArm64),
				)

				aws.PopulateEnvVars(testingConfig.Hosted.Architecture)

				// Populate the environment variables required for the hosted cluster.
				aws.PopulateHostedTemplateVars(context.Background(), kc, sdName)

				hdName = clusterdeployment.GenerateClusterName(fmt.Sprintf("aws-hosted-%d", i))
				hdTemplate := testingConfig.Hosted.Template
				templateBy(templates.TemplateAWSHostedCP, fmt.Sprintf("creating a hosted ClusterDeployment %s with template %s", hdName, hdTemplate))
				hd := clusterdeployment.Generate(templates.TemplateAWSHostedCP, hdName, hdTemplate)

				// Deploy the hosted cluster on top of the standalone cluster.
				hostedDeleteFunc := clusterdeployment.Create(context.Background(), standaloneClient.CrClient, hd)
				hostedDeleteFuncs = append(hostedDeleteFuncs, func() error {
					By(fmt.Sprintf("Deleting the %s ClusterDeployment", hdName))
					err = hostedDeleteFunc()
					Expect(err).NotTo(HaveOccurred())

					By(fmt.Sprintf("Verifying the %s ClusterDeployment deleted successfully", hdName))
					deletionValidator := clusterdeployment.NewProviderValidator(
						templates.TemplateAWSHostedCP,
						hdName,
						clusterdeployment.ValidationActionDelete,
					)
					Eventually(func() error {
						return deletionValidator.Validate(context.Background(), standaloneClient)
					}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
					return nil
				})

				templateBy(templates.TemplateAWSHostedCP, "Patching AWSCluster to ready")
				clusterdeployment.PatchHostedClusterReady(standaloneClient, clusterdeployment.ProviderAWS, hdName)

				// Verify the hosted cluster is running/ready.
				templateBy(templates.TemplateAWSHostedCP, "waiting for infrastructure to deploy successfully")
				deploymentValidator = clusterdeployment.NewProviderValidator(
					templates.TemplateAWSHostedCP,
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
		})
	}
})
