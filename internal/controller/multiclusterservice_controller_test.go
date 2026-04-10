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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

var _ = Describe("MultiClusterService Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			serviceTemplate1Name    = "test-service-1-v0-1-0"
			serviceTemplate2Name    = "test-service-2-v0-1-0"
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
		multiClusterService := &kcmv1.MultiClusterService{}
		clusterDeployment := kcmv1.ClusterDeployment{}
		serviceSet := kcmv1.ServiceSet{}

		helmRepositoryRef := types.NamespacedName{Namespace: testSystemNamespace, Name: helmRepoName}
		helmChartRef := types.NamespacedName{Namespace: testSystemNamespace, Name: helmChartName}
		serviceTemplate1Ref := types.NamespacedName{Namespace: testSystemNamespace, Name: serviceTemplate1Name}
		serviceTemplate2Ref := types.NamespacedName{Namespace: testSystemNamespace, Name: serviceTemplate2Name}
		multiClusterServiceRef := types.NamespacedName{Name: multiClusterServiceName}
		serviceSetKey := types.NamespacedName{}

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
			By("cleaning up")
			multiClusterServiceResource := &kcmv1.MultiClusterService{}
			Expect(k8sClient.Get(ctx, multiClusterServiceRef, multiClusterServiceResource)).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(ctx, multiClusterService)).To(Succeed())

			serviceTemplateResource := &kcmv1.ServiceTemplate{}
			Expect(k8sClient.Get(ctx, serviceTemplate1Ref, serviceTemplateResource)).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(ctx, serviceTemplateResource)).To(Succeed())

			Expect(k8sClient.Get(ctx, serviceTemplate2Ref, serviceTemplateResource)).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(ctx, serviceTemplateResource)).To(Succeed())

			helmChartResource := &sourcev1.HelmChart{}
			Expect(k8sClient.Get(ctx, helmChartRef, helmChartResource)).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(ctx, helmChartResource)).To(Succeed())

			helmRepositoryResource := &sourcev1.HelmRepository{}
			Expect(k8sClient.Get(ctx, helmRepositoryRef, helmRepositoryResource)).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(ctx, helmRepositoryResource)).To(Succeed())

			serviceSet := &kcmv1.ServiceSet{}
			Expect(k8sClient.Get(ctx, serviceSetKey, serviceSet)).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(ctx, serviceSet)).To(Succeed())
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
		})
	})

	Context("When reconciling sequential service upgrades", func() {
		const (
			seqMCSName     = "test-seq-upgrade-mcs"
			seqTemplate1   = "test-seq-svc-a-v1"
			seqTemplate2   = "test-seq-svc-a-v2"
			seqTemplate3   = "test-seq-svc-a-v3"
			seqChainName   = "test-seq-svc-a-chain"
			seqServiceName = "seq-service-a"
			seqServiceNS   = "seq-ns"
			seqCDLabel     = "seq-upgrade-test"
		)

		var seqServiceSetKey types.NamespacedName
		seqMCSRef := types.NamespacedName{Name: seqMCSName}

		BeforeEach(func() {
			By("ensuring test namespace exists")
			ns := &corev1.Namespace{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: testSystemNamespace}, ns)
			if apierrors.IsNotFound(err) {
				ns = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testSystemNamespace}}
				Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			}

			By("creating ServiceTemplates for v1, v2 and v3")
			for _, tmpl := range []struct {
				name    string
				version string
			}{
				{seqTemplate1, "1"},
				{seqTemplate2, "2"},
				{seqTemplate3, "3"},
			} {
				st := &kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{Name: tmpl.name, Namespace: testSystemNamespace},
					Spec: kcmv1.ServiceTemplateSpec{
						Version: tmpl.version,
						Helm: &kcmv1.HelmSpec{
							ChartSpec: &sourcev1.HelmChartSpec{
								Chart:   tmpl.name,
								Version: tmpl.version,
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, st)).To(Succeed())
				DeferCleanup(k8sClient.Delete, st)
			}

			By("creating ServiceTemplateChain for v1->v2->v3")
			// KCMManagedLabelKey bypasses the webhook's template-existence check,
			// avoiding a potential cache-sync race between template creation and
			// chain admission validation.
			chain := &kcmv1.ServiceTemplateChain{
				ObjectMeta: metav1.ObjectMeta{
					Name:      seqChainName,
					Namespace: testSystemNamespace,
					Labels:    map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue},
				},
				Spec: kcmv1.TemplateChainSpec{
					SupportedTemplates: []kcmv1.SupportedTemplate{
						{
							Name: seqTemplate1,
							AvailableUpgrades: []kcmv1.AvailableUpgrade{
								{Name: seqTemplate2, Version: "2"},
							},
						},
						{
							Name: seqTemplate2,
							AvailableUpgrades: []kcmv1.AvailableUpgrade{
								{Name: seqTemplate3, Version: "3"},
							},
						},
						{Name: seqTemplate3},
					},
				},
			}
			Expect(k8sClient.Create(ctx, chain)).To(Succeed())
			DeferCleanup(k8sClient.Delete, chain)

			By("creating ClusterDeployment")
			seqCD := kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "seq-cd-",
					Namespace:    testSystemNamespace,
					Labels:       map[string]string{seqCDLabel: "true"},
				},
				Spec: kcmv1.ClusterDeploymentSpec{
					Template:   "sample-template",
					Credential: "sample-credential",
					Config:     &apiextv1.JSON{Raw: []byte(`{"foo":"bar"}`)},
				},
			}
			Expect(k8sClient.Create(ctx, &seqCD)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &seqCD)

			mcsNameHash := sha256.Sum256([]byte(seqMCSName))
			seqServiceSetKey = types.NamespacedName{
				Namespace: seqCD.Namespace,
				Name:      fmt.Sprintf("%s-%x", seqCD.Name, mcsNameHash[:4]),
			}
			DeferCleanup(func() {
				ss := &kcmv1.ServiceSet{}
				if err := k8sClient.Get(ctx, seqServiceSetKey, ss); err == nil {
					_ = k8sClient.Delete(ctx, ss)
				}
			})

			By("creating MultiClusterService")
			seqMCS := &kcmv1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{
					Name: seqMCSName,
					Finalizers: []string{
						// Added manually to avoid an extra reconcile cycle just
						// for finalizer registration before the actual assertions.
						kcmv1.MultiClusterServiceFinalizer,
					},
				},
				Spec: kcmv1.MultiClusterServiceSpec{
					ClusterSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{seqCDLabel: "true"},
					},
					ServiceSpec: kcmv1.ServiceSpec{
						Provider: kcmv1.StateManagementProviderConfig{
							Name: kubeutil.DefaultStateManagementProvider,
						},
						Services: []kcmv1.Service{
							{
								Name:          seqServiceName,
								Namespace:     seqServiceNS,
								Template:      seqTemplate1,
								Version:       "1",
								TemplateChain: seqChainName,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, seqMCS)).To(Succeed())
			DeferCleanup(k8sClient.Delete, seqMCS)
		})

		It("should advance service through v1->v2->v3 one step at a time", func() {
			reconciler := &MultiClusterServiceReconciler{
				Client:          mgrClient,
				timeFunc:        func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
				SystemNamespace: testSystemNamespace,
			}
			seqSS := &kcmv1.ServiceSet{}

			By("cycle 1: initial reconcile deploys service at v1")
			Eventually(func(g Gomega) {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: seqMCSRef})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(k8sClient.Get(ctx, seqServiceSetKey, seqSS)).To(Succeed())
				g.Expect(seqSS.Spec.Services).To(HaveLen(1))
				g.Expect(seqSS.Spec.Services[0].Template).To(Equal(seqTemplate1))
				g.Expect(seqSS.Spec.Services[0].Version).NotTo(BeNil())
				g.Expect(*seqSS.Spec.Services[0].Version).To(Equal("1"))
			}).Should(Succeed())

			By("marking v1 as Deployed in the ServiceSet status")
			Expect(k8sClient.Get(ctx, seqServiceSetKey, seqSS)).To(Succeed())
			seqSS.Status.Services = []kcmv1.ServiceState{
				{
					Name:      seqServiceName,
					Namespace: seqServiceNS,
					State:     kcmv1.ServiceStateDeployed,
					Version:   new("1"),
				},
			}
			Expect(k8sClient.Status().Update(ctx, seqSS)).To(Succeed())

			By("updating MultiClusterService target to v3 — skipping v2 to exercise sequential enforcement")
			updatedMCS := &kcmv1.MultiClusterService{}
			Expect(k8sClient.Get(ctx, seqMCSRef, updatedMCS)).To(Succeed())
			updatedMCS.Spec.ServiceSpec.Services[0].Template = seqTemplate3
			updatedMCS.Spec.ServiceSpec.Services[0].Version = "3"
			Expect(k8sClient.Update(ctx, updatedMCS)).To(Succeed())

			By("cycle 2: reconcile must enforce sequential step — ServiceSet moves to v2, not v3")
			Eventually(func(g Gomega) {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: seqMCSRef})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(k8sClient.Get(ctx, seqServiceSetKey, seqSS)).To(Succeed())
				g.Expect(seqSS.Spec.Services).To(HaveLen(1))
				g.Expect(seqSS.Spec.Services[0].Template).To(Equal(seqTemplate2))
				g.Expect(seqSS.Spec.Services[0].Version).NotTo(BeNil())
				g.Expect(*seqSS.Spec.Services[0].Version).To(Equal("2"))
			}).Should(Succeed())

			By("marking v2 as Deployed in the ServiceSet status")
			Expect(k8sClient.Get(ctx, seqServiceSetKey, seqSS)).To(Succeed())
			seqSS.Status.Services = []kcmv1.ServiceState{
				{
					Name:      seqServiceName,
					Namespace: seqServiceNS,
					State:     kcmv1.ServiceStateDeployed,
					Version:   new("2"),
				},
			}
			Expect(k8sClient.Status().Update(ctx, seqSS)).To(Succeed())

			By("cycle 3: reconcile advances service to the final target v3")
			Eventually(func(g Gomega) {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: seqMCSRef})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(k8sClient.Get(ctx, seqServiceSetKey, seqSS)).To(Succeed())
				g.Expect(seqSS.Spec.Services).To(HaveLen(1))
				g.Expect(seqSS.Spec.Services[0].Template).To(Equal(seqTemplate3))
				g.Expect(seqSS.Spec.Services[0].Version).NotTo(BeNil())
				g.Expect(*seqSS.Spec.Services[0].Version).To(Equal("3"))
			}).Should(Succeed())
		})
	})
})
