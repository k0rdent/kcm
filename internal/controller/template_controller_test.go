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
	"fmt"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"helm.sh/helm/v3/pkg/chart"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hmcmirantiscomv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
)

var _ = Describe("Template Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			helmRepoNamespace = metav1.NamespaceDefault
			helmRepoName      = "test-helmrepo"
			helmChartName     = "test-helmchart"
			helmChartURL      = "http://source-controller.hmc-system.svc.cluster.local./helmchart/hmc-system/test-chart/0.1.0.tar.gz"
		)

		fakeDownloadHelmChartFunc := func(context.Context, *sourcev1.Artifact) (*chart.Chart, error) {
			return &chart.Chart{
				Metadata: &chart.Metadata{
					APIVersion: "v2",
					Version:    "0.1.0",
					Name:       "test-chart",
				},
			}, nil
		}

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: metav1.NamespaceDefault,
		}
		clusterTemplate := &hmcmirantiscomv1alpha1.ClusterTemplate{}
		serviceTemplate := &hmcmirantiscomv1alpha1.ServiceTemplate{}
		providerTemplate := &hmcmirantiscomv1alpha1.ProviderTemplate{}
		helmRepo := &sourcev1.HelmRepository{}
		helmChart := &sourcev1.HelmChart{}

		helmSpec := hmcmirantiscomv1alpha1.HelmSpec{
			ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind:      "HelmChart",
				Name:      helmChartName,
				Namespace: helmRepoNamespace,
			},
		}

		BeforeEach(func() {
			By("creating helm repository")
			err := k8sClient.Get(ctx, types.NamespacedName{Name: helmRepoName, Namespace: helmRepoNamespace}, helmRepo)
			if err != nil && apierrors.IsNotFound(err) {
				helmRepo = &sourcev1.HelmRepository{
					ObjectMeta: metav1.ObjectMeta{
						Name:      helmRepoName,
						Namespace: helmRepoNamespace,
					},
					Spec: sourcev1.HelmRepositorySpec{
						URL: "oci://test/helmrepo",
					},
				}
				Expect(k8sClient.Create(ctx, helmRepo)).To(Succeed())
			}

			By("creating helm chart")
			err = k8sClient.Get(ctx, types.NamespacedName{Name: helmChartName, Namespace: helmRepoNamespace}, helmChart)
			if err != nil && apierrors.IsNotFound(err) {
				helmChart = &sourcev1.HelmChart{
					ObjectMeta: metav1.ObjectMeta{
						Name:      helmChartName,
						Namespace: helmRepoNamespace,
					},
					Spec: sourcev1.HelmChartSpec{
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
			helmChart.Status.Artifact = &sourcev1.Artifact{
				URL:            helmChartURL,
				LastUpdateTime: metav1.Now(),
			}
			Expect(k8sClient.Status().Update(ctx, helmChart)).Should(Succeed())

			By("creating the custom resource for the Kind ClusterTemplate")
			err = k8sClient.Get(ctx, typeNamespacedName, clusterTemplate)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &hmcmirantiscomv1alpha1.ClusterTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: metav1.NamespaceDefault,
					},
					Spec: hmcmirantiscomv1alpha1.ClusterTemplateSpec{Helm: helmSpec},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
			By("creating the custom resource for the Kind ServiceTemplate")
			err = k8sClient.Get(ctx, typeNamespacedName, serviceTemplate)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &hmcmirantiscomv1alpha1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: metav1.NamespaceDefault,
					},
					Spec: hmcmirantiscomv1alpha1.ServiceTemplateSpec{Helm: helmSpec},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
			By("creating the custom resource for the Kind ProviderTemplate")
			err = k8sClient.Get(ctx, typeNamespacedName, providerTemplate)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &hmcmirantiscomv1alpha1.ProviderTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name: resourceName,
					},
					Spec: hmcmirantiscomv1alpha1.ProviderTemplateSpec{Helm: helmSpec},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			clusterTemplateResource := &hmcmirantiscomv1alpha1.ClusterTemplate{}
			err := k8sClient.Get(ctx, typeNamespacedName, clusterTemplateResource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ClusterTemplate")
			Expect(k8sClient.Delete(ctx, clusterTemplateResource)).To(Succeed())

			serviceTemplateResource := &hmcmirantiscomv1alpha1.ServiceTemplate{}
			err = k8sClient.Get(ctx, typeNamespacedName, serviceTemplateResource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ServiceTemplate")
			Expect(k8sClient.Delete(ctx, serviceTemplateResource)).To(Succeed())

			providerTemplateResource := &hmcmirantiscomv1alpha1.ProviderTemplate{}
			err = k8sClient.Get(ctx, typeNamespacedName, providerTemplateResource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ProviderTemplate")
			Expect(k8sClient.Delete(ctx, providerTemplateResource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			templateReconciler := TemplateReconciler{
				Client:                mgrClient,
				downloadHelmChartFunc: fakeDownloadHelmChartFunc,
			}
			By("Reconciling the ClusterTemplate resource")
			clusterTemplateReconciler := &ClusterTemplateReconciler{TemplateReconciler: templateReconciler}
			_, err := clusterTemplateReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling the ServiceTemplate resource")
			serviceTemplateReconciler := &ServiceTemplateReconciler{TemplateReconciler: templateReconciler}
			_, err = serviceTemplateReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling the ProviderTemplate resource")
			providerTemplateReconciler := &ProviderTemplateReconciler{TemplateReconciler: templateReconciler}
			_, err = providerTemplateReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should successfully validate cluster templates providers compatibility attributes", func() {
			const (
				clusterTemplateName   = "cluster-template-test-name"
				mgmtName              = hmcmirantiscomv1alpha1.ManagementName
				someProviderName      = "test-provider-name"
				otherProviderName     = "test-provider-name-other"
				someRequiredContract  = "v1beta2"
				otherRequiredContract = "v1beta1"
				someExposedContract   = "v1beta1_v1beta2"
				otherExposedContract  = "v1beta1"
				capiVersion           = "v1beta1"

				timeout  = time.Second * 10
				interval = time.Millisecond * 250
			)

			// NOTE: the cluster template from BeforeEach cannot be reused because spec is immutable
			By("Creating cluster template with constrained versions")
			clusterTemplate = &hmcmirantiscomv1alpha1.ClusterTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterTemplateName,
					Namespace: metav1.NamespaceDefault,
					Labels:    map[string]string{hmcmirantiscomv1alpha1.GenericComponentLabelName: hmcmirantiscomv1alpha1.GenericComponentLabelValueHMC},
				},
				Spec: hmcmirantiscomv1alpha1.ClusterTemplateSpec{
					Helm:              helmSpec,
					Providers:         []string{someProviderName, otherProviderName},
					ProviderContracts: hmcmirantiscomv1alpha1.CompatibilityContracts{someProviderName: someRequiredContract, otherProviderName: otherRequiredContract},
				},
			}
			Expect(k8sClient.Create(ctx, clusterTemplate)).To(Succeed())

			By("Checking the cluster template has been updated")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterTemplate), clusterTemplate); err != nil {
					return err
				}

				if l := len(clusterTemplate.Spec.Providers); l != 2 {
					return fmt.Errorf("expected .spec.providers length to be exactly 2, got %d", l)
				}
				if l := len(clusterTemplate.Spec.ProviderContracts); l != 2 {
					return fmt.Errorf("expected .spec.capiContracts length to be exactly 2, got %d", l)
				}

				if v := clusterTemplate.Spec.Providers[0]; v != someProviderName {
					return fmt.Errorf("expected .spec.providers[0] to be %s, got %s", someProviderName, v)
				}
				if v := clusterTemplate.Spec.Providers[1]; v != otherProviderName {
					return fmt.Errorf("expected .spec.providers[1] to be %s, got %s", otherProviderName, v)
				}
				if v := clusterTemplate.Spec.ProviderContracts[someProviderName]; v != someRequiredContract {
					return fmt.Errorf("expected .spec.capiContracts[%s] to be %s, got %s", someProviderName, someRequiredContract, v)
				}
				if v := clusterTemplate.Spec.ProviderContracts[otherProviderName]; v != otherRequiredContract {
					return fmt.Errorf("expected .spec.capiContracts[%s] to be %s, got %s", otherProviderName, otherRequiredContract, v)
				}

				return nil
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

			mgmt := &hmcmirantiscomv1alpha1.Management{}
			key := client.ObjectKey{Name: mgmtName}
			Expect(k8sClient.Get(ctx, key, mgmt)).To(Succeed())

			By("Checking the management cluster appears")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmt), mgmt); err != nil {
					return err
				}

				if l := len(mgmt.Status.AvailableProviders); l != 2 {
					return fmt.Errorf("expected .status.availableProviders length to be exactly 2, got %d", l)
				}
				if l := len(mgmt.Status.CAPIContracts); l != 2 {
					return fmt.Errorf("expected .status.capiContracts length to be exactly 2, got %d", l)
				}

				if v := mgmt.Status.AvailableProviders[0]; v != someProviderName {
					return fmt.Errorf("expected .status.availableProviders[0] to be %s, got %s", someProviderName, v)
				}
				if v := mgmt.Status.AvailableProviders[1]; v != otherProviderName {
					return fmt.Errorf("expected .status.availableProviders[1] to be %s, got %s", otherProviderName, v)
				}
				if v := mgmt.Status.CAPIContracts[someProviderName]; v[capiVersion] != someExposedContract {
					return fmt.Errorf("expected .status.capiContracts[%s][%s] to be %s, got %s", someProviderName, capiVersion, someExposedContract, v[capiVersion])
				}
				if v := mgmt.Status.CAPIContracts[otherProviderName]; v[capiVersion] != otherExposedContract {
					return fmt.Errorf("expected .status.capiContracts[%s][%s] to be %s, got %s", otherProviderName, capiVersion, otherExposedContract, v[capiVersion])
				}

				return nil
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

			By("Reconciling the cluster template")
			clusterTemplateReconciler := &ClusterTemplateReconciler{TemplateReconciler: TemplateReconciler{
				Client:                k8sClient,
				downloadHelmChartFunc: fakeDownloadHelmChartFunc,
			}}
			_, err := clusterTemplateReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
				Name:      clusterTemplateName,
				Namespace: metav1.NamespaceDefault,
			}})
			Expect(err).NotTo(HaveOccurred())

			By("Having the valid cluster template status")
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterTemplate), clusterTemplate)).To(Succeed())
			Expect(clusterTemplate.Status.Valid).To(BeTrue())
			Expect(clusterTemplate.Status.ValidationError).To(BeEmpty())
			Expect(clusterTemplate.Status.Providers).To(HaveLen(2))
			Expect(clusterTemplate.Status.ProviderContracts).To(HaveLen(2))
			Expect(clusterTemplate.Status.Providers[0]).To(Equal(someProviderName))
			Expect(clusterTemplate.Status.ProviderContracts).To(BeEquivalentTo(map[string]string{otherProviderName: otherRequiredContract, someProviderName: someRequiredContract}))

			By("Removing the created objects")
			Expect(k8sClient.Delete(ctx, clusterTemplate)).To(Succeed())

			By("Checking the created objects have been removed")
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterTemplate), &hmcmirantiscomv1alpha1.ClusterTemplate{}))
			}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())
		})
	})
})
