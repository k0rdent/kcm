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
	meta2 "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	clusterapiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
)

type fakeHelmActor struct{}

func (*fakeHelmActor) DownloadChartFromArtifact(_ context.Context, _ *sourcev1.Artifact) (*chart.Chart, error) {
	return &chart.Chart{
		Metadata: &chart.Metadata{
			APIVersion: "v2",
			Version:    "0.1.0",
			Name:       "test-cluster-chart",
		},
	}, nil
}

func (*fakeHelmActor) InitializeConfiguration(_ *kcm.ClusterDeployment, _ action.DebugLog) (*action.Configuration, error) {
	return &action.Configuration{}, nil
}

func (*fakeHelmActor) EnsureReleaseWithValues(_ context.Context, _ *action.Configuration, _ *chart.Chart, _ *kcm.ClusterDeployment) error {
	return nil
}

var _ = Describe("ClusterDeployment Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			helmChartURL = "http://source-controller.kcm-system.svc.cluster.local/helmchart/kcm-system/test-chart/0.1.0.tar.gz"
		)

		// resources required for ClusterDeployment reconciliation
		var (
			namespace                = corev1.Namespace{}
			secret                   = corev1.Secret{}
			awsCredential            = kcm.Credential{}
			clusterTemplate          = kcm.ClusterTemplate{}
			serviceTemplate          = kcm.ServiceTemplate{}
			helmRepo                 = sourcev1.HelmRepository{}
			clusterTemplateHelmChart = sourcev1.HelmChart{}
			serviceTemplateHelmChart = sourcev1.HelmChart{}

			clusterDeployment = kcm.ClusterDeployment{}

			cluster           = clusterapiv1beta1.Cluster{}
			machineDeployment = clusterapiv1beta1.MachineDeployment{}
			helmRelease       = hcv2.HelmRelease{}
		)

		BeforeEach(func() {
			By("ensure namespace", func() {
				namespace = corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-namespace-",
					},
				}
				Expect(k8sClient.Create(ctx, &namespace)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &namespace)
			})

			By("ensure HelmRepository resource", func() {
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
						URL:      "oci://kcm-local-registry:5000/charts",
					},
				}
				Expect(k8sClient.Create(ctx, &helmRepo)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &helmRepo)
			})

			By("ensure HelmChart resources", func() {
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
				DeferCleanup(k8sClient.Delete, &clusterTemplateHelmChart)

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
				DeferCleanup(k8sClient.Delete, &serviceTemplateHelmChart)
			})

			By("ensure ClusterTemplate resource", func() {
				clusterTemplate = kcm.ClusterTemplate{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-cluster-template-",
						Namespace:    namespace.Name,
					},
					Spec: kcm.ClusterTemplateSpec{
						Helm: kcm.HelmSpec{
							ChartRef: &hcv2.CrossNamespaceSourceReference{
								Kind:      "HelmChart",
								Name:      clusterTemplateHelmChart.Name,
								Namespace: namespace.Name,
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &clusterTemplate)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &clusterTemplate)

				clusterTemplate.Status = kcm.ClusterTemplateStatus{
					TemplateStatusCommon: kcm.TemplateStatusCommon{
						TemplateValidationStatus: kcm.TemplateValidationStatus{
							Valid: true,
						},
					},
					Providers: kcm.Providers{"infrastructure-aws"},
				}
				Expect(k8sClient.Status().Update(ctx, &clusterTemplate)).To(Succeed())
			})

			By("ensure ServiceTemplate resource", func() {
				serviceTemplate = kcm.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-service-template-",
						Namespace:    namespace.Name,
					},
					Spec: kcm.ServiceTemplateSpec{
						Helm: kcm.HelmSpec{
							ChartRef: &hcv2.CrossNamespaceSourceReference{
								Kind:      "HelmChart",
								Name:      serviceTemplateHelmChart.Name,
								Namespace: namespace.Name,
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &serviceTemplate)

				serviceTemplate.Status = kcm.ServiceTemplateStatus{
					TemplateStatusCommon: kcm.TemplateStatusCommon{
						ChartRef: &hcv2.CrossNamespaceSourceReference{
							Kind:      "HelmChart",
							Name:      serviceTemplateHelmChart.Name,
							Namespace: namespace.Name,
						},
						TemplateValidationStatus: kcm.TemplateValidationStatus{
							Valid: true,
						},
					},
				}
				Expect(k8sClient.Status().Update(ctx, &serviceTemplate)).To(Succeed())
			})

			By("ensure AWS Credential resource", func() {
				awsCredential = kcm.Credential{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-credential-aws-",
						Namespace:    namespace.Name,
					},
					Spec: kcm.CredentialSpec{
						IdentityRef: &corev1.ObjectReference{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
							Kind:       "AWSClusterStaticIdentity",
							Name:       "foo",
						},
					},
				}
				Expect(k8sClient.Create(ctx, &awsCredential)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &awsCredential)

				awsCredential.Status = kcm.CredentialStatus{
					Ready: true,
				}
				Expect(k8sClient.Status().Update(ctx, &awsCredential)).To(Succeed())
			})
		})

		AfterEach(func() {
			By("cleanup finalizer", func() {
				Expect(controllerutil.RemoveFinalizer(&clusterDeployment, kcm.ClusterDeploymentFinalizer)).To(BeTrue())
				Expect(k8sClient.Update(ctx, &clusterDeployment)).To(Succeed())
			})
		})

		It("should reconcile ClusterDeployment in dry-run mode", func() {
			controllerReconciler := &ClusterDeploymentReconciler{
				Client:    mgrClient,
				helmActor: &fakeHelmActor{},
				Config:    &rest.Config{},
			}

			By("creating ClusterDeployment resource", func() {
				clusterDeployment = kcm.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-cluster-deployment-",
						Namespace:    namespace.Name,
					},
					Spec: kcm.ClusterDeploymentSpec{
						Template:   clusterTemplate.Name,
						Credential: awsCredential.Name,
						DryRun:     true,
					},
				}
				Expect(k8sClient.Create(ctx, &clusterDeployment)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &clusterDeployment)
			})

			By("ensuring finalizer is added", func() {
				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: client.ObjectKeyFromObject(&clusterDeployment),
					})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(Object(&clusterDeployment)()).Should(SatisfyAll(
						HaveField("Finalizers", ContainElement(kcm.ClusterDeploymentFinalizer)),
					))
				}).Should(Succeed())
			})

			By("reconciling resource with finalizer", func() {
				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: client.ObjectKeyFromObject(&clusterDeployment),
					})
					g.Expect(err).To(HaveOccurred())
					g.Expect(Object(&clusterDeployment)()).Should(SatisfyAll(
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", kcm.TemplateReadyCondition),
							HaveField("Status", metav1.ConditionTrue),
							HaveField("Reason", kcm.SucceededReason),
							HaveField("Message", "Template is valid"),
						))),
					))
				}).Should(Succeed())
			})

			By("reconciling when dependencies are not in valid state", func() {
				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: client.ObjectKeyFromObject(&clusterDeployment),
					})
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring("helm chart source is not provided"))
				}).Should(Succeed())
			})

			By("patching ClusterTemplate and corresponding HelmChart statuses", func() {
				Expect(Get(&clusterTemplate)()).To(Succeed())
				clusterTemplate.Status.ChartRef = &hcv2.CrossNamespaceSourceReference{
					Kind:      "HelmChart",
					Name:      clusterTemplateHelmChart.Name,
					Namespace: namespace.Name,
				}
				Expect(k8sClient.Status().Update(ctx, &clusterTemplate)).To(Succeed())

				Expect(Get(&clusterTemplateHelmChart)()).To(Succeed())
				clusterTemplateHelmChart.Status.URL = helmChartURL
				clusterTemplateHelmChart.Status.Artifact = &sourcev1.Artifact{
					URL:            helmChartURL,
					LastUpdateTime: metav1.Now(),
				}
				Expect(k8sClient.Status().Update(ctx, &clusterTemplateHelmChart)).To(Succeed())

				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: client.ObjectKeyFromObject(&clusterDeployment),
					})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(Object(&clusterDeployment)()).Should(SatisfyAll(
						HaveField("Status.Conditions", ContainElements(
							SatisfyAll(
								HaveField("Type", kcm.HelmChartReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcm.SucceededReason),
								HaveField("Message", "Helm chart is valid"),
							),
							SatisfyAll(
								HaveField("Type", kcm.CredentialReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcm.SucceededReason),
								HaveField("Message", "Credential is Ready"),
							),
						))))
				}).Should(Succeed())
			})
		})

		It("should reconcile ClusterDeployment with AWS credentials", func() {
			controllerReconciler := &ClusterDeploymentReconciler{
				Client:        mgrClient,
				helmActor:     &fakeHelmActor{},
				Config:        &rest.Config{},
				DynamicClient: dynamicClient,
			}

			By("creating ClusterDeployment resource", func() {
				clusterDeployment = kcm.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-cluster-deployment-",
						Namespace:    namespace.Name,
					},
					Spec: kcm.ClusterDeploymentSpec{
						Template:   clusterTemplate.Name,
						Credential: awsCredential.Name,
						ServiceSpec: kcm.ServiceSpec{
							Services: []kcm.Service{
								{
									Template: serviceTemplate.Name,
									Name:     "test-service",
								},
							},
						},
						Config: &apiextensionsv1.JSON{
							Raw: []byte(`{"foo":"bar"}`),
						},
					},
				}
				Expect(k8sClient.Create(ctx, &clusterDeployment)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &clusterDeployment)
			})

			By("ensuring related resources exist", func() {
				Expect(Get(&clusterTemplate)()).To(Succeed())
				clusterTemplate.Status.ChartRef = &hcv2.CrossNamespaceSourceReference{
					Kind:      "HelmChart",
					Name:      clusterTemplateHelmChart.Name,
					Namespace: namespace.Name,
				}
				Expect(k8sClient.Status().Update(ctx, &clusterTemplate)).To(Succeed())

				Expect(Get(&clusterTemplateHelmChart)()).To(Succeed())
				clusterTemplateHelmChart.Status.URL = helmChartURL
				clusterTemplateHelmChart.Status.Artifact = &sourcev1.Artifact{
					URL:            helmChartURL,
					LastUpdateTime: metav1.Now(),
				}
				Expect(k8sClient.Status().Update(ctx, &clusterTemplateHelmChart)).To(Succeed())

				cluster = clusterapiv1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeployment.Name,
						Namespace: namespace.Name,
						Labels:    map[string]string{kcm.FluxHelmChartNameKey: clusterDeployment.Name},
					},
				}
				Expect(k8sClient.Create(ctx, &cluster)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &cluster)

				machineDeployment = clusterapiv1beta1.MachineDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeployment.Name + "-md",
						Namespace: namespace.Name,
						Labels:    map[string]string{kcm.FluxHelmChartNameKey: clusterDeployment.Name},
					},
					Spec: clusterapiv1beta1.MachineDeploymentSpec{
						ClusterName: cluster.Name,
						Template: clusterapiv1beta1.MachineTemplateSpec{
							Spec: clusterapiv1beta1.MachineSpec{
								ClusterName: cluster.Name,
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &machineDeployment)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &machineDeployment)

				secret = corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeployment.Name + "-kubeconfig",
						Namespace: namespace.Name,
					},
				}
				Expect(k8sClient.Create(ctx, &secret)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &secret)
			})

			By("ensuring conditions updates after reconciliation", func() {
				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: client.ObjectKeyFromObject(&clusterDeployment),
					})
					g.Expect(err).To(HaveOccurred())
					g.Expect(Object(&clusterDeployment)()).Should(SatisfyAll(
						HaveField("Finalizers", ContainElement(kcm.ClusterDeploymentFinalizer)),
						HaveField("Status.Conditions", ContainElements(
							SatisfyAll(
								HaveField("Type", kcm.TemplateReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcm.SucceededReason),
								HaveField("Message", "Template is valid"),
							),
							SatisfyAll(
								HaveField("Type", kcm.HelmChartReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcm.SucceededReason),
								HaveField("Message", "Helm chart is valid"),
							),
							SatisfyAll(
								HaveField("Type", kcm.CredentialReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcm.SucceededReason),
								HaveField("Message", "Credential is Ready"),
							),
						))))
				}).Should(Succeed())
			})

			By("ensuring related resources in proper state", func() {
				helmRelease = hcv2.HelmRelease{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeployment.Name,
						Namespace: namespace.Name,
					},
				}
				Expect(Get(&helmRelease)()).To(Succeed())
				meta.SetStatusCondition(&helmRelease.Status.Conditions, metav1.Condition{
					Type:               meta2.ReadyCondition,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             hcv2.InstallSucceededReason,
				})
				Expect(k8sClient.Status().Update(ctx, &helmRelease)).To(Succeed())

				Expect(Get(&cluster)()).To(Succeed())
				cluster.SetConditions([]clusterapiv1beta1.Condition{
					{
						Type:               clusterapiv1beta1.ControlPlaneInitializedCondition,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
					},
					{
						Type:               clusterapiv1beta1.ControlPlaneReadyCondition,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
					},
					{
						Type:               clusterapiv1beta1.InfrastructureReadyCondition,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
					},
				})
				Expect(k8sClient.Status().Update(ctx, &cluster)).To(Succeed())

				Expect(Get(&machineDeployment)()).To(Succeed())
				machineDeployment.SetConditions([]clusterapiv1beta1.Condition{
					{
						Type:               clusterapiv1beta1.MachineDeploymentAvailableCondition,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
					},
				})
				Expect(k8sClient.Status().Update(ctx, &machineDeployment)).To(Succeed())
			})

			By("ensuring ClusterDeployment is reconciled", func() {
				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: client.ObjectKeyFromObject(&clusterDeployment),
					})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(Object(&clusterDeployment)()).Should(SatisfyAll(
						HaveField("Status.Conditions", ContainElements(
							SatisfyAll(
								HaveField("Type", kcm.HelmReleaseReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", hcv2.InstallSucceededReason),
							),
							SatisfyAll(
								HaveField("Type", kcm.SveltosProfileReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcm.SucceededReason),
							),
							SatisfyAll(
								HaveField("Type", string(clusterapiv1beta1.ControlPlaneInitializedCondition)),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcm.SucceededReason),
							),
							SatisfyAll(
								HaveField("Type", string(clusterapiv1beta1.ControlPlaneReadyCondition)),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcm.SucceededReason),
							),
							SatisfyAll(
								HaveField("Type", string(clusterapiv1beta1.InfrastructureReadyCondition)),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcm.SucceededReason),
							),
							SatisfyAll(
								HaveField("Type", string(clusterapiv1beta1.MachineDeploymentAvailableCondition)),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcm.SucceededReason),
							),
							// TODO (#852 brongineer): add corresponding resources with expected state for successful reconciliation
							// SatisfyAll(
							// 	HaveField("Type", kcm.FetchServicesStatusSuccessCondition),
							// 	HaveField("Status", metav1.ConditionTrue),
							// 	HaveField("Reason", kcm.SucceededReason),
							// ),
							// SatisfyAll(
							// 	HaveField("Type", kcm.ReadyCondition),
							// 	HaveField("Status", metav1.ConditionTrue),
							// 	HaveField("Reason", kcm.SucceededReason),
							// ),
						))))
				}).Should(Succeed())
			})
		})

		// TODO (#852 brongineer): Add test for ClusterDeployment reconciliation with Azure credentials
		PIt("should reconcile ClusterDeployment with Azure credentials", func() {
			// TBD
		})

		// TODO (#852 brongineer): Add tests for ClusterDeployment reconciliation with other providers' credentials
		PIt("should reconcile ClusterDeployment with XXX credentials", func() {
			// TBD
		})

		// TODO (#852 brongineer): Add test for ClusterDeployment deletion
		PIt("should reconcile ClusterDeployment deletion", func() {
			// TBD
		})
	})
})
