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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/cluster-api/api/v1beta1"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hmc "github.com/K0rdent/kcm/api/v1alpha1"
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

func (*fakeHelmActor) InitializeConfiguration(_ *hmc.ClusterDeployment, _ action.DebugLog) (*action.Configuration, error) {
	return &action.Configuration{}, nil
}

func (*fakeHelmActor) EnsureReleaseWithValues(_ context.Context, _ *action.Configuration, _ *chart.Chart, _ *hmc.ClusterDeployment) error {
	return nil
}

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
			By("ensure namespace", func() {
				namespace = corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-namespace-",
					},
				}
				Expect(k8sClient.Create(ctx, &namespace)).To(Succeed())
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
						URL:      "oci://hmc-local-registry:5000/charts",
					},
				}
				Expect(k8sClient.Create(ctx, &helmRepo)).To(Succeed())
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

			By("ensure ClusterTemplate resource", func() {
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
					},
					Providers: hmc.Providers{"infrastructure-aws"},
				}
				Expect(k8sClient.Status().Update(ctx, &clusterTemplate)).To(Succeed())
			})

			By("ensure ServiceTemplate resource", func() {
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

			By("ensure Credential resource", func() {
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
			By("cleanup", func() {
				controllerReconciler := &ClusterDeploymentReconciler{
					Client:    mgrClient,
					HelmActor: &fakeHelmActor{},
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

		It("should successfully dry-run reconciliation", func() {
			controllerReconciler := &ClusterDeploymentReconciler{
				Client:    mgrClient,
				HelmActor: &fakeHelmActor{},
				Config:    &rest.Config{},
			}

			By("patching ClusterDeployment resource to dry-run mode", func() {
				clusterDeployment.Spec.DryRun = true
				Expect(k8sClient.Update(ctx, &clusterDeployment)).To(Succeed())
			})

			By("ensuring finalizer is added", func() {
				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: clusterDeploymentKey,
					})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(Object(&clusterDeployment)()).Should(SatisfyAll(
						HaveField("Finalizers", ContainElement(hmc.ClusterDeploymentFinalizer)),
					))
				}).Should(Succeed())
			})

			By("reconciling resource with finalizer", func() {
				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: clusterDeploymentKey,
					})
					g.Expect(err).To(HaveOccurred())
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

			By("reconciling when dependencies are not in valid state", func() {
				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: clusterDeploymentKey,
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
						NamespacedName: clusterDeploymentKey,
					})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(Object(&clusterDeployment)()).Should(SatisfyAll(
						HaveField("Status.Conditions", ContainElements(
							SatisfyAll(
								HaveField("Type", hmc.HelmChartReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", hmc.SucceededReason),
								HaveField("Message", "Helm chart is valid"),
							),
							SatisfyAll(
								HaveField("Type", hmc.CredentialReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", hmc.SucceededReason),
								HaveField("Message", "Credential is Ready"),
							),
						))))
				}).Should(Succeed())
			})
		})

		// todo: Cluster and MachineDeployment resources creation fails with "no matches for kind",
		//  need to install CRDs and add to scheme. Until then the test is disabled.
		PIt("should successfully finish reconciliation", func() {
			controllerReconciler := &ClusterDeploymentReconciler{
				Client:    mgrClient,
				HelmActor: &fakeHelmActor{},
				Config:    &rest.Config{},
			}

			By("Ensuring related resources are in proper state", func() {
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

				helmRelease := &hcv2.HelmRelease{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeployment.Name,
						Namespace: namespace.Name,
					},
				}
				Expect(Get(helmRelease)()).To(Succeed())
				meta.SetStatusCondition(&helmRelease.Status.Conditions, metav1.Condition{
					Type:               meta2.ReadyCondition,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             hcv2.InstallSucceededReason,
				})
				Expect(k8sClient.Status().Update(ctx, helmRelease)).To(Succeed())

				cluster := v1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeployment.Name,
						Namespace: namespace.Name,
						Labels:    map[string]string{hmc.FluxHelmChartNameKey: clusterDeployment.Name},
					},
				}
				Expect(k8sClient.Create(ctx, &cluster)).To(Succeed())

				machineDeployment := v1beta1.MachineDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeployment.Name + "-md",
						Namespace: namespace.Name,
						Labels:    map[string]string{hmc.FluxHelmChartNameKey: clusterDeployment.Name},
					},
				}
				Expect(k8sClient.Create(ctx, &machineDeployment)).To(Succeed())
			})

			By("ensuring reconciliation finished", func() {
				Eventually(func(g Gomega) {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: clusterDeploymentKey,
					})
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(Object(&clusterDeployment)()).Should(SatisfyAll(
						HaveField("Finalizers", ContainElement(hmc.ClusterDeploymentFinalizer)),
						HaveField("Status.Conditions", ContainElements(
							SatisfyAll(
								HaveField("Type", hmc.TemplateReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", hmc.SucceededReason),
								HaveField("Message", "Template is valid"),
							),
							SatisfyAll(
								HaveField("Type", hmc.HelmChartReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", hmc.SucceededReason),
								HaveField("Message", "Helm chart is valid"),
							),
							SatisfyAll(
								HaveField("Type", hmc.CredentialReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", hmc.SucceededReason),
								HaveField("Message", "Credential is Ready"),
							),
							SatisfyAll(
								HaveField("Type", hmc.HelmReleaseReadyCondition),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", hmc.SucceededReason),
							),
						))))
				}).Should(Succeed())
			})
		})
	})
})
