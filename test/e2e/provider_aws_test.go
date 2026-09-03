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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/serviceset"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/aws"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/credential"
	"github.com/K0rdent/kcm/test/e2e/flux"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/multiclusterservice"
	"github.com/K0rdent/kcm/test/e2e/templates"
	"github.com/K0rdent/kcm/test/e2e/upgrade"
	executil "github.com/K0rdent/kcm/test/util/exec"
)

var _ = Describe("AWS Templates", Label("provider:cloud", "provider:aws"), Ordered, ContinueOnFailure, func() {
	const (
		helmRepositoryName  = "k0rdent-catalog"
		serviceTemplateName = "ingress-nginx-4-12-3"
		// multiClusterServiceTemplate   = "kyverno-3-4-4"
		// multiClusterServiceName       = "test-multicluster"
		// multiClusterServiceMatchLabel = "k0rdent.mirantis.com/test-cluster-name"

		namespacedMultiClusterServiceTemplate   = "postgres-operator-1-15-1"
		namespacedMultiClusterServiceNamespace  = "postgres-operator"
		namespacedMultiClusterServiceName       = "test-namespacedmulticluster"
		namespacedMultiClusterServiceMatchLabel = "k0rdent.mirantis.com/test-cluster-name"
	)

	var (
		kc                    *kubeclient.KubeClient
		standaloneClusters    []string
		hostedDeleteFuncs     []func() error
		standaloneDeleteFuncs []func() error
		kubeconfigDeleteFuncs []func() error

		helmRepositorySpec = sourcev1.HelmRepositorySpec{
			Type: "oci",
			URL:  "oci://ghcr.io/k0rdent/catalog/charts",
		}

		serviceTemplateSpec = kcmv1.ServiceTemplateSpec{
			Helm: &kcmv1.HelmSpec{
				ChartSpec: &sourcev1.HelmChartSpec{
					Chart: "ingress-nginx",
					SourceRef: sourcev1.LocalHelmChartSourceReference{
						Kind: sourcev1.HelmRepositoryKind,
						Name: helmRepositoryName,
					},
					Version: "4.12.3",
				},
			},
		}

		// multiClusterServiceTemplateSpec = kcmv1.ServiceTemplateSpec{
		// 	Helm: &kcmv1.HelmSpec{
		// 		ChartSpec: &sourcev1.HelmChartSpec{
		// 			Chart: "kyverno",
		// 			SourceRef: sourcev1.LocalHelmChartSourceReference{
		// 				Kind: sourcev1.HelmRepositoryKind,
		// 				Name: helmRepositoryName,
		// 			},
		// 			Version: "3.4.4",
		// 		},
		// 	},
		// }

		namespacedMultiClusterServiceTemplateSpec = kcmv1.ServiceTemplateSpec{
			Helm: &kcmv1.HelmSpec{
				ChartSpec: &sourcev1.HelmChartSpec{
					Chart: "postgres-operator",
					SourceRef: sourcev1.LocalHelmChartSourceReference{
						Kind: sourcev1.HelmRepositoryKind,
						Name: helmRepositoryName,
					},
					Version: "1.15.1",
				},
			},
		}
	)

	// updateClusterDeploymentLabel sets the given label value on the given ClusterDeployment.
	// updateClusterDeploymentLabel := func(ctx context.Context, cl crclient.Client, cd *kcmv1.ClusterDeployment, label, value string) {
	// 	toUpdate := kcmv1.ClusterDeployment{}
	// 	Expect(cl.Get(ctx, crclient.ObjectKeyFromObject(cd), &toUpdate)).NotTo(HaveOccurred())
	// 	if toUpdate.Labels == nil {
	// 		toUpdate.Labels = map[string]string{}
	// 	}
	// 	toUpdate.Labels[label] = value
	// 	clusterdeployment.Update(ctx, cl, &toUpdate)
	// }

	BeforeAll(func() {
		By("Ensuring that env vars are set correctly")
		aws.CheckEnv()

		By("Creating kube client")
		kc = kubeclient.NewFromLocal(kubeutil.DefaultSystemNamespace)

		By("Providing cluster identity and credentials")
		credential.Apply("", "aws")

		By("Creating HelmRepository and ServiceTemplate", func() {
			flux.CreateHelmRepository(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace, helmRepositoryName, helmRepositorySpec)
			templates.CreateServiceTemplate(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace, serviceTemplateName, serviceTemplateSpec)
			// templates.CreateServiceTemplate(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace, multiClusterServiceTemplate, multiClusterServiceTemplateSpec)
			templates.CreateServiceTemplate(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace, namespacedMultiClusterServiceTemplate, namespacedMultiClusterServiceTemplateSpec)
		})
	})

	AfterAll(func() {
		// If we failed collect the support bundle before the cleanup
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

	// ensureClusterTemplateInNamespace copies the named ClusterTemplate from kcm-system into
	// namespace ns, and - since this repo's aws-standalone-cp/aws-eks templates reference their
	// Helm chart via a same-namespace-only chartSpec.sourceRef, not the cross-namespace-capable
	// chartRef - also copies the HelmRepository it points at. ClusterTemplate, ServiceTemplate,
	// Credential and HelmRepository are all namespace-scoped and resolved relative to the
	// referencing object's own namespace (see ClusterDeploymentValidator.getClusterDeploymentTemplate
	// and validationutil.ClusterDeployCredential), so a ClusterDeployment living outside kcm-system
	// needs its own copies of everything it references. Copying a bare Spec is safe:
	// ClusterTemplateReconciler (internal/controller/template_controller.go) validates every
	// ClusterTemplate purely from its own Spec regardless of namespace or provenance, so the copy
	// gets independently re-validated exactly the way the original already was.
	ensureClusterTemplateInNamespace := func(ctx context.Context, name, ns string) {
		src := &kcmv1.ClusterTemplate{}
		Expect(kc.CrClient.Get(ctx, crclient.ObjectKey{Namespace: kubeutil.DefaultSystemNamespace, Name: name}, src)).To(Succeed())

		if chartSpec := src.Spec.Helm.ChartSpec; chartSpec != nil && chartSpec.SourceRef.Kind == sourcev1.HelmRepositoryKind {
			srcRepo := &sourcev1.HelmRepository{}
			Expect(kc.CrClient.Get(ctx, crclient.ObjectKey{Namespace: kubeutil.DefaultSystemNamespace, Name: chartSpec.SourceRef.Name}, srcRepo)).To(Succeed())
			flux.CreateHelmRepository(ctx, kc.CrClient, ns, srcRepo.Name, *srcRepo.Spec.DeepCopy())
		}

		dst := &kcmv1.ClusterTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: src.Name, Namespace: ns},
			Spec:       *src.Spec.DeepCopy(),
		}
		Expect(crclient.IgnoreAlreadyExists(kc.CrClient.Create(ctx, dst))).NotTo(HaveOccurred())

		Eventually(func() bool {
			fresh := &kcmv1.ClusterTemplate{}
			if err := kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(dst), fresh); err != nil {
				return false
			}
			return fresh.Status.Valid
		}).WithTimeout(3*time.Minute).WithPolling(5*time.Second).Should(BeTrue(), fmt.Sprintf("ClusterTemplate %s/%s never became valid", ns, name))
	}

	for i, testingConfig := range config.Config[config.TestingProviderAWS] {
		It(fmt.Sprintf("Verifying AWS cluster deployment. Iteration: %d", i), func() {
			defer GinkgoRecover()
			testingConfig.SetDefaults(clusterTemplates, config.TestingProviderAWS)

			By(testingConfig.Description())

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

			aws.PopulateStandaloneEnvVars(testingConfig)

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

			// mcs := multiclusterservice.BuildMultiClusterService(sd, multiClusterServiceTemplate, multiClusterServiceMatchLabel, multiClusterServiceName)
			// multiclusterservice.CreateMultiClusterService(context.Background(), kc.CrClient, mcs)
			// multiclusterservice.ValidateMultiClusterService(context.Background(), kc, multiClusterServiceName, 1)
			// updateClusterDeploymentLabel(context.Background(), kc.CrClient, sd, multiClusterServiceMatchLabel, "not-matched")
			// multiclusterservice.ValidateMultiClusterService(context.Background(), kc, multiClusterServiceName, 0)

			By("verifying NamespacedMultiClusterService reconciles a namespace-scoped ServiceSet for the ClusterDeployment", func() {
				// aws-eks ClusterDeployments don't carry this label by default (unlike
				// aws-standalone-cp), so set it explicitly to make the selector match
				// regardless of which template type this iteration is testing.
				updateClusterDeploymentLabel(context.Background(), kc.CrClient, sd, namespacedMultiClusterServiceMatchLabel, sd.Name)

				nmcs := multiclusterservice.BuildNamespacedMultiClusterService(sd, namespacedMultiClusterServiceTemplate, namespacedMultiClusterServiceNamespace, namespacedMultiClusterServiceMatchLabel, namespacedMultiClusterServiceName)
				nmcsDeleteFunc := multiclusterservice.CreateNamespacedMultiClusterServiceWithDelete(context.Background(), kc.CrClient, nmcs)
				multiclusterservice.ValidateNamespacedMultiClusterService(context.Background(), kc, sd.Namespace, namespacedMultiClusterServiceName, 1)

				updateClusterDeploymentLabel(context.Background(), kc.CrClient, sd, namespacedMultiClusterServiceMatchLabel, "not-matched")
				multiclusterservice.ValidateNamespacedMultiClusterService(context.Background(), kc, sd.Namespace, namespacedMultiClusterServiceName, 0)

				Expect(nmcsDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete NamespacedMultiClusterService")
			})

			if !testingConfig.Upgrade && testingConfig.Hosted == nil {
				return
			}

			standaloneClient := kc.NewFromCluster(context.Background(), kubeutil.DefaultSystemNamespace, sdName)

			var hdName string
			if testingConfig.Hosted != nil {
				templateBy(templates.TemplateAWSHostedCP, "installing controller and templates on standalone cluster")

				// Download the KUBECONFIG for the standalone cluster and load it
				// so we can call Make targets against this cluster.
				// TODO(#472): Ideally we shouldn't use Make here and should just
				// convert these Make targets into Go code, but this will require a
				// helmclient.
				kubeCfgPath, _, kubecfgDeleteFunc, err := kc.WriteKubeconfig(context.Background(), sdName)
				Expect(err).To(Succeed())
				kubeconfigDeleteFuncs = append(kubeconfigDeleteFuncs, kubecfgDeleteFunc)

				GinkgoT().Setenv("KUBECONFIG", kubeCfgPath)
				cmd := exec.Command("make", "test-apply")
				_, err = executil.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
				Expect(os.Unsetenv("KUBECONFIG")).To(Succeed())

				templateBy(templates.TemplateAWSHostedCP, "validating that the controller is ready")

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

				// Populate the environment variables required for the hosted cluster.
				aws.PopulateHostedTemplateVars(context.Background(), kc, testingConfig.Hosted.Architecture, sdName)

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
					// Validate hosted deployment after the standalone upgrade
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

		It(fmt.Sprintf("NamespacedMultiClusterService matches and stops matching a ClusterDeployment in a custom namespace via a custom label. Iteration: %d", i), func() {
			defer GinkgoRecover()
			ctx := context.Background()

			const (
				customNamespace = "aws-nmcs-custom-ns"
				customLabel     = namespacedMultiClusterServiceMatchLabel
				customNMCSName  = "test-namespacedmulticluster-custom-ns"

				// Must match config/dev/aws-credentials.yaml: the AWSClusterStaticIdentity and its
				// backing Secret are shared/cluster-wide (allowedNamespaces: {} permits use from
				// any namespace), but Credential is namespace-scoped, so a ClusterDeployment living
				// outside kcm-system needs its own Credential pointing at that same identity -
				// exactly what AccessManagement's own Credential-distribution mechanism does
				// (internal/controller/accessmanagement_controller.go's createCredential), just
				// done directly here since there's no ClusterTemplateChain/ServiceTemplateChain
				// wired up for the AWS templates that AccessManagement would otherwise need too.
				awsIdentityName       = "aws-cluster-identity"
				awsIdentityAPIVersion = "infrastructure.cluster.x-k8s.io/v1beta2"
				awsIdentityKind       = "AWSClusterStaticIdentity"
				credentialName        = "aws-cluster-identity-cred"
			)

			By(fmt.Sprintf("creating namespace %s", customNamespace))
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: customNamespace}}
			Expect(crclient.IgnoreAlreadyExists(kc.CrClient.Create(ctx, ns))).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating a Credential in %s referencing the shared AWS identity", customNamespace))
			cred := &kcmv1.Credential{
				ObjectMeta: metav1.ObjectMeta{Name: credentialName, Namespace: customNamespace},
				Spec: kcmv1.CredentialSpec{
					Description: "AWS credentials (custom namespace e2e test)",
					IdentityRef: &corev1.ObjectReference{
						APIVersion: awsIdentityAPIVersion,
						Kind:       awsIdentityKind,
						Name:       awsIdentityName,
						Namespace:  kubeutil.DefaultSystemNamespace,
					},
				},
			}
			Expect(crclient.IgnoreAlreadyExists(kc.CrClient.Create(ctx, cred))).NotTo(HaveOccurred())
			Eventually(func() bool {
				fresh := &kcmv1.Credential{}
				if err := kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(cred), fresh); err != nil {
					return false
				}
				return fresh.Status.Ready
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(), "Credential never became Ready")

			sdTemplateType := templates.TemplateAWSStandaloneCP
			sdTemplates := templates.FindLatestTemplatesWithType(clusterTemplates, sdTemplateType, 1)
			Expect(sdTemplates).NotTo(BeEmpty(), "expected at least one aws-standalone-cp template")
			sdTemplate := sdTemplates[0]

			By(fmt.Sprintf("copying ClusterTemplate %s and its ServiceTemplate into %s", sdTemplate, customNamespace))
			ensureClusterTemplateInNamespace(ctx, sdTemplate, customNamespace)
			flux.CreateHelmRepository(ctx, kc.CrClient, customNamespace, helmRepositoryName, helmRepositorySpec)
			templates.CreateServiceTemplate(ctx, kc.CrClient, customNamespace, serviceTemplateName, serviceTemplateSpec)

			aws.PopulateEnvVars(config.ArchitectureAmd64)

			sdName := clusterdeployment.GenerateClusterName("aws-nmcs-custom-ns")
			templateBy(sdTemplateType, fmt.Sprintf("creating a ClusterDeployment %s in namespace %s with a custom label and no services", sdName, customNamespace))
			sd := clusterdeployment.Generate(sdTemplateType, sdName, sdTemplate)
			sd.Namespace = customNamespace
			if sd.Labels == nil {
				sd.Labels = map[string]string{}
			}
			sd.Labels[customLabel] = sdName
			sd.Spec.ServiceSpec.Services = nil

			// Every provider validator resolves the objects it checks through kc.Namespace (see
			// KubeClient.GetDynamicClient, which pins namespaced lookups to it), and so does the
			// workload cluster's kubeconfig Secret lookup. The CAPI Cluster, Machines, control
			// planes and that Secret are all created alongside the ClusterDeployment, so
			// validating one outside kcm-system needs a KubeClient scoped to its namespace -
			// passing the suite-wide kc here silently searches kcm-system and never finds them.
			kcCustomNS := kubeclient.NewFromLocal(customNamespace)

			standaloneDeleteFunc := clusterdeployment.Create(ctx, kc.CrClient, sd)
			standaloneClusters = append(standaloneClusters, sdName)
			standaloneDeleteFuncs = append(standaloneDeleteFuncs, func() error {
				By(fmt.Sprintf("Deleting the %s ClusterDeployment", sdName))
				err := standaloneDeleteFunc()
				Expect(err).NotTo(HaveOccurred())

				By(fmt.Sprintf("Verifying the %s ClusterDeployment deleted successfully", sdName))
				deletionValidator := clusterdeployment.NewProviderValidator(sdTemplateType, sdName, clusterdeployment.ValidationActionDelete)
				Eventually(func() error {
					return deletionValidator.Validate(context.Background(), kcCustomNS)
				}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
				return nil
			})

			// Fail fast if the ClusterDeployment never starts provisioning at all: the CAPI
			// Cluster object shows up within a poll or two of the ClusterDeployment being
			// admitted, long before the infrastructure is ready. Without this, a
			// ClusterDeployment that never reconciles is indistinguishable from one that is
			// still provisioning until the 30-minute validator below gives up.
			templateBy(sdTemplateType, fmt.Sprintf("waiting for the CAPI Cluster to be created in %s", customNamespace))
			Eventually(func() error {
				_, err := kcCustomNS.GetCluster(ctx, sdName)
				return err
			}).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
				"ClusterDeployment %s/%s never produced a CAPI Cluster - check its status conditions", customNamespace, sdName)

			templateBy(sdTemplateType, "waiting for the ClusterDeployment to become Ready")
			deploymentValidator := clusterdeployment.NewProviderValidator(sdTemplateType, sdName, clusterdeployment.ValidationActionDeploy)
			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kcCustomNS)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("creating a NamespacedMultiClusterService in the same namespace, matching via the custom label")
			nmcs := multiclusterservice.BuildNamespacedMultiClusterService(sd, serviceTemplateName, "default", customLabel, customNMCSName)
			nmcsDeleteFunc := multiclusterservice.CreateNamespacedMultiClusterServiceWithDelete(ctx, kc.CrClient, nmcs)

			By("verifying the NamespacedMultiClusterService matches the ClusterDeployment and its service is deployed (1/1)")
			multiclusterservice.ValidateNamespacedMultiClusterService(ctx, kc, customNamespace, customNMCSName, 1)

			By("verifying the ServiceSet is created and records .spec.namespacedMultiClusterService, not .spec.multiClusterService")
			multiclusterservice.ValidateNamespacedServiceSet(ctx, kc.CrClient, kubeutil.DefaultSystemNamespace, sd, nmcs)
			// The ServiceSet lands next to the ClusterDeployment, so this key is in customNamespace:
			// ObjectKey only falls back to the system namespace for the self-management ServiceSet.
			serviceSetKey := serviceset.ObjectKey(kubeutil.DefaultSystemNamespace, sd, nmcs)
			createdServiceSet := &kcmv1.ServiceSet{}
			Expect(kc.CrClient.Get(ctx, serviceSetKey, createdServiceSet)).To(Succeed())
			Expect(createdServiceSet.Spec.NamespacedMultiClusterService).To(Equal(nmcs.Namespace + "/" + nmcs.Name))
			Expect(createdServiceSet.Spec.MultiClusterService).To(BeEmpty())

			By("enabling selfManagement and verifying no management ServiceSet is ever created for it")
			Eventually(func(g Gomega) {
				fresh := &kcmv1.NamespacedMultiClusterService{}
				g.Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(nmcs), fresh)).To(Succeed())
				fresh.Spec.ServiceSpec.Provider.SelfManagement = true
				g.Expect(kc.CrClient.Update(ctx, fresh)).To(Succeed())
			}).Should(Succeed())

			// Still 1/1, not 1/2: for a MultiClusterService selfManagement would add the mgmt
			// pseudo-cluster to the denominator, so the count staying put is itself the evidence
			// that the flag was ignored.
			multiclusterservice.ValidateNamespacedMultiClusterService(ctx, kc, customNamespace, customNMCSName, 1)

			mgmtServiceSetKey := serviceset.ObjectKey(kubeutil.DefaultSystemNamespace, nil, nmcs)
			Consistently(func() bool {
				return apierrors.IsNotFound(kc.CrClient.Get(ctx, mgmtServiceSetKey, &kcmv1.ServiceSet{}))
			}, 30*time.Second, 5*time.Second).Should(BeTrue(),
				"selfManagement must be ignored for NamespacedMultiClusterService: no management ServiceSet %s should ever appear", mgmtServiceSetKey)

			By("removing the custom label from the ClusterDeployment")
			Eventually(func(g Gomega) {
				fresh := &kcmv1.ClusterDeployment{}
				g.Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(sd), fresh)).To(Succeed())
				delete(fresh.Labels, customLabel)
				g.Expect(kc.CrClient.Update(ctx, fresh)).To(Succeed())
			}).Should(Succeed())

			By("verifying the NamespacedMultiClusterService no longer matches the ClusterDeployment")
			multiclusterservice.ValidateNamespacedMultiClusterService(ctx, kc, customNamespace, customNMCSName, 0)

			// KeepServicesOnSelectorMismatch is left at its false default, so cleanupServiceSets
			// must tear this ServiceSet down once the ClusterDeployment stops matching - the
			// services it deployed are not meant to outlive the match.
			By("verifying the ServiceSet for the no-longer-matching ClusterDeployment is deleted")
			Eventually(func() bool {
				return apierrors.IsNotFound(kc.CrClient.Get(ctx, serviceSetKey, &kcmv1.ServiceSet{}))
			}).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(BeTrue(),
				"ServiceSet %s should be deleted once its ClusterDeployment no longer matches", serviceSetKey)

			By("deleting the NamespacedMultiClusterService")
			Expect(nmcsDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete NamespacedMultiClusterService")
		})
	}
})
