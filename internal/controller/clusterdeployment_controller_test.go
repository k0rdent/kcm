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
	"errors"
	"fmt"
	"testing"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	conditionsutil "github.com/K0rdent/kcm/internal/util/conditions"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

type fakeHelmActor struct{}

func (*fakeHelmActor) DownloadChartFromArtifact(_ context.Context, _ *fluxmeta.Artifact) (*chart.Chart, error) {
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
				clusterTemplateHelmChart.Status.Artifact = &fluxmeta.Artifact{
					URL:            helmChartURL,
					LastUpdateTime: metav1.Now(),
					Digest:         "some:digest", // just to pass validation
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
				clusterTemplateHelmChart.Status.Artifact = &fluxmeta.Artifact{
					URL:            helmChartURL,
					LastUpdateTime: metav1.Now(),
					Digest:         "some:digest", // just to pass validation
				}
				Expect(k8sClient.Status().Update(ctx, &clusterTemplateHelmChart)).To(Succeed())

				cluster = clusterapiv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeployment.Name,
						Namespace: namespace.Name,
						Labels:    map[string]string{kcmv1.FluxHelmChartNameKey: clusterDeployment.Name},
					},
					Spec: clusterapiv1.ClusterSpec{Paused: new(false)}, // just to pass validation
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
								Bootstrap: clusterapiv1.Bootstrap{
									DataSecretName: new("dummy"), // just to pass validation
								},
								InfrastructureRef: clusterapiv1.ContractVersionedObjectReference{
									APIGroup: "infrastructure.cluster.x-k8s.io",
									Kind:     "AWSMachineTemplate",
									Name:     "dummy", // just to pass validation
								},
							},
						},
						Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "dummy"}}, // just to pass validation
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
				apimeta.SetStatusCondition(&helmRelease.Status.Conditions, metav1.Condition{
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
						Message:            "Dummy",
						Reason:             clusterapiv1.ReadyReason,
						LastTransitionTime: metav1.Now(),
					},
					{
						Type:               string(clusterapiv1.ControlPlaneReadyV1Beta1Condition),
						Status:             metav1.ConditionTrue,
						Message:            "Dummy",
						Reason:             clusterapiv1.ReadyReason,
						LastTransitionTime: metav1.Now(),
					},
					{
						Type:               clusterapiv1.InfrastructureReadyCondition,
						Status:             metav1.ConditionTrue,
						Message:            "Dummy",
						Reason:             clusterapiv1.ReadyReason,
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
						Message:            "Dummy",
						Reason:             clusterapiv1.ReadyReason,
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
								HaveField("Message", "* ControlPlaneAvailable: Condition not yet reported\n* ControlPlaneMachinesReady: Condition not yet reported\n* WorkersAvailable: Condition not yet reported\n* WorkerMachinesReady: Condition not yet reported\n* RemoteConnectionProbe: Condition not yet reported"),
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

func Test_updateClusterDeploymentConditions(t *testing.T) {
	const generation = 2
	type testInput struct {
		name                   string
		currentConditions      []metav1.Condition
		expectedReadyCondition metav1.Condition
	}
	tests := []testInput{
		{
			name: "No conditions exist; should add Ready condition with Unknown status.",
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionUnknown,
				Reason:             kcmv1.ProgressingReason,
				ObservedGeneration: generation,
			},
		},
		{
			name: "Some conditions exist with Unknown status; should add Ready condition with Unknown status.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.CredentialReadyCondition,
					Status:  metav1.ConditionUnknown,
					Reason:  "CredentialStatusUnknown",
					Message: "Credential status is unknown",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionUnknown,
				Reason:             kcmv1.ProgressingReason,
				Message:            "Credential status is unknown",
				ObservedGeneration: generation,
			},
		},
		{
			name: "Credential and Template are not Ready; should reflect both errors in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.CredentialReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  "SomeCredentialError",
					Message: "Some error with credentials",
				},
				{
					Type:    kcmv1.TemplateReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  "SomeTemplateError",
					Message: "Some error with cluster template",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.FailedReason,
				Message:            "Some error with credentials. Some error with cluster template",
				ObservedGeneration: generation,
			},
		},
		{
			name: "ClusterDataSource and ClusterIdentity are not Ready; should reflect both errors in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.CredentialReadyCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.SucceededReason,
					Message: "Credential is ready",
				},
				{
					Type:    kcmv1.TemplateReadyCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.SucceededReason,
					Message: "ClusterTemplate is ready",
				},
				{
					Type:    kcmv1.ClusterDataSourceReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  kcmv1.FailedReason,
					Message: "Some error with cluster data source",
				},
				{
					Type:    kcmv1.ClusterAuthenticationReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  kcmv1.FailedReason,
					Message: "Some error with cluster authentication",
				},
				{
					Type:               kcmv1.CAPIClusterSummaryCondition,
					Status:             metav1.ConditionFalse,
					Reason:             "IssuesReported",
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-5 * time.Minute)},
					Message:            "InfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.FailedReason,
				Message:            "Some error with cluster data source. Some error with cluster authentication",
				ObservedGeneration: generation,
			},
		},
		{
			name: "CAPI Cluster is provisioning for 5 minutes; should reflect Progressing reason in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.ClusterAuthenticationReadyCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.SucceededReason,
					Message: "ClusterAuthentication is ready",
				},
				{
					Type:               kcmv1.CAPIClusterSummaryCondition,
					Status:             metav1.ConditionFalse,
					Reason:             "IssuesReported",
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-5 * time.Minute)},
					Message:            "InfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.ProgressingReason,
				Message:            "InfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				ObservedGeneration: generation,
			},
		},
		{
			name: "CAPI Cluster is provisioning for more than 30 minutes; should reflect Failed reason in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:               kcmv1.CAPIClusterSummaryCondition,
					Status:             metav1.ConditionFalse,
					Reason:             "IssuesReported",
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-40 * time.Minute)},
					Message:            "InfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.FailedReason,
				Message:            "Cluster is not ready. Check the provider logs for more details.\nInfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				ObservedGeneration: generation,
			},
		},
		{
			name: "Cluster is ready, should reflect Succeeded reason in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.CAPIClusterSummaryCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.SucceededReason,
					Message: "Cluster is ready",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionTrue,
				Reason:             kcmv1.SucceededReason,
				Message:            "Object is ready",
				ObservedGeneration: generation,
			},
		},
		{
			name: "Cluster is deleting, Ready condition should be equal to Deleting condition",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.DeletingCondition,
					Status:  metav1.ConditionTrue,
					Reason:  "IssuesReported",
					Message: "Some error with cluster deletion",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.DeletingReason,
				Message:            "Some error with cluster deletion",
				ObservedGeneration: generation,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := conditionsutil.UpdateReadyCondition(tt.currentConditions, generation, handleClusterDeploymentFailedConditions)
			checkReadyCondition(t, result, tt.expectedReadyCondition)
		})
	}
}

// checkReadyCondition verifies if the Ready condition in the list of all conditions matches the expected Ready condition.
func checkReadyCondition(t *testing.T, conditions []metav1.Condition, expectedReadyCondition metav1.Condition) {
	t.Helper()

	for _, cond := range conditions {
		if cond.Type == kcmv1.ReadyCondition {
			if cond.Status != expectedReadyCondition.Status || cond.Reason != expectedReadyCondition.Reason ||
				cond.Message != expectedReadyCondition.Message || cond.ObservedGeneration != expectedReadyCondition.ObservedGeneration {
				printCondition := func(c metav1.Condition) string {
					return fmt.Sprintf("{Status: %s, Reason: %s, Message: %s, ObservedGeneration: %d}", c.Status, c.Reason, c.Message, c.ObservedGeneration)
				}
				t.Errorf("Ready condition does not match expected.\nGot: %s,\nWant: %s", printCondition(cond), printCondition(expectedReadyCondition))
			}
			return
		}
	}
	t.Errorf("Ready condition not found")
}

func Test_detectHelmChartNameChange(t *testing.T) {
	const (
		cdName      = "test-cd"
		cdNamespace = "default"
	)

	tests := []struct {
		name                  string
		existingHelmRelease   *helmcontrollerv2.HelmRelease
		templateChartRef      *helmcontrollerv2.CrossNamespaceSourceReference
		clientInterceptor     *interceptor.Funcs
		expectConditionSet    bool
		expectConditionStatus metav1.ConditionStatus
		preExistingCondition  bool // whether to pre-set the HelmChartNameChanged condition
		expectError           bool
	}{
		{
			name:                "no existing HelmRelease, no condition set",
			existingHelmRelease: nil,
			templateChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind: "HelmChart",
				Name: "some-chart",
			},
			expectConditionSet: false,
		},
		{
			name: "chart name unchanged, no condition set",
			existingHelmRelease: &helmcontrollerv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
				Spec: helmcontrollerv2.HelmReleaseSpec{
					ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
						Kind: "HelmChart",
						Name: "same-chart",
					},
				},
			},
			templateChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind: "HelmChart",
				Name: "same-chart",
			},
			expectConditionSet: false,
		},
		{
			name: "chart name changed, condition should be set",
			existingHelmRelease: &helmcontrollerv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
				Spec: helmcontrollerv2.HelmReleaseSpec{
					ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
						Kind: "HelmChart",
						Name: "old-chart",
					},
				},
			},
			templateChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind: "HelmChart",
				Name: "new-chart",
			},
			expectConditionSet:    true,
			expectConditionStatus: metav1.ConditionTrue,
		},
		{
			name: "existing HelmRelease has nil ChartRef, no condition set",
			existingHelmRelease: &helmcontrollerv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
				Spec: helmcontrollerv2.HelmReleaseSpec{},
			},
			templateChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind: "HelmChart",
				Name: "new-chart",
			},
			expectConditionSet: false,
		},
		{
			name:             "template has nil ChartRef, method returns early",
			templateChartRef: nil,
			existingHelmRelease: &helmcontrollerv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
				Spec: helmcontrollerv2.HelmReleaseSpec{
					ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
						Kind: "HelmChart",
						Name: "old-chart",
					},
				},
			},
			expectConditionSet: false,
		},
		{
			name: "chart name back to same, pre-existing condition should be removed",
			existingHelmRelease: &helmcontrollerv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
				Spec: helmcontrollerv2.HelmReleaseSpec{
					ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
						Kind: "HelmChart",
						Name: "same-chart",
					},
				},
			},
			templateChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind: "HelmChart",
				Name: "same-chart",
			},
			preExistingCondition: true,
			expectConditionSet:   false,
		},
		{
			name: "transient Get error should return error",
			clientInterceptor: &interceptor.Funcs{
				Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
					if _, ok := obj.(*helmcontrollerv2.HelmRelease); ok {
						return errors.New("transient API server error")
					}
					return nil
				},
			},
			templateChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind: "HelmChart",
				Name: "some-chart",
			},
			expectConditionSet: false,
			expectError:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var existingObjects []runtime.Object
			if tt.existingHelmRelease != nil {
				existingObjects = append(existingObjects, tt.existingHelmRelease)
			}

			clientBuilder := fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithRuntimeObjects(existingObjects...)
			if tt.clientInterceptor != nil {
				clientBuilder = clientBuilder.WithInterceptorFuncs(*tt.clientInterceptor)
			}
			c := clientBuilder.Build()

			cd := &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
			}

			if tt.preExistingCondition {
				apimeta.SetStatusCondition(&cd.Status.Conditions, metav1.Condition{
					Type:    kcmv1.HelmChartNameChangedCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.HelmChartNameChangedReason,
					Message: "previously set",
				})
			}

			clusterTpl := &kcmv1.ClusterTemplate{
				Status: kcmv1.ClusterTemplateStatus{
					TemplateStatusCommon: kcmv1.TemplateStatusCommon{
						ChartRef: tt.templateChartRef,
					},
				},
			}

			r := &ClusterDeploymentReconciler{
				MgmtClient: c,
			}

			err := r.detectHelmChartNameChange(t.Context(), cd, clusterTpl)
			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			cond := apimeta.FindStatusCondition(cd.Status.Conditions, kcmv1.HelmChartNameChangedCondition)
			if tt.expectConditionSet {
				if cond == nil {
					t.Fatalf("expected %s condition to be set, but it was not", kcmv1.HelmChartNameChangedCondition)
				}
				if cond.Status != tt.expectConditionStatus {
					t.Errorf("expected condition status %s, got %s", tt.expectConditionStatus, cond.Status)
				}
				if cond.Reason != kcmv1.HelmChartNameChangedReason {
					t.Errorf("expected condition reason %s, got %s", kcmv1.HelmChartNameChangedReason, cond.Reason)
				}
			} else if cond != nil {
				t.Errorf("expected %s condition to not be set, but got: %+v", kcmv1.HelmChartNameChangedCondition, cond)
			}
		})
	}
}

func Test_projectCredentials(t *testing.T) {
	baseCred := func() *kcmv1.Credential {
		return &kcmv1.Credential{
			ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns"},
			Spec: kcmv1.CredentialSpec{
				IdentityRef: &corev1.ObjectReference{
					APIVersion: "v1", Kind: "Secret", Name: "id-secret", Namespace: "ns",
				},
				ProjectionConfig: &kcmv1.CredentialProjectionConfig{
					ResourceTemplateRef: &corev1.LocalObjectReference{Name: "tpl-cm"},
				},
			},
		}
	}

	baseCD := func() *kcmv1.ClusterDeployment {
		return &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "cd", Namespace: "ns"},
			Spec: kcmv1.ClusterDeploymentSpec{
				PropagateCredentials: new(true),
				Template:             "tpl",
			},
		}
	}

	tests := []struct {
		name           string
		cd             *kcmv1.ClusterDeployment
		cred           *kcmv1.Credential
		rgnObjects     []client.Object
		wantErr        string
		wantCondStatus metav1.ConditionStatus
	}{
		{
			name: "PropagateCredentials nil sets condition true",
			cd: func() *kcmv1.ClusterDeployment {
				cd := baseCD()
				cd.Spec.PropagateCredentials = nil
				return cd
			}(),
			cred:           baseCred(),
			wantCondStatus: metav1.ConditionTrue,
		},
		{
			name: "PropagateCredentials false sets condition true",
			cd: func() *kcmv1.ClusterDeployment {
				cd := baseCD()
				cd.Spec.PropagateCredentials = new(false)
				return cd
			}(),
			cred:           baseCred(),
			wantCondStatus: metav1.ConditionTrue,
		},
		{
			name: "ProjectionConfig nil sets condition true",
			cd:   baseCD(),
			cred: func() *kcmv1.Credential {
				c := baseCred()
				c.Spec.ProjectionConfig = nil
				return c
			}(),
			wantCondStatus: metav1.ConditionTrue,
		},
		{
			name:           "CAPI Cluster not found returns nil for retry",
			cd:             baseCD(),
			cred:           baseCred(),
			rgnObjects:     nil, // no cluster in regional client
			wantCondStatus: metav1.ConditionFalse,
		},
		{
			name: "kubeconfig secret not found returns nil for retry",
			cd:   baseCD(),
			cred: baseCred(),
			rgnObjects: []client.Object{
				&clusterapiv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{Name: "cd", Namespace: "ns"},
					// no InfrastructureRef
				},
				// no kubeconfig secret, GetChildClient returns NotFound
			},
			wantCondStatus: metav1.ConditionFalse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rgnClient := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(tt.rgnObjects...).Build()

			r := &ClusterDeploymentReconciler{
				MgmtClient: fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build(),
			}
			scope := &clusterScope{
				cd:        tt.cd,
				cred:      tt.cred,
				rgnClient: rgnClient,
			}

			err := r.projectCredentials(t.Context(), scope)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			if tt.wantCondStatus != "" {
				cond := apimeta.FindStatusCondition(tt.cd.Status.Conditions, kcmv1.CredentialsProjectedCondition)
				require.NotNil(t, cond, "expected CredentialsProjected condition to be set")
				assert.Equal(t, tt.wantCondStatus, cond.Status)
			}
		})
	}
}
