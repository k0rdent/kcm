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

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
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

func (*fakeHelmActor) InitializeConfiguration(_ *kcmv1.ClusterDeployment, _ action.DebugLog) (*action.Configuration, error) {
	return &action.Configuration{}, nil
}

func (*fakeHelmActor) EnsureReleaseWithValues(_ context.Context, _ *action.Configuration, _ *chart.Chart, _ *kcmv1.ClusterDeployment) error {
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
			awsCredential            = kcmv1.Credential{}
			clusterTemplate          = kcmv1.ClusterTemplate{}
			serviceTemplate          = kcmv1.ServiceTemplate{}
			helmRepo                 = sourcev1.HelmRepository{}
			clusterTemplateHelmChart = sourcev1.HelmChart{}
			serviceTemplateHelmChart = sourcev1.HelmChart{}

			clusterDeployment = kcmv1.ClusterDeployment{}

			cluster           = clusterapiv1.Cluster{}
			machineDeployment = clusterapiv1.MachineDeployment{}
			helmRelease       = helmcontrollerv2.HelmRelease{}
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
				clusterTemplate = kcmv1.ClusterTemplate{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-cluster-template-",
						Namespace:    namespace.Name,
					},
					Spec: kcmv1.ClusterTemplateSpec{
						Helm: kcmv1.HelmSpec{
							ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
								Kind:      "HelmChart",
								Name:      clusterTemplateHelmChart.Name,
								Namespace: namespace.Name,
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &clusterTemplate)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &clusterTemplate)

				clusterTemplate.Status = kcmv1.ClusterTemplateStatus{
					TemplateStatusCommon: kcmv1.TemplateStatusCommon{
						TemplateValidationStatus: kcmv1.TemplateValidationStatus{
							Valid: true,
						},
					},
					Providers: kcmv1.Providers{"infrastructure-aws"},
				}
				Expect(k8sClient.Status().Update(ctx, &clusterTemplate)).To(Succeed())
			})

			By("ensure ServiceTemplate resource", func() {
				serviceTemplate = kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-service-template-",
						Namespace:    namespace.Name,
					},
					Spec: kcmv1.ServiceTemplateSpec{
						Helm: &kcmv1.HelmSpec{
							ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
								Kind:      "HelmChart",
								Name:      serviceTemplateHelmChart.Name,
								Namespace: namespace.Name,
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &serviceTemplate)

				serviceTemplate.Status = kcmv1.ServiceTemplateStatus{
					TemplateStatusCommon: kcmv1.TemplateStatusCommon{
						ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
							Kind:      "HelmChart",
							Name:      serviceTemplateHelmChart.Name,
							Namespace: namespace.Name,
						},
						TemplateValidationStatus: kcmv1.TemplateValidationStatus{
							Valid: true,
						},
					},
				}
				Expect(k8sClient.Status().Update(ctx, &serviceTemplate)).To(Succeed())
			})

			By("ensure AWS Credential resource", func() {
				awsCredential = kcmv1.Credential{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-credential-aws-",
						Namespace:    namespace.Name,
					},
					Spec: kcmv1.CredentialSpec{
						IdentityRef: &corev1.ObjectReference{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
							Kind:       "AWSClusterStaticIdentity",
							Name:       "foo",
						},
					},
				}
				Expect(k8sClient.Create(ctx, &awsCredential)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &awsCredential)

				awsCredential.Status = kcmv1.CredentialStatus{
					Ready: true,
				}
				Expect(k8sClient.Status().Update(ctx, &awsCredential)).To(Succeed())
			})
		})

		AfterEach(func() {
			By("cleanup finalizer", func() {
				Expect(controllerutil.RemoveFinalizer(&clusterDeployment, kcmv1.ClusterDeploymentFinalizer)).To(BeTrue())
				Expect(k8sClient.Update(ctx, &clusterDeployment)).To(Succeed())
			})
		})

		It("should reconcile ClusterDeployment in dry-run mode", func() {
			controllerReconciler := &ClusterDeploymentReconciler{
				MgmtClient: mgrClient,
				helmActor:  &fakeHelmActor{},
			}

			By("creating ClusterDeployment resource", func() {
				clusterDeployment = kcmv1.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-cluster-deployment-",
						Namespace:    namespace.Name,
					},
					Spec: kcmv1.ClusterDeploymentSpec{
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
						HaveField("Finalizers", ContainElement(kcmv1.ClusterDeploymentFinalizer)),
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
							HaveField("Type", kcmv1.TemplateReadyCondition),
							HaveField("Status", metav1.ConditionTrue),
							HaveField("Reason", kcmv1.SucceededReason),
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
				clusterTemplate.Status.ChartRef = &helmcontrollerv2.CrossNamespaceSourceReference{
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
								HaveField("Type", kcmv1.HelmChartReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcmv1.SucceededReason),
							),
							SatisfyAll(
								HaveField("Type", kcmv1.CredentialReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcmv1.SucceededReason),
							),
						))))
				}).Should(Succeed())
			})
		})

		It("should reconcile ClusterDeployment with AWS credentials", func() {
			controllerReconciler := &ClusterDeploymentReconciler{
				MgmtClient: mgrClient,
				helmActor:  &fakeHelmActor{},
			}

			By("creating ClusterDeployment resource", func() {
				clusterDeployment = kcmv1.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-cluster-deployment-",
						Namespace:    namespace.Name,
					},
					Spec: kcmv1.ClusterDeploymentSpec{
						Template:   clusterTemplate.Name,
						Credential: awsCredential.Name,
						ServiceSpec: kcmv1.ServiceSpec{
							Provider: kcmv1.StateManagementProviderConfig{
								Name: kubeutil.DefaultStateManagementProvider,
							},
							Services: []kcmv1.Service{
								{
									Template: serviceTemplate.Name,
									Name:     "test-service",
								},
							},
						},
						Config: &apiextv1.JSON{
							Raw: []byte(`{"foo":"bar"}`),
						},
					},
				}
				Expect(k8sClient.Create(ctx, &clusterDeployment)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &clusterDeployment)
			})

			By("ensuring related resources exist", func() {
				Expect(Get(&clusterTemplate)()).To(Succeed())
				clusterTemplate.Status.ChartRef = &helmcontrollerv2.CrossNamespaceSourceReference{
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

				cluster = clusterapiv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeployment.Name,
						Namespace: namespace.Name,
						Labels:    map[string]string{kcmv1.FluxHelmChartNameKey: clusterDeployment.Name},
					},
				}
				Expect(k8sClient.Create(ctx, &cluster)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &cluster)

				machineDeployment = clusterapiv1.MachineDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeployment.Name + "-md",
						Namespace: namespace.Name,
						Labels:    map[string]string{kcmv1.FluxHelmChartNameKey: clusterDeployment.Name},
					},
					Spec: clusterapiv1.MachineDeploymentSpec{
						ClusterName: cluster.Name,
						Template: clusterapiv1.MachineTemplateSpec{
							Spec: clusterapiv1.MachineSpec{
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
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(Object(&clusterDeployment)()).Should(SatisfyAll(
						HaveField("Finalizers", ContainElement(kcmv1.ClusterDeploymentFinalizer)),
						HaveField("Status.Conditions", ContainElements(
							SatisfyAll(
								HaveField("Type", kcmv1.TemplateReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcmv1.SucceededReason),
							),
							SatisfyAll(
								HaveField("Type", kcmv1.HelmChartReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcmv1.SucceededReason),
							),
							SatisfyAll(
								HaveField("Type", kcmv1.CredentialReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", kcmv1.SucceededReason),
							),
							SatisfyAll(
								HaveField("Type", kcmv1.CAPIClusterSummaryCondition),
								HaveField("Status", metav1.ConditionUnknown),
								HaveField("Reason", "UnknownReported"),
								HaveField("Message", "* InfrastructureReady: Condition not yet reported\n* ControlPlaneInitialized: Condition not yet reported\n* ControlPlaneAvailable: Condition not yet reported\n* ControlPlaneMachinesReady: Condition not yet reported\n* WorkersAvailable: Condition not yet reported\n* WorkerMachinesReady: Condition not yet reported\n* RemoteConnectionProbe: Condition not yet reported"),
							),
						))))
				}).Should(Succeed())
			})

			By("ensuring related resources in proper state", func() {
				helmRelease = helmcontrollerv2.HelmRelease{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeployment.Name,
						Namespace: namespace.Name,
					},
				}
				Expect(Get(&helmRelease)()).To(Succeed())
				meta.SetStatusCondition(&helmRelease.Status.Conditions, metav1.Condition{
					Type:               fluxmeta.ReadyCondition,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             helmcontrollerv2.InstallSucceededReason,
				})
				Expect(k8sClient.Status().Update(ctx, &helmRelease)).To(Succeed())

				Expect(Get(&cluster)()).To(Succeed())
				cluster.SetConditions([]metav1.Condition{
					{
						Type:               string(clusterapiv1.ControlPlaneInitializedV1Beta1Condition),
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
					},
					{
						Type:               string(clusterapiv1.ControlPlaneReadyV1Beta1Condition),
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
					},
					{
						Type:               clusterapiv1.InfrastructureReadyCondition,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
					},
				})
				Expect(k8sClient.Status().Update(ctx, &cluster)).To(Succeed())

				Expect(Get(&machineDeployment)()).To(Succeed())
				machineDeployment.SetConditions([]metav1.Condition{
					{
						Type:               clusterapiv1.MachineDeploymentAvailableCondition,
						Status:             metav1.ConditionTrue,
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
								HaveField("Type", kcmv1.HelmReleaseReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", helmcontrollerv2.InstallSucceededReason),
							),
							SatisfyAll(
								HaveField("Type", kcmv1.CAPIClusterSummaryCondition),
								HaveField("Status", metav1.ConditionUnknown),
								HaveField("Reason", "UnknownReported"),
								HaveField("Message", "* InfrastructureReady: Condition not yet reported\n* ControlPlaneInitialized: Condition not yet reported\n* ControlPlaneAvailable: Condition not yet reported\n* ControlPlaneMachinesReady: Condition not yet reported\n* WorkersAvailable: Condition not yet reported\n* WorkerMachinesReady: Condition not yet reported\n* RemoteConnectionProbe: Condition not yet reported"),
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
