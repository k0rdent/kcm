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
	"errors"
	"fmt"
	"testing"
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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/serviceset"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

// createFailingSelfManagingDependency creates and reconciles a MultiClusterService named name
// that self-manages, matches a CD via ClusterSelector "test": "true", and references a
// nonexistent ServiceTemplate so it never produces any ServiceSet. Used as a dependency MCS in
// tests exercising okToReconcileServiceSet's blocking behavior.
func createFailingSelfManagingDependency(name string, reconciler *MultiClusterServiceReconciler) *kcmv1.MultiClusterService {
	failingMCS := &kcmv1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
		},
		Spec: kcmv1.MultiClusterServiceSpec{
			ClusterSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"test": "true"},
			},
			ServiceSpec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{
					Name:           kubeutil.DefaultStateManagementProvider,
					SelfManagement: true,
				},
				Services: []kcmv1.Service{
					{Template: "nonexistent-service-template", Name: "bad-rel", Namespace: "ns-bad"},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, failingMCS)).To(Succeed())
	DeferCleanup(func() {
		got := &kcmv1.MultiClusterService{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Name: name}, got); err == nil {
			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
		}
	})

	Eventually(func(g Gomega) {
		g.Expect(mgrClient.Get(ctx, client.ObjectKey{Name: name}, &kcmv1.MultiClusterService{})).To(Succeed())
	}).Should(Succeed())

	Eventually(func(g Gomega) {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: name}})
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name}, failingMCS)).To(Succeed())
		g.Expect(failingMCS.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", kcmv1.ServicesReferencesValidationCondition),
			HaveField("Status", metav1.ConditionFalse),
		)))
	}).Should(Succeed())

	return failingMCS
}

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

			// Regression test for https://github.com/k0rdent/kcm/issues/2919:
			// disabling selfManagement while the cluster selector still matches
			// the CD must delete the management ServiceSet, since it no longer
			// matches, while leaving the CD's ServiceSet untouched.
			By("updating MultiClusterService to unset selfManagement")
			Eventually(func(g Gomega) {
				// Update the MCS
				g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).NotTo(HaveOccurred())
				multiClusterService.Spec.ServiceSpec.Provider.SelfManagement = false
				g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())

				// Reconcile the MCS
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).ToNot(HaveOccurred())

				// Verify the ServiceSet for Management no longer exists
				err = k8sClient.Get(ctx, mgmtServiceSetKey, &mgmtServiceSet)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

				// Verify the ServiceSet for CD (via MCS) still exists
				g.Expect(k8sClient.Get(ctx, serviceSetKey, &serviceSet)).ToNot(HaveOccurred())
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
					_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
					// Waiting on an MCS dependency is an expected, self-resolving state,
					// not a reconcile failure, so it must not be returned as an error
					// (which would otherwise put the object into backoff-rate-limited
					// requeues instead of the steady default requeue interval).
					g.Expect(err).NotTo(HaveOccurred())

					// ServiceSet for the matching CD must not exist - creation was
					// blocked because the sibling's ServiceSet for this CD is missing.
					ssErr := k8sClient.Get(ctx, serviceSetKey, &kcmv1.ServiceSet{})
					g.Expect(apierrors.IsNotFound(ssErr)).To(BeTrue(),
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

					// The MultiClusterServiceDependencyReady condition must explicitly call
					// out that this MCS is waiting on its dependency, instead of leaving the
					// user to infer that from the bare ClusterInReadyState ratio.
					g.Expect(multiClusterService.Status.Conditions).To(ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.MultiClusterServiceDependencyReadyCondition),
						HaveField("Status", metav1.ConditionFalse),
						HaveField("Reason", kcmv1.MultiClusterServiceDependencyNotReadyReason),
						HaveField("Message", "waiting for MultiClusterService dependencies to be ready on 1 matching cluster(s)"),
					)))

					// The blocked cluster must be surfaced per-cluster in matchingClusters,
					// even though it has no ServiceSet yet, with a message identifying the
					// sibling MultiClusterService it's waiting on.
					g.Expect(multiClusterService.Status.MatchingClusters).To(ContainElement(SatisfyAll(
						HaveField("Name", clusterDeployment.Name),
						HaveField("Namespace", clusterDeployment.Namespace),
						HaveField("Deployed", false),
						HaveField("Reason", kcmv1.MultiClusterServiceDependencyNotReadyReason),
						HaveField("Message", ContainSubstring(siblingMCSName)),
					)))
				}).Should(Succeed())
			})
		})

		// Regression test: okToReconcileServiceSet used to decide whether it was checking the
		// self-management (mgmt) target or a real matching ClusterDeployment based on the
		// reconciled MCS's own SelfManagement flag, instead of on whether a cd was actually
		// passed in. That breaks down for an MCS that both self-manages AND matches a
		// ClusterSelector, since okToReconcileServiceSet is then called twice for it: once with
		// cd == nil (mgmt) and once with the real cd. With the bug, the CD-based call still used
		// the mgmt cluster's (empty) labels instead of the real cd's labels to decide whether a
		// dependency applied, so once the dependency MCS stopped self-managing, its still-matching
		// ClusterSelector was wrongly ignored and the CD ServiceSet was created despite the
		// dependency's services never having been deployed there.
		It("should keep the CD blocked on a dependency that still matches it via ClusterSelector, even after that dependency stops self-managing", func() {
			const failingMCSName = "test-multiclusterservice-failing-dependency"

			reconciler := &MultiClusterServiceReconciler{
				Client:          mgrClient,
				timeFunc:        func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
				SystemNamespace: testSystemNamespace,
			}

			failingMCS := createFailingSelfManagingDependency(failingMCSName, reconciler)

			By("configuring mcs2 to self-manage, match the CD, and depend on mcs1", func() {
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
					multiClusterService.Spec.DependsOn = []string{failingMCSName}
					multiClusterService.Spec.ServiceSpec.Provider.SelfManagement = true
					g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())
				}).Should(Succeed())

				Eventually(func(g Gomega) {
					fresh := &kcmv1.MultiClusterService{}
					g.Expect(mgrClient.Get(ctx, multiClusterServiceRef, fresh)).To(Succeed())
					g.Expect(fresh.Spec.DependsOn).To(ContainElement(failingMCSName))
					g.Expect(fresh.Spec.ServiceSpec.Provider.SelfManagement).To(BeTrue())
				}).Should(Succeed())
			})

			By("reconciling mcs2 and asserting it is blocked on both the CD and the mgmt cluster", func() {
				Eventually(func(g Gomega) {
					_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, serviceSetKey, &kcmv1.ServiceSet{}))).To(BeTrue(),
						"CD ServiceSet should not be created while mcs1 is blocking it")
					g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, mgmtServiceSetKey, &kcmv1.ServiceSet{}))).To(BeTrue(),
						"mgmt ServiceSet should not be created while mcs1 is blocking it")

					g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
					g.Expect(multiClusterService.Status.Conditions).To(ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.ClusterInReadyStateCondition),
						HaveField("Message", "0/2"),
					)))
				}).Should(Succeed())
			})

			By("mcs1 stops self-managing but still matches the CD via ClusterSelector", func() {
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: failingMCSName}, failingMCS)).To(Succeed())
					failingMCS.Spec.ServiceSpec.Provider.SelfManagement = false
					g.Expect(k8sClient.Update(ctx, failingMCS)).To(Succeed())
				}).Should(Succeed())

				Eventually(func(g Gomega) {
					fresh := &kcmv1.MultiClusterService{}
					g.Expect(mgrClient.Get(ctx, client.ObjectKey{Name: failingMCSName}, fresh)).To(Succeed())
					g.Expect(fresh.Spec.ServiceSpec.Provider.SelfManagement).To(BeFalse())
				}).Should(Succeed())
			})

			By("reconciling mcs2 again and asserting the mgmt ServiceSet is created while the CD one remains blocked", func() {
				Eventually(func(g Gomega) {
					_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
					g.Expect(err).NotTo(HaveOccurred())

					// mcs1 no longer targets the mgmt cluster, so mcs2's mgmt ServiceSet must
					// now be created.
					g.Expect(k8sClient.Get(ctx, mgmtServiceSetKey, &kcmv1.ServiceSet{})).To(Succeed())

					// mcs1 still matches the CD via ClusterSelector and has never produced a
					// ServiceSet there (its ServiceTemplate is still invalid), so mcs2's CD
					// ServiceSet must remain blocked.
					g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, serviceSetKey, &kcmv1.ServiceSet{}))).To(BeTrue(),
						"CD ServiceSet must remain blocked since mcs1 still matches the CD and has not deployed there")

					g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
					g.Expect(multiClusterService.Status.MatchingClusters).To(ContainElement(SatisfyAll(
						HaveField("Name", clusterDeployment.Name),
						HaveField("Namespace", clusterDeployment.Namespace),
						HaveField("Deployed", false),
						HaveField("Reason", kcmv1.MultiClusterServiceDependencyNotReadyReason),
					)))
				}).Should(Succeed())
			})
		})

		// Regression test: okToReconcileServiceSet's selfMgmtDependency shortcut (both mcs and
		// depMCS self-manage) used to bypass the ClusterSelector match check for BOTH the mgmt
		// pseudo-target AND any real matching ClusterDeployment. But self-management only pertains
		// to the mothership - it says nothing about whether depMCS targets some other, unrelated CD.
		// So once depMCS's ClusterSelector no longer matched the CD, it should have unblocked that
		// CD's ServiceSet regardless of both MCS's self-management status; instead the shortcut kept
		// it blocked because both objects still self-managed the mothership.
		It("should unblock the CD once a dependency's ClusterSelector stops matching it, even while both still self-manage", func() {
			const failingMCSName = "test-multiclusterservice-failing-dependency-2"

			reconciler := &MultiClusterServiceReconciler{
				Client:          mgrClient,
				timeFunc:        func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
				SystemNamespace: testSystemNamespace,
			}

			failingMCS := createFailingSelfManagingDependency(failingMCSName, reconciler)

			By("configuring mcs2 to self-manage, match the CD, and depend on mcs1", func() {
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
					multiClusterService.Spec.DependsOn = []string{failingMCSName}
					multiClusterService.Spec.ServiceSpec.Provider.SelfManagement = true
					g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())
				}).Should(Succeed())

				Eventually(func(g Gomega) {
					fresh := &kcmv1.MultiClusterService{}
					g.Expect(mgrClient.Get(ctx, multiClusterServiceRef, fresh)).To(Succeed())
					g.Expect(fresh.Spec.DependsOn).To(ContainElement(failingMCSName))
					g.Expect(fresh.Spec.ServiceSpec.Provider.SelfManagement).To(BeTrue())
				}).Should(Succeed())
			})

			By("reconciling mcs2 and asserting it is blocked on both the CD and the mgmt cluster", func() {
				Eventually(func(g Gomega) {
					_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, serviceSetKey, &kcmv1.ServiceSet{}))).To(BeTrue(),
						"CD ServiceSet should not be created while mcs1 is blocking it")
					g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, mgmtServiceSetKey, &kcmv1.ServiceSet{}))).To(BeTrue(),
						"mgmt ServiceSet should not be created while mcs1 is blocking it")

					g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
					g.Expect(multiClusterService.Status.Conditions).To(ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.ClusterInReadyStateCondition),
						HaveField("Message", "0/2"),
					)))
				}).Should(Succeed())
			})

			By("mcs1 stops matching the CD (ClusterSelector cleared) but keeps self-managing", func() {
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: failingMCSName}, failingMCS)).To(Succeed())
					failingMCS.Spec.ClusterSelector = metav1.LabelSelector{}
					g.Expect(k8sClient.Update(ctx, failingMCS)).To(Succeed())
				}).Should(Succeed())

				Eventually(func(g Gomega) {
					fresh := &kcmv1.MultiClusterService{}
					g.Expect(mgrClient.Get(ctx, client.ObjectKey{Name: failingMCSName}, fresh)).To(Succeed())
					g.Expect(fresh.Spec.ClusterSelector.MatchLabels).To(BeEmpty())
					g.Expect(fresh.Spec.ServiceSpec.Provider.SelfManagement).To(BeTrue())
				}).Should(Succeed())
			})

			By("reconciling mcs2 again and asserting the CD ServiceSet is created while the mgmt one remains blocked", func() {
				Eventually(func(g Gomega) {
					_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
					g.Expect(err).NotTo(HaveOccurred())

					// mcs1 no longer matches the CD via ClusterSelector, so mcs2's CD
					// ServiceSet must now be created.
					g.Expect(k8sClient.Get(ctx, serviceSetKey, &kcmv1.ServiceSet{})).To(Succeed())

					// mcs1 still self-manages the mothership and has never produced a
					// ServiceSet there (its ServiceTemplate is still invalid), so mcs2's
					// mgmt ServiceSet must remain blocked.
					g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, mgmtServiceSetKey, &kcmv1.ServiceSet{}))).To(BeTrue(),
						"mgmt ServiceSet must remain blocked since mcs1 still self-manages and has not deployed there")

					g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
					g.Expect(multiClusterService.Status.MatchingClusters).To(ContainElement(SatisfyAll(
						HaveField("Kind", "SveltosCluster"),
						HaveField("Name", "mgmt"),
						HaveField("Namespace", "mgmt"),
						HaveField("Deployed", false),
						HaveField("Reason", kcmv1.MultiClusterServiceDependencyNotReadyReason),
					)))
				}).Should(Succeed())
			})
		})

		// Regression test: setMatchingClusters used to build the ServiceSet-derived entries and the
		// blocked entries as two separate slices and simply concatenate them. If a cluster's
		// ServiceSet was created during an earlier, unblocked reconcile (and is deliberately never
		// deleted, since already-deployed services should keep running) and the dependency later
		// becomes unsatisfied again, that same cluster ends up in both slices - once from the
		// still-existing ServiceSet and once from the newly-computed blocked list - producing two
		// entries for the same cluster in .status.matchingClusters instead of one.
		It("should keep exactly one matchingClusters entry per cluster after a ServiceSet is created, blocked again, but not deleted", func() {
			const failingMCSName = "test-multiclusterservice-failing-dependency-3"

			reconciler := &MultiClusterServiceReconciler{
				Client:          mgrClient,
				timeFunc:        func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
				SystemNamespace: testSystemNamespace,
			}

			failingMCS := createFailingSelfManagingDependency(failingMCSName, reconciler)

			By("creating the Credential referenced by the CD, so setMatchingClusters can resolve it once a real matchingClusters entry exists", func() {
				cred := &kcmv1.Credential{
					ObjectMeta: metav1.ObjectMeta{
						Name:      clusterDeployment.Spec.Credential,
						Namespace: clusterDeployment.Namespace,
					},
					Spec: kcmv1.CredentialSpec{
						IdentityRef: &corev1.ObjectReference{
							Kind:       "Secret",
							Name:       "sample-identity",
							Namespace:  clusterDeployment.Namespace,
							APIVersion: "v1",
						},
					},
				}
				Expect(k8sClient.Create(ctx, cred)).To(Succeed())
				DeferCleanup(func() {
					got := &kcmv1.Credential{}
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: cred.Name, Namespace: cred.Namespace}, got); err == nil {
						Expect(k8sClient.Delete(ctx, got)).To(Succeed())
					}
				})
			})

			By("configuring mcs2 to self-manage, match the CD, and depend on mcs1", func() {
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
					multiClusterService.Spec.DependsOn = []string{failingMCSName}
					multiClusterService.Spec.ServiceSpec.Provider.SelfManagement = true
					g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())
				}).Should(Succeed())

				Eventually(func(g Gomega) {
					fresh := &kcmv1.MultiClusterService{}
					g.Expect(mgrClient.Get(ctx, multiClusterServiceRef, fresh)).To(Succeed())
					g.Expect(fresh.Spec.DependsOn).To(ContainElement(failingMCSName))
					g.Expect(fresh.Spec.ServiceSpec.Provider.SelfManagement).To(BeTrue())
				}).Should(Succeed())
			})

			By("reconciling mcs2 and asserting it is blocked on both the CD and the mgmt cluster", func() {
				Eventually(func(g Gomega) {
					_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, serviceSetKey, &kcmv1.ServiceSet{}))).To(BeTrue(),
						"CD ServiceSet should not be created while mcs1 is blocking it")
				}).Should(Succeed())
			})

			By("mcs1 stops matching the CD (ClusterSelector cleared), unblocking the CD ServiceSet", func() {
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: failingMCSName}, failingMCS)).To(Succeed())
					failingMCS.Spec.ClusterSelector = metav1.LabelSelector{}
					g.Expect(k8sClient.Update(ctx, failingMCS)).To(Succeed())
				}).Should(Succeed())

				Eventually(func(g Gomega) {
					fresh := &kcmv1.MultiClusterService{}
					g.Expect(mgrClient.Get(ctx, client.ObjectKey{Name: failingMCSName}, fresh)).To(Succeed())
					g.Expect(fresh.Spec.ClusterSelector.MatchLabels).To(BeEmpty())
				}).Should(Succeed())

				Eventually(func(g Gomega) {
					_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(k8sClient.Get(ctx, serviceSetKey, &kcmv1.ServiceSet{})).To(Succeed())
				}).Should(Succeed())

				// The MCS reconciler only creates the ServiceSet; a separate ServiceSet
				// controller (not running in this suite) is what normally populates
				// .status.cluster once the underlying Sveltos objects exist. Set it
				// manually here to simulate that and let setMatchingClusters pick up
				// this cluster as a real (non-blocked) matchingClusters entry.
				Eventually(func(g Gomega) {
					ss := &kcmv1.ServiceSet{}
					g.Expect(k8sClient.Get(ctx, serviceSetKey, ss)).To(Succeed())
					ss.Status.Cluster = &corev1.ObjectReference{
						Kind:       kcmv1.ClusterDeploymentKind,
						Name:       clusterDeployment.Name,
						Namespace:  clusterDeployment.Namespace,
						APIVersion: kcmv1.GroupVersion.WithKind(kcmv1.ClusterDeploymentKind).GroupVersion().String(),
					}
					ss.Status.Deployed = true
					g.Expect(k8sClient.Status().Update(ctx, ss)).To(Succeed())
				}).Should(Succeed())

				Eventually(func(g Gomega) {
					_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
					matching := make([]kcmv1.MatchingCluster, 0)
					for _, c := range multiClusterService.Status.MatchingClusters {
						if c.Kind == kcmv1.ClusterDeploymentKind {
							matching = append(matching, c)
						}
					}
					g.Expect(matching).To(HaveLen(1))
					g.Expect(matching[0].Deployed).To(BeTrue())
				}).Should(Succeed())
			})

			By("mcs1 matches the CD again, blocking its ServiceSet, without deleting the already-created ServiceSet", func() {
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: failingMCSName}, failingMCS)).To(Succeed())
					failingMCS.Spec.ClusterSelector = metav1.LabelSelector{
						MatchLabels: map[string]string{"test": "true"},
					}
					g.Expect(k8sClient.Update(ctx, failingMCS)).To(Succeed())
				}).Should(Succeed())

				Eventually(func(g Gomega) {
					fresh := &kcmv1.MultiClusterService{}
					g.Expect(mgrClient.Get(ctx, client.ObjectKey{Name: failingMCSName}, fresh)).To(Succeed())
					g.Expect(fresh.Spec.ClusterSelector.MatchLabels).To(HaveKeyWithValue("test", "true"))
				}).Should(Succeed())
			})

			By("reconciling mcs2 again and asserting exactly one matchingClusters entry remains for the CD, reflecting the blocked state", func() {
				Eventually(func(g Gomega) {
					_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
					g.Expect(err).NotTo(HaveOccurred())

					// The ServiceSet created earlier must still exist - already-deployed
					// services are never torn down just because a dependency becomes
					// unsatisfied again.
					g.Expect(k8sClient.Get(ctx, serviceSetKey, &kcmv1.ServiceSet{})).To(Succeed())

					g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
					matching := make([]kcmv1.MatchingCluster, 0)
					for _, c := range multiClusterService.Status.MatchingClusters {
						if c.Kind == kcmv1.ClusterDeploymentKind {
							matching = append(matching, c)
						}
					}
					g.Expect(matching).To(HaveLen(1), "expected exactly one matchingClusters entry for the CD, got %d", len(matching))
					g.Expect(matching[0]).To(SatisfyAll(
						HaveField("Name", clusterDeployment.Name),
						HaveField("Namespace", clusterDeployment.Namespace),
						HaveField("Deployed", false),
						HaveField("Reason", kcmv1.MultiClusterServiceDependencyNotReadyReason),
					))
				}).Should(Succeed())
			})
		})

		It("should preserve ServiceSet when ClusterDeployment labels diverge from selector and KeepServicesOnSelectorMismatch is set", func() {
			multiClusterServiceReconciler := &MultiClusterServiceReconciler{
				Client:          mgrClient,
				timeFunc:        func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
				SystemNamespace: testSystemNamespace,
			}

			By("reconciling MultiClusterService to create the initial ServiceSet")
			Eventually(func(g Gomega) {
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(k8sClient.Get(ctx, serviceSetKey, &serviceSet)).NotTo(HaveOccurred())
			}).Should(Succeed())

			By("setting KeepServicesOnSelectorMismatch=true on the MultiClusterService")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
				multiClusterService.Spec.KeepServicesOnSelectorMismatch = true
				g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())
			}).Should(Succeed())

			By("removing the matching label from the ClusterDeployment so it diverges from the selector")
			Eventually(func(g Gomega) {
				fresh := &kcmv1.ClusterDeployment{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&clusterDeployment), fresh)).To(Succeed())
				delete(fresh.Labels, "test")
				g.Expect(k8sClient.Update(ctx, fresh)).To(Succeed())
			}).Should(Succeed())

			// Block the assertion until the reconciler's cached client actually
			// observes both changes. Without this gate, a spuriously passing
			// test could result from the reconciler operating on a stale spec
			// (flag still false, label still matches) and trivially leaving
			// the ServiceSet alone.
			By("waiting for the mgrClient cache to observe both spec changes")
			Eventually(func(g Gomega) {
				observedMCS := &kcmv1.MultiClusterService{}
				g.Expect(mgrClient.Get(ctx, multiClusterServiceRef, observedMCS)).To(Succeed())
				g.Expect(observedMCS.Spec.KeepServicesOnSelectorMismatch).To(BeTrue())

				observedCD := &kcmv1.ClusterDeployment{}
				g.Expect(mgrClient.Get(ctx, client.ObjectKeyFromObject(&clusterDeployment), observedCD)).To(Succeed())
				g.Expect(observedCD.Labels).NotTo(HaveKey("test"))
			}).Should(Succeed())

			By("asserting the ServiceSet is preserved across repeated reconciles despite the label mismatch")
			Consistently(func(g Gomega) {
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).NotTo(HaveOccurred())

				fresh := &kcmv1.ServiceSet{}
				g.Expect(k8sClient.Get(ctx, serviceSetKey, fresh)).To(Succeed(),
					"ServiceSet was deleted despite KeepServicesOnSelectorMismatch=true")
				g.Expect(fresh.DeletionTimestamp.IsZero()).To(BeTrue(),
					"ServiceSet was marked for deletion despite KeepServicesOnSelectorMismatch=true")
			}, 2*time.Second, 100*time.Millisecond).Should(Succeed())
		})

		It("should preserve ServiceSet when ClusterSelector is cleared and KeepServicesOnSelectorMismatch is set", func() {
			multiClusterServiceReconciler := &MultiClusterServiceReconciler{
				Client:          mgrClient,
				timeFunc:        func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
				SystemNamespace: testSystemNamespace,
			}

			By("reconciling MultiClusterService to create the initial ServiceSet")
			Eventually(func(g Gomega) {
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(k8sClient.Get(ctx, serviceSetKey, &serviceSet)).NotTo(HaveOccurred())
			}).Should(Succeed())

			By("setting KeepServicesOnSelectorMismatch=true and clearing ClusterSelector in a single update")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
				multiClusterService.Spec.KeepServicesOnSelectorMismatch = true
				multiClusterService.Spec.ClusterSelector = metav1.LabelSelector{}
				g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())
			}).Should(Succeed())

			By("waiting for the mgrClient cache to observe the cleared selector and the flag")
			Eventually(func(g Gomega) {
				observedMCS := &kcmv1.MultiClusterService{}
				g.Expect(mgrClient.Get(ctx, multiClusterServiceRef, observedMCS)).To(Succeed())
				g.Expect(observedMCS.Spec.KeepServicesOnSelectorMismatch).To(BeTrue())
				g.Expect(observedMCS.Spec.ClusterSelector.MatchLabels).To(BeEmpty())
				g.Expect(observedMCS.Spec.ClusterSelector.MatchExpressions).To(BeEmpty())
			}).Should(Succeed())

			By("asserting the ServiceSet is preserved across repeated reconciles despite the empty selector")
			Consistently(func(g Gomega) {
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).NotTo(HaveOccurred())

				fresh := &kcmv1.ServiceSet{}
				g.Expect(k8sClient.Get(ctx, serviceSetKey, fresh)).To(Succeed(),
					"ServiceSet was deleted despite KeepServicesOnSelectorMismatch=true")
				g.Expect(fresh.DeletionTimestamp.IsZero()).To(BeTrue(),
					"ServiceSet was marked for deletion despite KeepServicesOnSelectorMismatch=true")
			}, 2*time.Second, 100*time.Millisecond).Should(Succeed())
		})

		// Regression test for https://github.com/k0rdent/kcm/issues/2919: the
		// self-management ServiceSet must be treated the same as a ClusterDeployment-scoped
		// ServiceSet with respect to KeepServicesOnSelectorMismatch — disabling
		// selfManagement is a "no longer matches" event for that ServiceSet, and
		// cleanupServiceSets must not delete it while the flag is set.
		It("should preserve self-management ServiceSet when selfManagement is disabled and KeepServicesOnSelectorMismatch is set", func() {
			multiClusterServiceReconciler := &MultiClusterServiceReconciler{
				Client:          mgrClient,
				timeFunc:        func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
				SystemNamespace: testSystemNamespace,
			}

			By("enabling selfManagement and reconciling to create the management ServiceSet")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
				multiClusterService.Spec.ServiceSpec.Provider.SelfManagement = true
				g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())

				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(k8sClient.Get(ctx, mgmtServiceSetKey, &mgmtServiceSet)).NotTo(HaveOccurred())
			}).Should(Succeed())

			By("setting KeepServicesOnSelectorMismatch=true and disabling selfManagement in a single update")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
				multiClusterService.Spec.KeepServicesOnSelectorMismatch = true
				multiClusterService.Spec.ServiceSpec.Provider.SelfManagement = false
				g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())
			}).Should(Succeed())

			// Block the assertion until the reconciler's cached client actually
			// observes both changes. Without this gate, a spuriously passing
			// test could result from the reconciler operating on a stale spec
			// (flag still false, selfManagement still true) and trivially
			// leaving the ServiceSet alone.
			By("waiting for the mgrClient cache to observe both spec changes")
			Eventually(func(g Gomega) {
				observedMCS := &kcmv1.MultiClusterService{}
				g.Expect(mgrClient.Get(ctx, multiClusterServiceRef, observedMCS)).To(Succeed())
				g.Expect(observedMCS.Spec.KeepServicesOnSelectorMismatch).To(BeTrue())
				g.Expect(observedMCS.Spec.ServiceSpec.Provider.SelfManagement).To(BeFalse())
			}).Should(Succeed())

			By("asserting the management ServiceSet is preserved across repeated reconciles despite selfManagement being disabled")
			Consistently(func(g Gomega) {
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).NotTo(HaveOccurred())

				fresh := &kcmv1.ServiceSet{}
				g.Expect(k8sClient.Get(ctx, mgmtServiceSetKey, fresh)).To(Succeed(),
					"management ServiceSet was deleted despite KeepServicesOnSelectorMismatch=true")
				g.Expect(fresh.DeletionTimestamp.IsZero()).To(BeTrue(),
					"management ServiceSet was marked for deletion despite KeepServicesOnSelectorMismatch=true")
			}, 2*time.Second, 100*time.Millisecond).Should(Succeed())
		})

		It("should update preserved ServiceSet in place when ClusterDeployment labels match again", func() {
			multiClusterServiceReconciler := &MultiClusterServiceReconciler{
				Client:          mgrClient,
				timeFunc:        func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
				SystemNamespace: testSystemNamespace,
			}

			By("reconciling MultiClusterService to create the initial ServiceSet")
			Eventually(func(g Gomega) {
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(k8sClient.Get(ctx, serviceSetKey, &serviceSet)).NotTo(HaveOccurred())
			}).Should(Succeed())

			// Capture the UID so we can later prove the ServiceSet was updated
			// in place rather than recreated. A changed UID would indicate that
			// the controller deleted the kept ServiceSet on rematch and stamped
			// out a fresh one — which would defeat the rollout flow this flag
			// is designed to support.
			initialUID := serviceSet.UID
			Expect(initialUID).NotTo(BeEmpty())
			initialServicesCount := len(multiClusterService.Spec.ServiceSpec.Services)

			By("setting KeepServicesOnSelectorMismatch=true on the MultiClusterService")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
				multiClusterService.Spec.KeepServicesOnSelectorMismatch = true
				g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())
			}).Should(Succeed())

			By("removing the matching label from the ClusterDeployment so the ServiceSet is preserved")
			Eventually(func(g Gomega) {
				fresh := &kcmv1.ClusterDeployment{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&clusterDeployment), fresh)).To(Succeed())
				delete(fresh.Labels, "test")
				g.Expect(k8sClient.Update(ctx, fresh)).To(Succeed())
			}).Should(Succeed())

			By("waiting for the mgrClient cache to observe the label removal")
			Eventually(func(g Gomega) {
				observedCD := &kcmv1.ClusterDeployment{}
				g.Expect(mgrClient.Get(ctx, client.ObjectKeyFromObject(&clusterDeployment), observedCD)).To(Succeed())
				g.Expect(observedCD.Labels).NotTo(HaveKey("test"))
			}).Should(Succeed())

			By("reconciling to confirm the ServiceSet is preserved while the cluster is mismatched")
			Eventually(func(g Gomega) {
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).NotTo(HaveOccurred())
				fresh := &kcmv1.ServiceSet{}
				g.Expect(k8sClient.Get(ctx, serviceSetKey, fresh)).To(Succeed())
				g.Expect(fresh.UID).To(Equal(initialUID))
			}).Should(Succeed())

			By("updating the MultiClusterService Services list while the cluster is mismatched")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterService)).To(Succeed())
				multiClusterService.Spec.ServiceSpec.Services = append(
					multiClusterService.Spec.ServiceSpec.Services,
					kcmv1.Service{
						Template:  serviceTemplate3Name,
						Name:      helmChartReleaseName,
						Namespace: "ns3",
					},
				)
				g.Expect(k8sClient.Update(ctx, multiClusterService)).To(Succeed())
			}).Should(Succeed())

			By("re-adding the matching label to the ClusterDeployment so it matches the selector again")
			Eventually(func(g Gomega) {
				fresh := &kcmv1.ClusterDeployment{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&clusterDeployment), fresh)).To(Succeed())
				if fresh.Labels == nil {
					fresh.Labels = map[string]string{}
				}
				fresh.Labels["test"] = "true"
				g.Expect(k8sClient.Update(ctx, fresh)).To(Succeed())
			}).Should(Succeed())

			By("waiting for the mgrClient cache to observe the rematch and the new Services entry")
			Eventually(func(g Gomega) {
				observedCD := &kcmv1.ClusterDeployment{}
				g.Expect(mgrClient.Get(ctx, client.ObjectKeyFromObject(&clusterDeployment), observedCD)).To(Succeed())
				g.Expect(observedCD.Labels).To(HaveKeyWithValue("test", "true"))

				observedMCS := &kcmv1.MultiClusterService{}
				g.Expect(mgrClient.Get(ctx, multiClusterServiceRef, observedMCS)).To(Succeed())
				g.Expect(observedMCS.Spec.ServiceSpec.Services).To(HaveLen(initialServicesCount + 1))
			}).Should(Succeed())

			By("reconciling and asserting the preserved ServiceSet is updated in place to reflect the new MCS spec")
			Eventually(func(g Gomega) {
				_, err := multiClusterServiceReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: multiClusterServiceRef})
				g.Expect(err).NotTo(HaveOccurred())

				fresh := &kcmv1.ServiceSet{}
				g.Expect(k8sClient.Get(ctx, serviceSetKey, fresh)).To(Succeed())
				g.Expect(fresh.UID).To(Equal(initialUID),
					"ServiceSet UID changed — the controller recreated it on rematch instead of updating in place")
				g.Expect(fresh.DeletionTimestamp.IsZero()).To(BeTrue(),
					"ServiceSet was marked for deletion on rematch")
				g.Expect(fresh.Spec.Services).To(HaveLen(initialServicesCount+1),
					"ServiceSet spec did not converge to the updated MCS Services list after rematch")
			}).Should(Succeed())
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

// Test_okToReconcileServiceSet verifies that okToReconcileServiceSet distinguishes expected
// "dependency not ready" states (missing dependency ServiceSet, dependency not fully deployed)
// - returned via the blocked value and meant to be surfaced only in status - from unexpected
// operational errors (failing to Get the dependency MultiClusterService/ServiceSet, an
// unparsable ClusterSelector) - returned via err and meant to propagate as a real reconcile
// error so controller-runtime retries it with backoff instead of it being silently folded into
// the "waiting on dependency" status.
func Test_okToReconcileServiceSet(t *testing.T) {
	const (
		mcsName     = "mcs2"
		depMCSName  = "mcs1"
		cdName      = "test-cd"
		cdNamespace = "test-ns"
		sysNS       = "kcm-system"
	)

	depService := kcmv1.Service{Template: "tmpl", Name: "svc", Namespace: "ns"}
	matchingSelector := metav1.LabelSelector{MatchLabels: map[string]string{"test": "true"}}

	newDepMCS := func(selfManagement bool, clusterSelector metav1.LabelSelector) *kcmv1.MultiClusterService {
		return &kcmv1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Name: depMCSName},
			Spec: kcmv1.MultiClusterServiceSpec{
				ClusterSelector: clusterSelector,
				ServiceSpec: kcmv1.ServiceSpec{
					Provider: kcmv1.StateManagementProviderConfig{SelfManagement: selfManagement},
					Services: []kcmv1.Service{depService},
				},
			},
		}
	}

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cdName,
			Namespace: cdNamespace,
			Labels:    map[string]string{"test": "true"},
		},
	}

	tests := []struct {
		name              string
		mcsSelfManagement bool
		cd                *kcmv1.ClusterDeployment // nil to exercise the self-management (mgmt) path
		depMCS            *kcmv1.MultiClusterService
		serviceSet        *kcmv1.ServiceSet
		clientInterceptor *interceptor.Funcs
		wantBlocked       bool
		wantErr           bool
	}{
		{
			name:   "transient error getting dependency MultiClusterService is a real error, not blocked",
			cd:     cd,
			depMCS: newDepMCS(false, matchingSelector),
			clientInterceptor: &interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*kcmv1.MultiClusterService); ok && key.Name == depMCSName {
						return errors.New("transient API server error")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			},
			wantErr: true,
		},
		{
			name: "malformed ClusterSelector on the dependency is a real error, not blocked",
			cd:   cd,
			depMCS: newDepMCS(false, metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: "owner", Operator: "NotAnOperator", Values: []string{"dev-team"}},
				},
			}),
			wantErr: true,
		},
		{
			name:        "dependency ServiceSet not yet created is an expected blocked state",
			cd:          cd,
			depMCS:      newDepMCS(false, matchingSelector),
			wantBlocked: true,
		},
		{
			name:   "transient error getting dependency ServiceSet is a real error, not blocked",
			cd:     cd,
			depMCS: newDepMCS(false, matchingSelector),
			clientInterceptor: &interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*kcmv1.ServiceSet); ok {
						return errors.New("transient API server error")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			},
			wantErr: true,
		},
		{
			name:   "dependency ServiceSet exists but not all services deployed is an expected blocked state",
			cd:     cd,
			depMCS: newDepMCS(false, matchingSelector),
			serviceSet: &kcmv1.ServiceSet{
				Status: kcmv1.ServiceSetStatus{},
			},
			wantBlocked: true,
		},
		{
			name:   "dependency fully deployed: neither blocked nor error",
			cd:     cd,
			depMCS: newDepMCS(false, matchingSelector),
			serviceSet: &kcmv1.ServiceSet{
				Status: kcmv1.ServiceSetStatus{
					Services: []kcmv1.ServiceState{
						{Name: depService.Name, Namespace: depService.Namespace, State: kcmv1.ServiceStateDeployed},
					},
				},
			},
		},
		{
			name:              "mgmt path: dependency ServiceSet not yet created is an expected blocked state",
			mcsSelfManagement: true,
			cd:                nil,
			depMCS:            newDepMCS(true, metav1.LabelSelector{}),
			wantBlocked:       true,
		},
		{
			// mgmt path, but depMCS does not self-manage, so it never targets the mgmt
			// pseudo-cluster - there is no dependency here and reconcile may proceed.
			name:              "mgmt path: dependency that does not target the mgmt cluster is not a dependency",
			mcsSelfManagement: true,
			cd:                nil,
			depMCS:            newDepMCS(false, matchingSelector),
		},
		{
			// An empty ClusterSelector matches no ClusterDeployment (mirroring reconcileUpdate),
			// so depMCS does not target this cluster and there is no dependency.
			name:   "dependency with empty ClusterSelector is not a dependency",
			cd:     cd,
			depMCS: newDepMCS(false, metav1.LabelSelector{}),
		},
		{
			// depMCS's ClusterSelector does not match the cluster's labels, so it does not
			// target this cluster and there is no dependency.
			name:   "dependency whose ClusterSelector does not match the cluster is not a dependency",
			cd:     cd,
			depMCS: newDepMCS(false, metav1.LabelSelector{MatchLabels: map[string]string{"test": "false"}}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{tt.depMCS}
			if tt.cd != nil {
				objs = append(objs, tt.cd)
			}
			if tt.serviceSet != nil {
				ssKey := serviceset.ObjectKey(sysNS, tt.cd, tt.depMCS)
				tt.serviceSet.Name = ssKey.Name
				tt.serviceSet.Namespace = ssKey.Namespace
				objs = append(objs, tt.serviceSet)
			}

			builder := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(objs...)
			if tt.clientInterceptor != nil {
				builder = builder.WithInterceptorFuncs(*tt.clientInterceptor)
			}

			mcs := &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{Name: mcsName},
				Spec: kcmv1.MultiClusterServiceSpec{
					DependsOn: []string{depMCSName},
					ServiceSpec: kcmv1.ServiceSpec{
						Provider: kcmv1.StateManagementProviderConfig{SelfManagement: tt.mcsSelfManagement},
					},
				},
			}

			r := &MultiClusterServiceReconciler{
				Client:          builder.Build(),
				SystemNamespace: sysNS,
			}

			var blocked []blockedCluster
			ok, err := r.okToReconcileServiceSet(t.Context(), mcs, tt.cd, &blocked)

			// A real, unexpected error is returned via err; an expected blocked state is
			// surfaced only by appending to the blocked slice (err stays nil for it).
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected err, got nil")
				}
			} else if err != nil {
				t.Fatalf("expected no err, got: %v", err)
			}

			gotBlocked := len(blocked) > 0
			if gotBlocked != tt.wantBlocked {
				t.Fatalf("expected blocked=%v, got %v (%v)", tt.wantBlocked, gotBlocked, blocked)
			}

			// ok gates create/update: true only when neither errored nor blocked.
			if wantOk := !tt.wantErr && !tt.wantBlocked; ok != wantOk {
				t.Fatalf("expected ok=%v, got %v", wantOk, ok)
			}
		})
	}
}

// Test_okToReconcileServiceSet_errorAndBlocked verifies the mixed case a single-dependency
// table cannot express: when one dependency fails with a real, unexpected error while another
// is merely not-ready-yet, okToReconcileServiceSet must both return a non-nil err (so the
// failure is propagated and retried with backoff) and append the not-ready cluster to blocked
// (so it is still surfaced on mcs.Status). ok must be false.
func Test_okToReconcileServiceSet_errorAndBlocked(t *testing.T) {
	const (
		mcsName     = "mcs3"
		errDepName  = "dep-err" // dependency whose ServiceSet Get fails with a real error
		blkDepName  = "dep-blk" // dependency that is blocked (its ServiceSet does not exist yet)
		cdName      = "test-cd"
		cdNamespace = "test-ns"
		sysNS       = "kcm-system"
	)

	matchingSelector := metav1.LabelSelector{MatchLabels: map[string]string{"test": "true"}}
	depService := kcmv1.Service{Template: "tmpl", Name: "svc", Namespace: "ns"}
	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: cdName, Namespace: cdNamespace, Labels: map[string]string{"test": "true"}},
	}

	newDep := func(name string) *kcmv1.MultiClusterService {
		return &kcmv1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: kcmv1.MultiClusterServiceSpec{
				ClusterSelector: matchingSelector,
				ServiceSpec:     kcmv1.ServiceSpec{Services: []kcmv1.Service{depService}},
			},
		}
	}
	errDep := newDep(errDepName)
	blkDep := newDep(blkDepName)

	// Fail the Get only for errDep's ServiceSet; blkDep's ServiceSet is simply absent (NotFound).
	errDepSSetKey := serviceset.ObjectKey(sysNS, cd, errDep)
	builder := fake.NewClientBuilder().WithScheme(testscheme.Scheme).
		WithObjects(cd, errDep, blkDep).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*kcmv1.ServiceSet); ok && key == errDepSSetKey {
					return errors.New("transient API server error")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		})

	mcs := &kcmv1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{Name: mcsName},
		Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{errDepName, blkDepName}},
	}
	r := &MultiClusterServiceReconciler{Client: builder.Build(), SystemNamespace: sysNS}

	var blocked []blockedCluster
	ok, err := r.okToReconcileServiceSet(t.Context(), mcs, cd, &blocked)

	if err == nil {
		t.Fatal("expected a real error from the failing dependency, got nil")
	}
	if len(blocked) != 1 {
		t.Fatalf("expected exactly one blocked cluster, got %d (%v)", len(blocked), blocked)
	}
	if ok {
		t.Fatal("expected ok=false when both errored and blocked")
	}
}

// Test_okToReconcileServiceSet_nilBlocked verifies the defensive nil-pointer guard: a blocked
// state with a nil blocked slice pointer must not panic. ok is still false so the caller skips
// create/update, and err stays nil since the blocked state is never folded into err.
func Test_okToReconcileServiceSet_nilBlocked(t *testing.T) {
	const (
		mcsName     = "mcs4"
		depMCSName  = "dep"
		cdName      = "test-cd"
		cdNamespace = "test-ns"
		sysNS       = "kcm-system"
	)

	depMCS := &kcmv1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{Name: depMCSName},
		Spec: kcmv1.MultiClusterServiceSpec{
			ClusterSelector: metav1.LabelSelector{MatchLabels: map[string]string{"test": "true"}},
			ServiceSpec:     kcmv1.ServiceSpec{Services: []kcmv1.Service{{Template: "tmpl", Name: "svc", Namespace: "ns"}}},
		},
	}
	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: cdName, Namespace: cdNamespace, Labels: map[string]string{"test": "true"}},
	}
	mcs := &kcmv1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{Name: mcsName},
		Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{depMCSName}},
	}

	r := &MultiClusterServiceReconciler{
		Client:          fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(cd, depMCS).Build(),
		SystemNamespace: sysNS,
	}

	// depMCS's ServiceSet does not exist -> blocked state, but blocked pointer is nil.
	ok, err := r.okToReconcileServiceSet(t.Context(), mcs, cd, nil)
	if err != nil {
		t.Fatalf("expected no err, got: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a blocked state")
	}
}
