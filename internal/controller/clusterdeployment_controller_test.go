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

package controller

import (
	"time"

	hcv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hmc "github.com/K0rdent/kcm/api/v1alpha1"
)

var _ = Describe("ClusterDeployment Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			helmChartURL = "http://source-controller.hmc-system.svc.cluster.local/helmchart/hmc-system/test-chart/0.1.0.tar.gz"
		)

		// resources required for ClusterDeployment reconciliation
		var (
			namespace                = corev1.Namespace{}
			credential               = hmc.Credential{}
			clusterTemplate          = hmc.ClusterTemplate{}
			serviceTemplate          = hmc.ServiceTemplate{}
			helmRepo                 = sourcev1.HelmRepository{}
			clusterTemplateHelmChart = sourcev1.HelmChart{}
			serviceTemplateHelmChart = sourcev1.HelmChart{}

			clusterDeployment    = hmc.ClusterDeployment{}
			clusterDeploymentKey = types.NamespacedName{}
		)

		BeforeEach(func() {
			By("Ensure namespace", func() {
				namespace = corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-namespace-",
					},
				}
				Expect(k8sClient.Create(ctx, &namespace)).To(Succeed())
			})

			By("Ensure HelmRepository resource", func() {
				helmRepo = sourcev1.HelmRepository{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-repository-",
						Namespace:    namespace.Name,
					},
					Spec: sourcev1.HelmRepositorySpec{
						Insecure: true,
						Interval: metav1.Duration{
							Duration: 10 * time.Minute,
						},
						Provider: "generic",
						Type:     "oci",
						URL:      "oci://hmc-local-registry:5000/charts",
					},
				}
				Expect(k8sClient.Create(ctx, &helmRepo)).To(Succeed())
			})

			By("Ensure HelmChart resources", func() {
				clusterTemplateHelmChart = sourcev1.HelmChart{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-cluster-template-chart-",
						Namespace:    namespace.Name,
					},
					Spec: sourcev1.HelmChartSpec{
						Chart: "test-cluster",
						Interval: metav1.Duration{
							Duration: 10 * time.Minute,
						},
						ReconcileStrategy: sourcev1.ReconcileStrategyChartVersion,
						SourceRef: sourcev1.LocalHelmChartSourceReference{
							Kind: "HelmRepository",
							Name: helmRepo.Name,
						},
						Version: "0.1.0",
					},
				}
				Expect(k8sClient.Create(ctx, &clusterTemplateHelmChart)).To(Succeed())

				serviceTemplateHelmChart = sourcev1.HelmChart{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-service-template-chart-",
						Namespace:    namespace.Name,
					},
					Spec: sourcev1.HelmChartSpec{
						Chart: "test-service",
						Interval: metav1.Duration{
							Duration: 10 * time.Minute,
						},
						ReconcileStrategy: sourcev1.ReconcileStrategyChartVersion,
						SourceRef: sourcev1.LocalHelmChartSourceReference{
							Kind: "HelmRepository",
							Name: helmRepo.Name,
						},
						Version: "0.1.0",
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplateHelmChart)).To(Succeed())
			})

			By("Ensure ClusterTemplate resource", func() {
				clusterTemplate = hmc.ClusterTemplate{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-cluster-template-",
						Namespace:    namespace.Name,
					},
					Spec: hmc.ClusterTemplateSpec{
						Helm: hmc.HelmSpec{
							ChartRef: &hcv2.CrossNamespaceSourceReference{
								Kind:      "HelmChart",
								Name:      clusterTemplateHelmChart.Name,
								Namespace: namespace.Name,
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &clusterTemplate)).To(Succeed())
				clusterTemplate.Status = hmc.ClusterTemplateStatus{
					TemplateStatusCommon: hmc.TemplateStatusCommon{
						TemplateValidationStatus: hmc.TemplateValidationStatus{
							Valid: true,
						},
						Config: &apiextensionsv1.JSON{
							Raw: []byte(`{"foo":"bar"}`),
						},
					},
					Providers: hmc.Providers{"infrastructure-aws"},
				}
				Expect(k8sClient.Status().Update(ctx, &clusterTemplate)).To(Succeed())
			})

			By("Ensure ServiceTemplate resource", func() {
				serviceTemplate = hmc.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-service-template-",
						Namespace:    namespace.Name,
					},
					Spec: hmc.ServiceTemplateSpec{
						Helm: hmc.HelmSpec{
							ChartRef: &hcv2.CrossNamespaceSourceReference{
								Kind:      "HelmChart",
								Name:      serviceTemplateHelmChart.Name,
								Namespace: namespace.Name,
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).To(Succeed())
				serviceTemplate.Status = hmc.ServiceTemplateStatus{
					TemplateStatusCommon: hmc.TemplateStatusCommon{
						ChartRef: &hcv2.CrossNamespaceSourceReference{
							Kind:      "HelmChart",
							Name:      serviceTemplateHelmChart.Name,
							Namespace: namespace.Name,
						},
						TemplateValidationStatus: hmc.TemplateValidationStatus{
							Valid: true,
						},
					},
				}
				Expect(k8sClient.Status().Update(ctx, &serviceTemplate)).To(Succeed())
			})

			By("Ensure Credential resource", func() {
				credential = hmc.Credential{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-credential-",
						Namespace:    namespace.Name,
					},
					Spec: hmc.CredentialSpec{
						IdentityRef: &corev1.ObjectReference{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
							Kind:       "AWSClusterStaticIdentity",
							Name:       "foo",
						},
					},
				}
				Expect(k8sClient.Create(ctx, &credential)).To(Succeed())
				credential.Status = hmc.CredentialStatus{
					Ready: true,
				}
				Expect(k8sClient.Status().Update(ctx, &credential)).To(Succeed())
			})

			By("Ensure ClusterDeployment resource", func() {
				clusterDeployment = hmc.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-cluster-deployment-",
						Namespace:    namespace.Name,
					},
					Spec: hmc.ClusterDeploymentSpec{
						Template:   clusterTemplate.Name,
						Credential: credential.Name,
						Services: []hmc.ServiceSpec{
							{
								Template: serviceTemplate.Name,
								Name:     "test-service",
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &clusterDeployment)).To(Succeed())
				clusterDeploymentKey = types.NamespacedName{
					Namespace: clusterDeployment.Namespace,
					Name:      clusterDeployment.Name,
				}
			})
		})

		AfterEach(func() {
			By("Cleanup", func() {
				controllerReconciler := &ClusterDeploymentReconciler{
					Client: mgrClient,
				}

				// Running reconcile to remove the finalizer and delete the ClusterDeployment
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Delete(ctx, &clusterDeployment)).To(Succeed())
					_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clusterDeploymentKey})
					g.Expect(Get(&clusterDeployment)()).ShouldNot(Succeed())
				}).Should(Succeed())

				Expect(k8sClient.Delete(ctx, &clusterTemplate)).To(Succeed())
				Expect(k8sClient.Delete(ctx, &namespace)).To(Succeed())
			})
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ClusterDeploymentReconciler{
				Client: mgrClient,
				Config: &rest.Config{},
			}

			By("Ensure finalizer is added", func() {
				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: clusterDeploymentKey,
					})
					Expect(err).NotTo(HaveOccurred())
					g.Expect(Object(&clusterDeployment)()).Should(SatisfyAll(
						HaveField("Finalizers", ContainElement(hmc.ClusterDeploymentFinalizer)),
					))
				}).Should(Succeed())
			})

			By("Reconciling resource with finalizer", func() {
				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: clusterDeploymentKey,
					})
					Expect(err).To(HaveOccurred())
					g.Expect(Object(&clusterDeployment)()).Should(SatisfyAll(
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", hmc.TemplateReadyCondition),
							HaveField("Status", metav1.ConditionTrue),
							HaveField("Reason", hmc.SucceededReason),
							HaveField("Message", "Template is valid"),
						))),
					))
				}).Should(Succeed())
			})

			By("Reconciling when dependencies are not in valid state", func() {
				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: clusterDeploymentKey,
					})
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring("helm chart source is not provided"))
				}).Should(Succeed())
			})

			// By("Patching ClusterTemplate and corresponding HelmChart statuses", func() {
			// 	Expect(Get(&clusterTemplate)()).To(Succeed())
			// 	clusterTemplate.Status.ChartRef = &hcv2.CrossNamespaceSourceReference{
			// 		Kind:      "HelmChart",
			// 		Name:      clusterTemplateHelmChart.Name,
			// 		Namespace: namespace.Name,
			// 	}
			// 	Expect(k8sClient.Status().Update(ctx, &clusterTemplate)).To(Succeed())
			//
			// 	Expect(Get(&clusterTemplateHelmChart)()).To(Succeed())
			// 	clusterTemplateHelmChart.Status.URL = helmChartURL
			// 	clusterTemplateHelmChart.Status.Artifact = &sourcev1.Artifact{
			// 		URL:            helmChartURL,
			// 		LastUpdateTime: metav1.Now(),
			// 	}
			// 	Expect(k8sClient.Status().Update(ctx, &clusterTemplateHelmChart)).To(Succeed())
			//
			// 	// todo: next error occurs due to dependency on helm library. The best way to mitigate this is to
			// 	//  inject an interface into the reconciler struct that can be mocked out for testing.
			// 	Eventually(func(g Gomega) {
			// 		_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
			// 			NamespacedName: clusterDeploymentKey,
			// 		})
			// 		g.Expect(err).To(HaveOccurred())
			// 	}).Should(Succeed())
			// })
		})
	})
})
