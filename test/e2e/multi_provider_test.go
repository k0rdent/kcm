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
	"encoding/json"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/K0rdent/kcm/api/v1alpha1"
	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment/clusteridentity"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
)

var _ = Context("Multi Cloud Templates", Label("provider:multi-cloud", "provider:aws-azure"), Ordered, func() {
	var (
		kc                             *kubeclient.KubeClient
		azureStandaloneDeleteFunc      func() error
		awsStandaloneDeleteFunc        func() error
		multiClusterServiceDeleteFunc  func() error
		serviceTemplateChainDeleteFunc func() error
		azureClusterDeploymentName     string
		awsClusterDeploymentName       string
		mcs                            *v1alpha1.MultiClusterService
	)

	const (
		multiCloudLabelKey   = "k0rdent.mirantis.com/test"
		multiCloudLabelValue = "multi-cloud"
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
	})

	AfterEach(func() {
		// If we failed collect logs from each of the affiliated controllers
		// as well as the output of clusterctl to store as artifacts.
		if CurrentSpecReport().Failed() && cleanup() {
			if kc != nil {
				By("collecting failure logs from controllers")
				logs.Collector{
					Client:        kc,
					ProviderTypes: []clusterdeployment.ProviderType{clusterdeployment.ProviderAWS, clusterdeployment.ProviderAzure, clusterdeployment.ProviderCAPI},
					ClusterNames:  []string{azureClusterDeploymentName, awsClusterDeploymentName},
				}.CollectAll()
			}
		}

		By("deleting resources")
		for _, deleteFunc := range []func() error{
			multiClusterServiceDeleteFunc,
			serviceTemplateChainDeleteFunc,
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
		const (
			multiClusterServiceName    = "test-mcs"
			initialServiceTemplateName = "ingress-nginx-4-11-0"
			upgradeServiceTemplateName = "ingress-nginx-4-13-0"
		)
		By("setting environment variables", func() {
			GinkgoT().Setenv(clusterdeployment.EnvVarAWSInstanceType, "t3.xlarge")
		})

		By("creating standalone cluster in Azure", func() {
			azureClusterDeploymentName = clusterdeployment.GenerateClusterName("")
			sd := clusterdeployment.GetUnstructured(templates.TemplateAzureStandaloneCP, azureClusterDeploymentName, templates.Default[templates.TemplateAzureStandaloneCP])
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
			awsClusterDeploymentName = clusterdeployment.GenerateClusterName("")
			sd := clusterdeployment.GetUnstructured(templates.TemplateAWSStandaloneCP, awsClusterDeploymentName, templates.Default[templates.TemplateAWSStandaloneCP])
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
			mcs = &v1alpha1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{
					Name: multiClusterServiceName,
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
								Template:  initialServiceTemplateName,
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

		By("creating service template chain", func() {
			serviceTemplateChain := &v1alpha1.ServiceTemplateChain{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-service-template-chain",
					Namespace: internalutils.DefaultSystemNamespace,
				},
				Spec: v1alpha1.TemplateChainSpec{
					SupportedTemplates: []v1alpha1.SupportedTemplate{
						{
							Name: initialServiceTemplateName,
							AvailableUpgrades: []v1alpha1.AvailableUpgrade{
								{Name: upgradeServiceTemplateName},
							},
						},
						{
							Name: upgradeServiceTemplateName,
						},
					},
				},
			}
			data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(serviceTemplateChain)
			Expect(err).NotTo(HaveOccurred())
			serviceTemplateChainUnstructured := new(unstructured.Unstructured)
			serviceTemplateChainUnstructured.SetUnstructuredContent(data)
			serviceTemplateChainUnstructured.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("ServiceTemplateChain"))

			serviceTemplateChainDeleteFunc = kc.CreateServiceTemplateChain(context.Background(), serviceTemplateChainUnstructured)
		})

		By("ensuring service template for upgrade version exists", func() {
			Eventually(func(g Gomega) {
				templateUnstructured, err := kc.GetServiceTemplates(context.Background(), upgradeServiceTemplateName)
				g.Expect(err).NotTo(HaveOccurred())
				template := new(v1alpha1.ServiceTemplate)
				g.Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(templateUnstructured.Object, &template)).To(Succeed())
				g.Expect(template.Status.Valid).To(BeTrue())
			}).WithTimeout(1 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})

		By("updating multi cluster service to upgrade version", func() {
			patch := map[string]any{
				"spec": map[string]any{
					"serviceSpec": map[string]any{
						"services": []any{
							map[string]any{
								"name":     "managed-ingress-nginx",
								"template": upgradeServiceTemplateName,
							},
						},
					},
				},
			}
			patchBytes, err := json.Marshal(patch)
			Expect(err).NotTo(HaveOccurred())
			_, err = kc.PatchMultiClusterService(context.Background(), multiClusterServiceName, types.MergePatchType, patchBytes)
			Expect(err).NotTo(HaveOccurred())
		})

		By("validating service is upgraded", func() {
			awsServiceDeployedValidator := clusterdeployment.NewServiceValidator(awsClusterDeploymentName, "managed-ingress-nginx", "default").
				WithResourceValidation("helmrelease", clusterdeployment.ManagedServiceResource{
					ValidationFunc: clusterdeployment.ValidateHelmRelease,
				}).
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
				WithResourceValidation("helmrelease", clusterdeployment.ManagedServiceResource{
					ValidationFunc: clusterdeployment.ValidateHelmRelease,
				}).
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
