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
	"crypto/sha256"
	"fmt"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"helm.sh/helm/v3/pkg/chart"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

var _ = Describe("MultiClusterService Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			serviceTemplate1Name    = "test-service-1-v0-1-0"
			serviceTemplate2Name    = "test-service-2-v0-1-0"
			serviceTemplate3Name    = "test-service-3-v0-1-0"
			helmRepoName            = "test-helmrepo"
			helmChartName           = "test-helmchart"
			helmChartReleaseName    = "test-helmchart-release"
			helmChartVersion        = "0.1.0"
			helmChartURL            = "http://source-controller.kcm-system.svc.cluster.local./helmchart/kcm-system/test-chart/0.1.0.tar.gz"
			multiClusterServiceName = "test-multiclusterservice"
			clusterDeploymentName   = "test-clusterdeployment"
		)

		fakeDownloadHelmChartFunc := func(_ context.Context, _, _ string) (*chart.Chart, error) {
			return &chart.Chart{
				Metadata: &chart.Metadata{
					APIVersion: "v2",
					Version:    helmChartVersion,
					Name:       helmChartName,
				},
			}, nil
		}

		namespace := &corev1.Namespace{}
		helmChart := &sourcev1.HelmChart{}
		helmRepo := &sourcev1.HelmRepository{}
		serviceTemplate := &kcmv1.ServiceTemplate{}
		serviceTemplate2 := &kcmv1.ServiceTemplate{}
		serviceTemplate3 := &kcmv1.ServiceTemplate{}
		multiClusterService := &kcmv1.MultiClusterService{}
		clusterDeployment := kcmv1.ClusterDeployment{}
		serviceSet := kcmv1.ServiceSet{}
		mgmtServiceSet := kcmv1.ServiceSet{}

		helmRepositoryRef := types.NamespacedName{Namespace: testSystemNamespace, Name: helmRepoName}
		helmChartRef := types.NamespacedName{Namespace: testSystemNamespace, Name: helmChartName}
		serviceTemplate1Ref := types.NamespacedName{Namespace: testSystemNamespace, Name: serviceTemplate1Name}
		serviceTemplate2Ref := types.NamespacedName{Namespace: testSystemNamespace, Name: serviceTemplate2Name}
		serviceTemplate3Ref := types.NamespacedName{Namespace: testSystemNamespace, Name: serviceTemplate3Name}
		multiClusterServiceRef := types.NamespacedName{Name: multiClusterServiceName}
		serviceSetKey := types.NamespacedName{}
		mgmtServiceSetKey := types.NamespacedName{}

		BeforeEach(func() {
			By("creating Namespace")
			err := k8sClient.Get(ctx, types.NamespacedName{Name: testSystemNamespace}, namespace)
			if err != nil && apierrors.IsNotFound(err) {
				namespace = &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testSystemNamespace,
					},
				}
				Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			}

			By("creating HelmRepository")
			err = k8sClient.Get(ctx, types.NamespacedName{Name: helmRepoName, Namespace: testSystemNamespace}, helmRepo)
			if err != nil && apierrors.IsNotFound(err) {
				helmRepo = &sourcev1.HelmRepository{
					ObjectMeta: metav1.ObjectMeta{
						Name:      helmRepoName,
						Namespace: testSystemNamespace,
					},
					Spec: sourcev1.HelmRepositorySpec{
						URL: "oci://test/helmrepo",
					},
				}
				Expect(k8sClient.Create(ctx, helmRepo)).To(Succeed())
			}

			By("creating HelmChart")
			err = k8sClient.Get(ctx, types.NamespacedName{Name: helmChartName, Namespace: testSystemNamespace}, helmChart)
			if err != nil && apierrors.IsNotFound(err) {
				helmChart = &sourcev1.HelmChart{
					ObjectMeta: metav1.ObjectMeta{
						Name:      helmChartName,
						Namespace: testSystemNamespace,
					},
					Spec: sourcev1.HelmChartSpec{
						Chart:   helmChartName,
						Version: helmChartVersion,
						SourceRef: sourcev1.LocalHelmChartSourceReference{
							Kind: sourcev1.HelmRepositoryKind,
							Name: helmRepoName,
						},
					},
				}
				Expect(k8sClient.Create(ctx, helmChart)).To(Succeed())
			}

			By("updating HelmChart status with artifact URL")
			helmChart.Status.URL = helmChartURL
			helmChart.Status.Artifact = &fluxmeta.Artifact{
				URL:            helmChartURL,
				LastUpdateTime: metav1.Now(),
				Digest:         "some:digest", // just to pass validation
			}
			Expect(k8sClient.Status().Update(ctx, helmChart)).Should(Succeed())

			By("creating ServiceTemplate1 with chartRef set in .spec")
			err = k8sClient.Get(ctx, serviceTemplate1Ref, serviceTemplate)
			if err != nil && apierrors.IsNotFound(err) {
				serviceTemplate = &kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      serviceTemplate1Name,
						Namespace: testSystemNamespace,
						Labels: map[string]string{
							kcmv1.KCMManagedLabelKey:        "true",
							kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM,
						},
					},
					Spec: kcmv1.ServiceTemplateSpec{
						Helm: &kcmv1.HelmSpec{
							ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
								Kind:      "HelmChart",
								Name:      helmChartName,
								Namespace: testSystemNamespace,
							},
						},
					},
				}
			}
			Expect(k8sClient.Create(ctx, serviceTemplate)).To(Succeed())

			By("creating ServiceTemplate2 with chartRef set in .status")
			err = k8sClient.Get(ctx, serviceTemplate2Ref, serviceTemplate2)
			if err != nil && apierrors.IsNotFound(err) {
				serviceTemplate2 = &kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      serviceTemplate2Name,
						Namespace: testSystemNamespace,
						Labels:    map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
					},
					Spec: kcmv1.ServiceTemplateSpec{
						Helm: &kcmv1.HelmSpec{
							ChartSpec: &sourcev1.HelmChartSpec{
								Chart:   helmChartName,
								Version: helmChartVersion,
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, serviceTemplate2)).To(Succeed())
				serviceTemplate2.Status = kcmv1.ServiceTemplateStatus{
					TemplateStatusCommon: kcmv1.TemplateStatusCommon{
						ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
							Kind:      "HelmChart",
							Name:      helmChartName,
							Namespace: testSystemNamespace,
						},
						TemplateValidationStatus: kcmv1.TemplateValidationStatus{
							Valid: true,
						},
					},
				}
				Expect(k8sClient.Status().Update(ctx, serviceTemplate2)).To(Succeed())
			}

			By("creating ServiceTemplate3 with Resources+LocalSourceRef (ConfigMap) and no version")
			err = k8sClient.Get(ctx, serviceTemplate3Ref, serviceTemplate3)
			if err != nil && apierrors.IsNotFound(err) {
				serviceTemplate3 = &kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      serviceTemplate3Name,
						Namespace: testSystemNamespace,
						Labels:    map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
					},
					Spec: kcmv1.ServiceTemplateSpec{
						// Resources-only template with a ConfigMap-backed local source
						// and NO Spec.Version — exercises the "no values, no versions"
						// path where fillService*Versions falls back to the
						// template name as the effective version.
						Resources: &kcmv1.SourceSpec{
							DeploymentType: "Local",
							LocalSourceRef: &kcmv1.LocalSourceRef{
								Kind: "ConfigMap",
								Name: "manifests",
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, serviceTemplate3)).To(Succeed())
				serviceTemplate3.Status = kcmv1.ServiceTemplateStatus{
					TemplateStatusCommon: kcmv1.TemplateStatusCommon{
						TemplateValidationStatus: kcmv1.TemplateValidationStatus{
							Valid: true,
						},
					},
				}
				Expect(k8sClient.Status().Update(ctx, serviceTemplate3)).To(Succeed())
			}

			By("creating ClusterDeployment resource", func() {
				clusterDeployment = kcmv1.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: clusterDeploymentName + "-",
						Namespace:    namespace.Name,
						Labels: map[string]string{
							"test": "true",
						},
					},
					Spec: kcmv1.ClusterDeploymentSpec{
						Template:   "sample-template",
						Credential: "sample-credential",
						Config: &apiextv1.JSON{
							Raw: []byte(`{"foo":"bar"}`),
						},
					},
				}
				Expect(k8sClient.Create(ctx, &clusterDeployment)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &clusterDeployment)

				mcsNameHash := sha256.Sum256([]byte(multiClusterServiceName))
				serviceSetKey = types.NamespacedName{
					Namespace: clusterDeployment.Namespace,
					Name:      fmt.Sprintf("%s-%x", clusterDeployment.Name, mcsNameHash[:4]),
				}
				mgmtServiceSetKey = types.NamespacedName{
					Namespace: testSystemNamespace,
					Name:      fmt.Sprintf("management-%x", mcsNameHash[:4]),
				}
			})

			// NOTE: ServiceTemplate2 doesn't need to be reconciled
			// because we are setting its status manually.
			By("reconciling ServiceTemplate1 used by MultiClusterService")
			templateReconciler := TemplateReconciler{
				Client:                k8sClient,
				downloadHelmChartFunc: fakeDownloadHelmChartFunc,
			}
			serviceTemplateReconciler := &ServiceTemplateReconciler{TemplateReconciler: templateReconciler}
			_, err = serviceTemplateReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: serviceTemplate1Ref})
			Expect(err).NotTo(HaveOccurred())

			By("having the valid status for ServiceTemplate2")
			Expect(k8sClient.Get(ctx, serviceTemplate1Ref, serviceTemplate)).To(Succeed())
			Expect(serviceTemplate.Status.Valid).To(BeTrue())
			Expect(serviceTemplate.Status.ValidationError).To(BeEmpty())

			By("creating MultiClusterService")
			err = k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)
			if err != nil && apierrors.IsNotFound(err) {
				multiClusterService = &kcmv1.MultiClusterService{
					ObjectMeta: metav1.ObjectMeta{
						Name:   multiClusterServiceName,
						Labels: map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
						Finalizers: []string{
							// Reconcile attempts to add this finalizer and returns immediately
							// if successful. So adding this finalizer here manually in order
							// to avoid having to call reconcile multiple times for this test.
							kcmv1.MultiClusterServiceFinalizer,
						},
					},
					Spec: kcmv1.MultiClusterServiceSpec{
						ClusterSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"test": "true",
							},
						},
						ServiceSpec: kcmv1.ServiceSpec{
							Provider: kcmv1.StateManagementProviderConfig{
								Name: kubeutil.DefaultStateManagementProvider,
							},
							Services: []kcmv1.Service{
								{
									Template:  serviceTemplate1Name,
									Name:      helmChartReleaseName,
									Namespace: "ns1",
								},
								{
									Template:  serviceTemplate2Name,
									Name:      helmChartReleaseName,
									Namespace: "ns2",
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, multiClusterService)).To(Succeed())
			}
		})

		AfterEach(func() {
			deleteIfNotFound := func(ctx context.Context, key client.ObjectKey, obj client.Object) {
				if err := k8sClient.Get(ctx, key, obj); err == nil {
					Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
				} else if !apierrors.IsNotFound(err) { // ignore not found error
					Expect(err).ToNot(HaveOccurred())
				}
			}

			By("cleaning up")

			// The MCS is created with kcmv1.MultiClusterServiceFinalizer preset so the
			// reconciler can act in a single pass. At teardown the finalizer keeps the
			// object stuck Terminating, which would cause subsequent tests to reuse a
			// stale MCS (BeforeEach's NotFound guard skips re-creation). Clear the
			// finalizer and wait for real deletion so each test starts fresh.
			mcsToDelete := &kcmv1.MultiClusterService{}
			if err := k8sClient.Get(ctx, multiClusterServiceRef, mcsToDelete); err == nil {
				if len(mcsToDelete.Finalizers) > 0 {
					mcsToDelete.Finalizers = nil
					Expect(k8sClient.Update(ctx, mcsToDelete)).To(Succeed())
				}
				Expect(k8sClient.Delete(ctx, mcsToDelete)).To(Succeed())
				Eventually(func() bool {
					return apierrors.IsNotFound(k8sClient.Get(ctx, multiClusterServiceRef, &kcmv1.MultiClusterService{}))
				}).Should(BeTrue())
			} else if !apierrors.IsNotFound(err) {
				Expect(err).ToNot(HaveOccurred())
			}

			serviceTemplateResource := &kcmv1.ServiceTemplate{}
			deleteIfNotFound(ctx, serviceTemplate1Ref, serviceTemplateResource)
			deleteIfNotFound(ctx, serviceTemplate2Ref, serviceTemplateResource)
			deleteIfNotFound(ctx, serviceTemplate3Ref, serviceTemplateResource)

			helmChartResource := &sourcev1.HelmChart{}
			deleteIfNotFound(ctx, helmChartRef, helmChartResource)

			helmRepositoryResource := &sourcev1.HelmRepository{}
			deleteIfNotFound(ctx, helmRepositoryRef, helmRepositoryResource)

			serviceSet := &kcmv1.ServiceSet{}
			deleteIfNotFound(ctx, serviceSetKey, serviceSet)
			deleteIfNotFound(ctx, mgmtServiceSetKey, serviceSet)
		})

		It("should successfully reconcile the resource", func() {
			By("reconciling MultiClusterService")
			multiClusterServiceReconciler := &MultiClusterServiceReconciler{
				Client:          mgrClient,
				timeFunc:        func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
				SystemNamespace: testSystemNamespace,
			}

			Eventually(func(g Gomega) {
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(k8sClient.Get(ctx, serviceSetKey, &serviceSet)).NotTo(HaveOccurred())
			}).Should(Succeed())

			By("updating MultiClusterService to remove cluster selector")
			Eventually(func(g Gomega) {
				// Update the MCS
				g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).NotTo(HaveOccurred())
				multiClusterService.Spec.ClusterSelector = metav1.LabelSelector{}
				g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())

				// Reconcile the MCS
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).ToNot(HaveOccurred())

				// Verify that the ServiceSet for CD (via MCS) no longer exists
				err = k8sClient.Get(ctx, serviceSetKey, &serviceSet)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}).Should(Succeed())

			By("updating MultiClusterService to set selfManagement")
			Eventually(func(g Gomega) {
				// Update the MCS
				g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).NotTo(HaveOccurred())
				multiClusterService.Spec.ServiceSpec.Provider.SelfManagement = true
				g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())

				// Reconcile the MCS
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).ToNot(HaveOccurred())

				// Verify the ServiceSet for Management is created
				g.Expect(k8sClient.Get(ctx, mgmtServiceSetKey, &mgmtServiceSet)).ToNot(HaveOccurred())

				// Verify the ServiceSet for CD (via MCS) still doesn't exist
				err = k8sClient.Get(ctx, serviceSetKey, &serviceSet)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}).Should(Succeed())

			By("updating MultiClusterService to re-add cluster selector")
			Eventually(func(g Gomega) {
				// Update the MCS
				g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).NotTo(HaveOccurred())
				multiClusterService.Spec.ClusterSelector = metav1.LabelSelector{
					MatchLabels: map[string]string{
						"test": "true",
					},
				}
				g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())

				// Reconcile the MCS
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).ToNot(HaveOccurred())

				// Verify that the ServiceSet for CD (via MCS) and Management exist
				g.Expect(k8sClient.Get(ctx, serviceSetKey, &serviceSet)).ToNot(HaveOccurred())
				g.Expect(k8sClient.Get(ctx, mgmtServiceSetKey, &mgmtServiceSet)).ToNot(HaveOccurred())
			}).Should(Succeed())
		})

		// Regression test: the ClusterInReadyState denominator must be sourced from
		// the matching ClusterDeployments (plus selfManagement), not from the list of
		// existing ServiceSets. Previously, when ServiceSet creation was blocked for a
		// matching cluster (e.g. an unmet DependsOn dependency or a transient error),
		// that cluster silently vanished from the total and the status reported "0/0"
		// while a matching cluster existed.
		//
		// We block ServiceSet creation by adding a DependsOn referencing a sibling MCS
		// that has no ServiceSet for the matching CD. okToReconcileServiceSet errors,
		// createOrUpdateServiceSet skips, and the matching CD must still show up in
		// the denominator.
		It("should reflect matching ClusterDeployments in ClusterInReadyState even when ServiceSet creation is blocked", func() {
			const siblingMCSName = "test-multiclusterservice-dependency"

			By("creating a sibling MCS that the test MCS will DependsOn", func() {
				sibling := &kcmv1.MultiClusterService{
					ObjectMeta: metav1.ObjectMeta{
						Name:   siblingMCSName,
						Labels: map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
					},
					Spec: kcmv1.MultiClusterServiceSpec{
						ClusterSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{"test": "true"},
						},
						ServiceSpec: kcmv1.ServiceSpec{
							Provider: kcmv1.StateManagementProviderConfig{
								Name: kubeutil.DefaultStateManagementProvider,
							},
							Services: []kcmv1.Service{
								{Template: serviceTemplate1Name, Name: "sibling-rel", Namespace: "ns-sibling"},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, sibling)).To(Succeed())
				DeferCleanup(func() {
					got := &kcmv1.MultiClusterService{}
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: siblingMCSName}, got); err == nil {
						Expect(k8sClient.Delete(ctx, got)).To(Succeed())
					}
				})

				// The reconciler reads via mgrClient (cached), so wait for the
				// cache to observe the sibling before exercising the dependency
				// path. okToReconcileServiceSet must be able to Get the sibling
				// MCS object for the dependency check to engage.
				Eventually(func(g Gomega) {
					g.Expect(mgrClient.Get(ctx, client.ObjectKey{Name: siblingMCSName}, &kcmv1.MultiClusterService{})).To(Succeed())
				}).Should(Succeed())
			})

			By("adding DependsOn to the test MCS so okToReconcileServiceSet blocks creation", func() {
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
					multiClusterService.Spec.DependsOn = []string{siblingMCSName}
					g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())
				}).Should(Succeed())

				// Wait for mgrClient cache to observe the DependsOn update;
				// otherwise the first Reconcile would see the old spec, find
				// no dependencies to check, and create the ServiceSet.
				Eventually(func(g Gomega) {
					fresh := &kcmv1.MultiClusterService{}
					g.Expect(mgrClient.Get(ctx, multiClusterServiceRef, fresh)).To(Succeed())
					g.Expect(fresh.Spec.DependsOn).To(ContainElement(siblingMCSName))
				}).Should(Succeed())
			})

			reconciler := &MultiClusterServiceReconciler{
				Client:          mgrClient,
				timeFunc:        func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
				SystemNamespace: testSystemNamespace,
			}

			By("reconciling and asserting the denominator counts the matching CD even with no ServiceSet", func() {
				Eventually(func(g Gomega) {
					_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})

					// ServiceSet for the matching CD must not exist - creation was
					// blocked because the sibling's ServiceSet for this CD is missing.
					err := k8sClient.Get(ctx, serviceSetKey, &kcmv1.ServiceSet{})
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
						"ServiceSet should not be created when DependsOn is unsatisfied")

					// The matching CD must still appear in the denominator: "0/1",
					// not "0/0" as the previous (buggy) implementation would report.
					g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
					g.Expect(multiClusterService.Status.Conditions).To(ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.ClusterInReadyStateCondition),
						HaveField("Status", metav1.ConditionFalse),
						HaveField("Reason", kcmv1.FailedReason),
						HaveField("Message", "0/1"),
					)))
				}).Should(Succeed())
			})
		})

		// Regression test for continuous ServiceSet generation bumps caused by
		// in-place mutation of stored spec during every reconcile.
		// Each entry updates the MCS services, drains transient reconciles caused
		// by that spec change, captures ServiceSet.Generation, then reconciles
		// repeatedly and asserts Generation never increases. A growing Generation
		// would signal that the controller is still producing a spec diff against
		// its own stored state.
		DescribeTable("should keep ServiceSet generation stable across reconciles",
			func(services []kcmv1.Service) {
				multiClusterServiceReconciler := &MultiClusterServiceReconciler{
					Client:          mgrClient,
					timeFunc:        func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
					SystemNamespace: testSystemNamespace,
				}

				By("updating MultiClusterService services to the entry's spec")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
					multiClusterService.Spec.ServiceSpec.Services = services
					g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())
				}).Should(Succeed())

				By("draining reconciles until the ServiceSet spec reflects the update")
				// Each reconcile is wrapped in Eventually so that transient
				// conflict errors on MCS status updates (caused by mgrClient
				// cache lag after our spec Update above) get retried until the
				// cache catches up and the reconcile becomes a no-op.
				Eventually(func(g Gomega) {
					_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
					g.Expect(err).NotTo(HaveOccurred())
					fresh := &kcmv1.ServiceSet{}
					g.Expect(k8sClient.Get(ctx, serviceSetKey, fresh)).To(Succeed())
					g.Expect(fresh.Spec.Services).To(HaveLen(len(services)))
				}).Should(Succeed())

				// A couple more reconciles so the controller has converged on a
				// stable spec before we sample Generation.
				for range 2 {
					Eventually(func(g Gomega) {
						_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
						g.Expect(err).NotTo(HaveOccurred())
					}).Should(Succeed())
				}

				fresh := &kcmv1.ServiceSet{}
				Expect(k8sClient.Get(ctx, serviceSetKey, fresh)).To(Succeed())
				initialGeneration := fresh.Generation
				Expect(initialGeneration).To(BeNumerically(">", 0))

				By("asserting ServiceSet generation is stable across repeated reconciles")
				Consistently(func(g Gomega) {
					_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
					g.Expect(err).NotTo(HaveOccurred())
					refreshed := &kcmv1.ServiceSet{}
					g.Expect(k8sClient.Get(ctx, serviceSetKey, refreshed)).To(Succeed())
					g.Expect(refreshed.Generation).To(Equal(initialGeneration),
						"ServiceSet generation bumped from %d to %d — controller is producing a spec diff against its own stored state",
						initialGeneration, refreshed.Generation)
				}, 3*time.Second, 100*time.Millisecond).Should(Succeed())
			},
			Entry("service with inline Values",
				[]kcmv1.Service{
					{
						Template:  serviceTemplate1Name,
						Name:      helmChartReleaseName,
						Namespace: "ns1",
						Values:    "foo: bar",
					},
				},
			),
			Entry("service with ValuesFrom only",
				[]kcmv1.Service{
					{
						Template:  serviceTemplate2Name,
						Name:      helmChartReleaseName,
						Namespace: "ns2",
						ValuesFrom: []kcmv1.ValuesFrom{
							{Kind: "ConfigMap", Name: "helm-values"},
						},
					},
				},
			),
			Entry("service with no values, template with LocalSourceRef and no version",
				[]kcmv1.Service{
					{
						Template:  serviceTemplate3Name,
						Name:      helmChartReleaseName,
						Namespace: "ns3",
					},
				},
			),
			Entry("mixed: inline Values, ValuesFrom, and LocalSourceRef-backed template",
				[]kcmv1.Service{
					{
						Template:  serviceTemplate1Name,
						Name:      helmChartReleaseName,
						Namespace: "ns1",
						Values:    "foo: bar",
					},
					{
						Template:  serviceTemplate2Name,
						Name:      helmChartReleaseName,
						Namespace: "ns2",
						ValuesFrom: []kcmv1.ValuesFrom{
							{Kind: "ConfigMap", Name: "helm-values"},
						},
					},
					{
						Template:  serviceTemplate3Name,
						Name:      helmChartReleaseName,
						Namespace: "ns3",
					},
				},
			),
		)
	})
})
