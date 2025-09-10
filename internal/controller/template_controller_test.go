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
	"testing"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"helm.sh/helm/v3/pkg/chart"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

var _ = Describe("Template Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			helmRepoNamespace = metav1.NamespaceDefault
			helmRepoName      = "test-helmrepo"
			helmChartName     = "test-helmchart"
			helmChartURL      = "http://source-controller.kcm-system.svc.cluster.local./helmchart/kcm-system/test-chart/0.1.0.tar.gz"
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
		clusterTemplate := &kcmv1.ClusterTemplate{}
		serviceTemplate := &kcmv1.ServiceTemplate{}
		providerTemplate := &kcmv1.ProviderTemplate{}
		helmRepo := &sourcev1.HelmRepository{}
		helmChart := &sourcev1.HelmChart{}

		helmSpec := kcmv1.HelmSpec{
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
				resource := &kcmv1.ClusterTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: metav1.NamespaceDefault,
					},
					Spec: kcmv1.ClusterTemplateSpec{Helm: helmSpec},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
			By("creating the custom resource for the Kind ServiceTemplate")
			err = k8sClient.Get(ctx, typeNamespacedName, serviceTemplate)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: metav1.NamespaceDefault,
					},
					Spec: kcmv1.ServiceTemplateSpec{Helm: &helmSpec},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
			By("creating the custom resource for the Kind ProviderTemplate")
			err = k8sClient.Get(ctx, typeNamespacedName, providerTemplate)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &kcmv1.ProviderTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name: resourceName,
					},
					Spec: kcmv1.ProviderTemplateSpec{Helm: helmSpec},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			clusterTemplateResource := &kcmv1.ClusterTemplate{}
			err := k8sClient.Get(ctx, typeNamespacedName, clusterTemplateResource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ClusterTemplate")
			Expect(k8sClient.Delete(ctx, clusterTemplateResource)).To(Succeed())

			serviceTemplateResource := &kcmv1.ServiceTemplate{}
			err = k8sClient.Get(ctx, typeNamespacedName, serviceTemplateResource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ServiceTemplate")
			Expect(k8sClient.Delete(ctx, serviceTemplateResource)).To(Succeed())

			providerTemplateResource := &kcmv1.ProviderTemplate{}
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
				mgmtName              = kcmv1.ManagementName
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
			clusterTemplate = &kcmv1.ClusterTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterTemplateName,
					Namespace: metav1.NamespaceDefault,
					Labels:    map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
				},
				Spec: kcmv1.ClusterTemplateSpec{
					Helm:              helmSpec,
					Providers:         []string{someProviderName, otherProviderName},
					ProviderContracts: kcmv1.CompatibilityContracts{someProviderName: someRequiredContract, otherProviderName: otherRequiredContract},
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

			mgmt := &kcmv1.Management{}
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
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterTemplate), &kcmv1.ClusterTemplate{}))
			}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())
		})
	})
})

func Test_generateSchemaConfigMapName(t *testing.T) {
	tests := []struct {
		name     string
		template templateCommon
		expected string
	}{
		{
			name: "cluster template",
			template: &kcmv1.ClusterTemplate{
				TypeMeta: metav1.TypeMeta{
					APIVersion: kcmv1.GroupVersion.String(),
					Kind:       kcmv1.ClusterTemplateKind,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-template",
				},
			},
			expected: "schema-ct-test-template",
		},
		{
			name: "provider template",
			template: &kcmv1.ProviderTemplate{
				TypeMeta: metav1.TypeMeta{
					APIVersion: kcmv1.GroupVersion.String(),
					Kind:       kcmv1.ProviderTemplateKind,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-template",
				},
			},
			expected: "schema-pt-test-template",
		},
		{
			name: "service template",
			template: &kcmv1.ServiceTemplate{
				TypeMeta: metav1.TypeMeta{
					APIVersion: kcmv1.GroupVersion.String(),
					Kind:       kcmv1.ServiceTemplateKind,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-template",
				},
			},
			expected: "schema-st-test-template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateSchemaConfigMapName(tt.template)
			assert.Equal(t, tt.expected, got)
		})
	}
}
