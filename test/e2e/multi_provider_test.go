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
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/aws"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/azure"
	"github.com/K0rdent/kcm/test/e2e/credential"
	"github.com/K0rdent/kcm/test/e2e/flux"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
)

var _ = Context("Multi Cloud Templates", Label("provider:multi-cloud", "provider:aws-azure"), Ordered, ContinueOnFailure, func() {
	var (
		azureStandaloneDeleteFunc     func() error
		awsStandaloneDeleteFunc       func() error
		multiClusterServiceDeleteFunc func() error
		azureClusterDeploymentName    string
		awsClusterDeploymentName      string

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
						Name: "k0rdent-catalog",
					},
					Version: "4.12.3",
				},
			},
		}
	)

	const (
		multiCloudLabelKey   = "k0rdent.mirantis.com/test"
		multiCloudLabelValue = "multi-cloud"

		helmRepositoryName  = "k0rdent-catalog"
		serviceTemplateName = "ingress-nginx-4-12-3"
	)

	BeforeAll(func() {
		By("Ensuring that env vars are set correctly")
		aws.CheckEnv()
		azure.CheckEnv()

		By("Creating kube client")
		kc = kubeclient.NewFromLocal(kubeutil.DefaultSystemNamespace)

		By("Providing cluster identity and credentials", func() {
			credential.Apply("", "aws", "azure")
		})

		By("Creating HelmRepository and ServiceTemplate", func() {
			flux.CreateHelmRepository(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace, helmRepositoryName, helmRepositorySpec)
			templates.CreateServiceTemplate(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace, serviceTemplateName, serviceTemplateSpec)
		})
	})

	AfterEach(func() {
		// If we failed collect the support bundle before the cleanup
		if CurrentSpecReport().Failed() && cleanup() {
			By("collecting the support bundle from the management cluster")
			logs.SupportBundle(kc, "")

			for _, clusterName := range []string{awsClusterDeploymentName, azureClusterDeploymentName} {
				By(fmt.Sprintf("collecting the support bundle from the %s cluster", awsClusterDeploymentName))
				logs.SupportBundle(kc, clusterName)
			}
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
		var err error
		clusterTemplates, err = templates.GetSortedClusterTemplates(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace)
		Expect(err).NotTo(HaveOccurred())

		By("setting environment variables", func() {
			GinkgoT().Setenv(clusterdeployment.EnvVarAWSInstanceType, "t3.xlarge")
		})

		By("creating standalone cluster in Azure", func() {
			azureTemplates := templates.FindLatestTemplatesWithType(clusterTemplates, templates.TemplateAzureStandaloneCP, 1)
			Expect(azureTemplates).NotTo(BeEmpty())

			azureClusterDeploymentName = clusterdeployment.GenerateClusterName("")
			sd := clusterdeployment.Generate(templates.TemplateAzureStandaloneCP, azureClusterDeploymentName, azureTemplates[0])
			azureStandaloneDeleteFunc = clusterdeployment.Create(context.Background(), kc.CrClient, sd)

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
			sd := clusterdeployment.Generate(templates.TemplateAWSStandaloneCP, awsClusterDeploymentName, awsTemplates[0])
			awsStandaloneDeleteFunc = clusterdeployment.Create(context.Background(), kc.CrClient, sd)

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
			mcs := &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-mcs",
				},
				Spec: kcmv1.MultiClusterServiceSpec{
					ClusterSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							multiCloudLabelKey: multiCloudLabelValue,
						},
					},
					ServiceSpec: kcmv1.ServiceSpec{
						Provider: kcmv1.StateManagementProviderConfig{
							Name: kubeutil.DefaultStateManagementProvider,
						},
						Services: []kcmv1.Service{
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
			mcsUnstructured.SetGroupVersionKind(kcmv1.GroupVersion.WithKind("MultiClusterService"))

			multiClusterServiceDeleteFunc = kc.CreateMultiClusterService(context.Background(), mcsUnstructured)
		})

		By("adding labels to deployed clusters", func() {
			gvr := schema.GroupVersionResource{
				Group:    "k0rdent.mirantis.com",
				Version:  "v1beta1",
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
