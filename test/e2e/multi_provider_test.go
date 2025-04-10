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

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/K0rdent/kcm/api/v1alpha1"
	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/clusteridentity"
	"github.com/K0rdent/kcm/test/e2e/flux"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
)

var _ = Context("Multi Cloud Templates", Label("provider:multi-cloud", "provider:aws-azure"), Ordered, func() {
	var (
		kc                            *kubeclient.KubeClient
		azureStandaloneDeleteFunc     func() error
		awsStandaloneDeleteFunc       func() error
		multiClusterServiceDeleteFunc func() error
		azureClusterDeploymentName    string
		awsClusterDeploymentName      string

		helmRepositorySpec = sourcev1.HelmRepositorySpec{
			URL: "https://kubernetes.github.io/ingress-nginx",
		}
		serviceTemplateSpec = v1alpha1.ServiceTemplateSpec{
			Helm: &v1alpha1.HelmSpec{
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
		multiCloudLabelKey   = "k0rdent.mirantis.com/test"
		multiCloudLabelValue = "multi-cloud"

		helmRepositoryName  = "ingress-nginx"
		serviceTemplateName = "ingress-nginx-4-12-1"
	)

	BeforeAll(func() {
		kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)

		By("ensuring Azure credentials are set", func() {
			azureCi := clusteridentity.New(kc, clusterdeployment.ProviderAzure)
			azureCi.WaitForValidCredential(kc)
			Expect(os.Setenv(clusterdeployment.EnvVarAzureClusterIdentity, azureCi.IdentityName)).Should(Succeed())
		})

		By("ensuring AWS credentials are set", func() {
			awsCi := clusteridentity.New(kc, clusterdeployment.ProviderAWS)
			awsCi.WaitForValidCredential(kc)
			Expect(os.Setenv(clusterdeployment.EnvVarAWSClusterIdentity, awsCi.IdentityName)).Should(Succeed())
		})

		By("creating HelmRepository and ServiceTemplate", func() {
			flux.CreateHelmRepository(context.Background(), kc.CrClient, internalutils.DefaultSystemNamespace, helmRepositoryName, helmRepositorySpec)
			templates.CreateServiceTemplate(context.Background(), kc.CrClient, internalutils.DefaultSystemNamespace, serviceTemplateName, serviceTemplateSpec)
		})
	})

	AfterEach(func() {
		// If we failed collect the support bundle before the cleanup
		if CurrentSpecReport().Failed() && cleanup() {
			By("collecting the support bundle from the management cluster")
			logs.SupportBundle("")
		}

		By("deleting resources")
		for _, deleteFunc := range []func() error{
			multiClusterServiceDeleteFunc,
			awsStandaloneDeleteFunc,
			azureStandaloneDeleteFunc,
		} {
			if deleteFunc != nil {
				err := deleteFunc()
				Expect(err).NotTo(HaveOccurred())
			}
		}
	})

	It("should deploy service in multi-cloud environment", func() {
		clusterTemplates, err := templates.GetSortedClusterTemplates(context.Background(), kc.CrClient, internalutils.DefaultSystemNamespace)
		Expect(err).NotTo(HaveOccurred())

		By("setting environment variables", func() {
			GinkgoT().Setenv(clusterdeployment.EnvVarAWSInstanceType, "t3.xlarge")
		})

		By("creating standalone cluster in Azure", func() {
			azureTemplates := templates.FindLatestTemplatesWithType(clusterTemplates, templates.TemplateAzureStandaloneCP, 1)
			Expect(azureTemplates).NotTo(BeEmpty())

			azureClusterDeploymentName = clusterdeployment.GenerateClusterName("")
			sd := clusterdeployment.GetUnstructured(templates.TemplateAzureStandaloneCP, azureClusterDeploymentName, azureTemplates[0])
			azureStandaloneDeleteFunc = kc.CreateClusterDeployment(context.Background(), sd)

			deploymentValidator := clusterdeployment.NewProviderValidator(
				templates.TemplateAzureStandaloneCP,
				azureClusterDeploymentName,
				clusterdeployment.ValidationActionDeploy,
			)

			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kc)
			}).WithTimeout(90 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})

		By("creating standalone cluster in AWS", func() {
			awsTemplates := templates.FindLatestTemplatesWithType(clusterTemplates, templates.TemplateAWSStandaloneCP, 1)
			Expect(awsTemplates).NotTo(BeEmpty())

			awsClusterDeploymentName = clusterdeployment.GenerateClusterName("")
			sd := clusterdeployment.GetUnstructured(templates.TemplateAWSStandaloneCP, awsClusterDeploymentName, awsTemplates[0])
			awsStandaloneDeleteFunc = kc.CreateClusterDeployment(context.Background(), sd)

			deploymentValidator := clusterdeployment.NewProviderValidator(
				templates.TemplateAWSStandaloneCP,
				awsClusterDeploymentName,
				clusterdeployment.ValidationActionDeploy,
			)

			Eventually(func() error {
				return deploymentValidator.Validate(context.Background(), kc)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})

		By("creating multi-cluster service", func() {
			mcs := &v1alpha1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-mcs",
				},
				Spec: v1alpha1.MultiClusterServiceSpec{
					ClusterSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							multiCloudLabelKey: multiCloudLabelValue,
						},
					},
					ServiceSpec: v1alpha1.ServiceSpec{
						Services: []v1alpha1.Service{
							{
								Name:      "managed-ingress-nginx",
								Namespace: "default",
								Template:  serviceTemplateName,
							},
						},
					},
				},
			}
			data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(mcs)
			Expect(err).NotTo(HaveOccurred())
			mcsUnstructured := new(unstructured.Unstructured)
			mcsUnstructured.SetUnstructuredContent(data)
			mcsUnstructured.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("MultiClusterService"))

			multiClusterServiceDeleteFunc = kc.CreateMultiClusterService(context.Background(), mcsUnstructured)
		})

		By("adding labels to deployed clusters", func() {
			gvr := schema.GroupVersionResource{
				Group:    "k0rdent.mirantis.com",
				Version:  "v1alpha1",
				Resource: "clusterdeployments",
			}
			dynClient := kc.GetDynamicClient(gvr, true)

			azureCluster, err := dynClient.Get(context.Background(), azureClusterDeploymentName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			azureClusterLabels := azureCluster.GetLabels()
			azureClusterLabels[multiCloudLabelKey] = multiCloudLabelValue
			azureCluster.SetLabels(azureClusterLabels)
			_, err = dynClient.Update(context.Background(), azureCluster, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())

			awsCluster, err := dynClient.Get(context.Background(), awsClusterDeploymentName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			awsClusterLabels := awsCluster.GetLabels()
			awsClusterLabels[multiCloudLabelKey] = multiCloudLabelValue
			awsCluster.SetLabels(awsClusterLabels)
			_, err = dynClient.Update(context.Background(), awsCluster, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		By("validating service is deployed", func() {
			awsServiceDeployedValidator := clusterdeployment.NewServiceValidator(awsClusterDeploymentName, "managed-ingress-nginx", "default").
				WithResourceValidation("service", clusterdeployment.ManagedServiceResource{
					ResourceNameSuffix: "controller",
					ValidationFunc:     clusterdeployment.ValidateService,
				}).
				WithResourceValidation("deployment", clusterdeployment.ManagedServiceResource{
					ResourceNameSuffix: "controller",
					ValidationFunc:     clusterdeployment.ValidateDeployment,
				})
			Eventually(func() error {
				return awsServiceDeployedValidator.Validate(context.Background(), kc)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			azureServiceDeployedValidator := clusterdeployment.NewServiceValidator(azureClusterDeploymentName, "managed-ingress-nginx", "default").
				WithResourceValidation("service", clusterdeployment.ManagedServiceResource{
					ResourceNameSuffix: "controller",
					ValidationFunc:     clusterdeployment.ValidateService,
				}).
				WithResourceValidation("deployment", clusterdeployment.ManagedServiceResource{
					ResourceNameSuffix: "controller",
					ValidationFunc:     clusterdeployment.ValidateDeployment,
				})
			Eventually(func() error {
				return azureServiceDeployedValidator.Validate(context.Background(), kc)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})
	})
})
