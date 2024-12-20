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
	"context"
	"time"

	hcv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hmc "github.com/K0rdent/kcm/api/v1alpha1"
)

var _ = Describe("ClusterDeployment Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			clusterDeploymentName      = "test-cluster-deployment"
			clusterDeploymentNamespace = "test"

			templateName    = "test-template"
			svcTemplateName = "test-svc-template"
			credentialName  = "test-credential"

			helmChartURL = "http://source-controller.hmc-system.svc.cluster.local/helmchart/hmc-system/test-chart/0.1.0.tar.gz"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      clusterDeploymentName,
			Namespace: clusterDeploymentNamespace,
		}
		clusterDeployment := &hmc.ClusterDeployment{}
		template := &hmc.ClusterTemplate{}
		svcTemplate := &hmc.ServiceTemplate{}
		management := &hmc.Management{}
		credential := &hmc.Credential{}
		namespace := &corev1.Namespace{}

		BeforeEach(func() {
			By("creating ClusterDeployment namespace")
			err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterDeploymentNamespace}, namespace)
			if err != nil && errors.IsNotFound(err) {
				namespace = &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: clusterDeploymentNamespace,
					},
				}
				Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			}

			By("creating the custom resource for the Kind ClusterTemplate")
			err = k8sClient.Get(ctx, typeNamespacedName, template)
			if err != nil && errors.IsNotFound(err) {
				template = &hmc.ClusterTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      templateName,
						Namespace: clusterDeploymentNamespace,
					},
					Spec: hmc.ClusterTemplateSpec{
						Helm: hmc.HelmSpec{
							ChartRef: &hcv2.CrossNamespaceSourceReference{
								Kind:      "HelmChart",
								Name:      "ref-test",
								Namespace: "default",
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, template)).To(Succeed())
				template.Status = hmc.ClusterTemplateStatus{
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
				Expect(k8sClient.Status().Update(ctx, template)).To(Succeed())
			}

			By("creating the custom resource for the Kind ServiceTemplate")
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: clusterDeploymentNamespace, Name: svcTemplateName}, svcTemplate)
			if err != nil && errors.IsNotFound(err) {
				svcTemplate = &hmc.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      svcTemplateName,
						Namespace: clusterDeploymentNamespace,
					},
					Spec: hmc.ServiceTemplateSpec{
						Helm: hmc.HelmSpec{
							ChartRef: &hcv2.CrossNamespaceSourceReference{
								Kind:      "HelmChart",
								Name:      "ref-test",
								Namespace: "default",
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, svcTemplate)).To(Succeed())
				svcTemplate.Status = hmc.ServiceTemplateStatus{
					TemplateStatusCommon: hmc.TemplateStatusCommon{
						ChartRef: &hcv2.CrossNamespaceSourceReference{
							Kind:      "HelmChart",
							Name:      "ref-test",
							Namespace: "default",
						},
						TemplateValidationStatus: hmc.TemplateValidationStatus{
							Valid: true,
						},
					},
				}
				Expect(k8sClient.Status().Update(ctx, svcTemplate)).To(Succeed())
			}

			By("creating the custom resource for the Kind Management")
			err = k8sClient.Get(ctx, typeNamespacedName, management)
			if err != nil && errors.IsNotFound(err) {
				management = &hmc.Management{
					ObjectMeta: metav1.ObjectMeta{
						Name: hmc.ManagementName,
					},
					Spec: hmc.ManagementSpec{
						Release: "test-release",
					},
				}
				Expect(k8sClient.Create(ctx, management)).To(Succeed())
				management.Status = hmc.ManagementStatus{
					AvailableProviders: hmc.Providers{"infrastructure-aws"},
				}
				Expect(k8sClient.Status().Update(ctx, management)).To(Succeed())
			}
			By("creating the custom resource for the Kind Credential")
			err = k8sClient.Get(ctx, typeNamespacedName, credential)
			if err != nil && errors.IsNotFound(err) {
				credential = &hmc.Credential{
					ObjectMeta: metav1.ObjectMeta{
						Name:      credentialName,
						Namespace: clusterDeploymentNamespace,
					},
					Spec: hmc.CredentialSpec{
						IdentityRef: &corev1.ObjectReference{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
							Kind:       "AWSClusterStaticIdentity",
							Name:       "foo",
						},
					},
				}
				Expect(k8sClient.Create(ctx, credential)).To(Succeed())
				credential.Status = hmc.CredentialStatus{
					Ready: true,
				}
				Expect(k8sClient.Status().Update(ctx, credential)).To(Succeed())
			}

			By("creating the custom resource for the Kind ClusterDeployment")
			err = k8sClient.Get(ctx, typeNamespacedName, clusterDeployment)
			if err != nil && errors.IsNotFound(err) {
				clusterDeployment = &hmc.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeploymentName,
						Namespace: clusterDeploymentNamespace,
					},
					Spec: hmc.ClusterDeploymentSpec{
						Template:   templateName,
						Credential: credentialName,
						Services: []hmc.ServiceSpec{
							{
								Template: svcTemplateName,
								Name:     "test-svc-name",
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, clusterDeployment)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup")

			controllerReconciler := &ClusterDeploymentReconciler{
				Client: k8sClient,
			}

			Expect(k8sClient.Delete(ctx, clusterDeployment)).To(Succeed())
			// Running reconcile to remove the finalizer and delete the ClusterDeployment
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Eventually(k8sClient.Get, 1*time.Minute, 5*time.Second).WithArguments(ctx, typeNamespacedName, clusterDeployment).Should(HaveOccurred())

			Expect(k8sClient.Delete(ctx, template)).To(Succeed())
			Expect(k8sClient.Delete(ctx, management)).To(Succeed())
			Expect(k8sClient.Delete(ctx, namespace)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ClusterDeploymentReconciler{
				Client: mgrClient,
				Config: &rest.Config{},
			}

			By("Ensure finalizer is added")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Eventually(Object(clusterDeployment)).Should(SatisfyAll(
				HaveField("Finalizers", ContainElement(hmc.ClusterDeploymentFinalizer)),
			))

			By("Reconciling resource with finalizer")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).To(HaveOccurred())
			Eventually(Object(clusterDeployment)).Should(SatisfyAll(
				HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", hmc.TemplateReadyCondition),
					HaveField("Status", metav1.ConditionTrue),
					HaveField("Reason", hmc.SucceededReason),
					HaveField("Message", "Template is valid"),
				))),
			))

			By("Creating absent required resources: HelmChart, HelmRepository")
			helmRepo := &sourcev1.HelmRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repository",
					Namespace: "default",
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
			Expect(k8sClient.Create(ctx, helmRepo)).To(Succeed())

			helmChart := &sourcev1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ref-test",
					Namespace: "default",
				},
				Spec: sourcev1.HelmChartSpec{
					Chart: "test",
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
			Expect(k8sClient.Create(ctx, helmChart)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).To(HaveOccurred())

			By("Patching ClusterTemplate status")
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(template), template)).Should(Succeed())
			template.Status.ChartRef = &hcv2.CrossNamespaceSourceReference{
				Kind:      "HelmChart",
				Name:      "ref-test",
				Namespace: "default",
			}
			Expect(k8sClient.Status().Update(ctx, template)).To(Succeed())

			helmChart.Status.URL = helmChartURL
			helmChart.Status.Artifact = &sourcev1.Artifact{
				URL:            helmChartURL,
				LastUpdateTime: metav1.Now(),
			}
			Expect(k8sClient.Status().Update(ctx, helmChart)).To(Succeed())

			// todo: next error occurs due to dependency on helm library. The best way to mitigate this is to
			//  inject an interface into the reconciler struct that can be mocked out for testing.
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).To(HaveOccurred())
		})
	})
})
