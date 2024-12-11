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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hmcmirantiscomv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/objects/template"
)

var _ = Describe("Template Chain Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			ctChain1Name = "ct-chain-1"
			ctChain2Name = "ct-chain-2"
			stChain1Name = "st-chain-1"
			stChain2Name = "st-chain-2"
		)

		ctx := context.Background()

		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-chains",
			},
		}

		chartName := "test"
		templateHelmSpec := hmcmirantiscomv1alpha1.HelmSpec{ChartSpec: &sourcev1.HelmChartSpec{Chart: chartName}}
		chartRef := &helmcontrollerv2.CrossNamespaceSourceReference{
			Kind:      "HelmChart",
			Namespace: utils.DefaultSystemNamespace,
			Name:      chartName,
		}

		ctProviders := hmcmirantiscomv1alpha1.Providers{"ct-provider-test"}
		stProviders := hmcmirantiscomv1alpha1.Providers{"st-provider-test"}

		ctTemplates := map[string]*hmcmirantiscomv1alpha1.ClusterTemplate{
			// Should be created in target namespace
			"test": template.NewClusterTemplate(
				template.WithName("test"),
				template.WithNamespace(utils.DefaultSystemNamespace),
				template.WithHelmSpec(templateHelmSpec),
			),
			// Should be created in target namespace
			"ct0": template.NewClusterTemplate(
				template.WithName("ct0"),
				template.WithNamespace(utils.DefaultSystemNamespace),
				template.WithHelmSpec(templateHelmSpec),
			),
			// Should be created in target namespace. Template is managed by two chains.
			"ct1": template.NewClusterTemplate(
				template.WithName("ct1"),
				template.WithNamespace(utils.DefaultSystemNamespace),
				template.WithHelmSpec(templateHelmSpec),
			),
			// Should be unchanged (unmanaged)
			"ct2": template.NewClusterTemplate(
				template.WithName("ct2"),
				template.WithNamespace(namespace.Name),
				template.WithHelmSpec(templateHelmSpec),
			),
		}
		stTemplates := map[string]*hmcmirantiscomv1alpha1.ServiceTemplate{
			// Should be created in target namespace
			"test": template.NewServiceTemplate(
				template.WithName("test"),
				template.WithNamespace(utils.DefaultSystemNamespace),
				template.WithHelmSpec(templateHelmSpec),
			),
			// Should be created in target namespace
			"st0": template.NewServiceTemplate(
				template.WithName("st0"),
				template.WithNamespace(utils.DefaultSystemNamespace),
				template.WithHelmSpec(templateHelmSpec),
			),
			// Should be created in target namespace. Template is managed by two chains.
			"st1": template.NewServiceTemplate(
				template.WithName("st1"),
				template.WithNamespace(utils.DefaultSystemNamespace),
				template.WithHelmSpec(templateHelmSpec),
			),
			// Should be unchanged (unmanaged)
			"st2": template.NewServiceTemplate(
				template.WithName("st2"),
				template.WithNamespace(namespace.Name),
				template.WithHelmSpec(templateHelmSpec),
			),
		}

		ctChainNames := []types.NamespacedName{
			getNamespacedChainName(namespace.Name, ctChain1Name),
			getNamespacedChainName(namespace.Name, ctChain2Name),
		}
		stChainNames := []types.NamespacedName{
			getNamespacedChainName(namespace.Name, stChain1Name),
			getNamespacedChainName(namespace.Name, stChain2Name),
		}

		supportedClusterTemplates := map[string][]hmcmirantiscomv1alpha1.SupportedTemplate{
			ctChain1Name: {
				{Name: "test"},
				{Name: "ct0"},
				{Name: "ct1"},
			},
			ctChain2Name: {
				{Name: "ct1"},
			},
		}
		supportedServiceTemplates := map[string][]hmcmirantiscomv1alpha1.SupportedTemplate{
			stChain1Name: {
				{Name: "test"},
				{Name: "st0"},
				{Name: "st1"},
			},
			stChain2Name: {
				{Name: "st1"},
			},
		}

		ctChainUIDs := make(map[string]types.UID)
		stChainUIDs := make(map[string]types.UID)

		BeforeEach(func() {
			By("creating the system and test namespaces")
			for _, ns := range []string{namespace.Name, utils.DefaultSystemNamespace} {
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: ns}, &corev1.Namespace{}); errors.IsNotFound(err) {
					Expect(k8sClient.Create(ctx, &corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name: ns,
						},
					})).To(Succeed())
				}
			}

			By("creating the custom resources for the Kind ClusterTemplateChain")
			for _, chain := range ctChainNames {
				clusterTemplateChain := &hmcmirantiscomv1alpha1.ClusterTemplateChain{}
				err := k8sClient.Get(ctx, chain, clusterTemplateChain)
				if err != nil && errors.IsNotFound(err) {
					resource := &hmcmirantiscomv1alpha1.ClusterTemplateChain{
						ObjectMeta: metav1.ObjectMeta{
							Name:      chain.Name,
							Namespace: chain.Namespace,
							Labels:    map[string]string{hmcmirantiscomv1alpha1.GenericComponentLabelName: hmcmirantiscomv1alpha1.GenericComponentLabelValueHMC},
						},
						Spec: hmcmirantiscomv1alpha1.TemplateChainSpec{SupportedTemplates: supportedClusterTemplates[chain.Name]},
					}
					Expect(k8sClient.Create(ctx, resource)).To(Succeed())
				}
			}
			By("creating the custom resources for the Kind ServiceTemplateChain")
			for _, chain := range stChainNames {
				serviceTemplateChain := &hmcmirantiscomv1alpha1.ServiceTemplateChain{}
				err := k8sClient.Get(ctx, chain, serviceTemplateChain)
				if err != nil && errors.IsNotFound(err) {
					resource := &hmcmirantiscomv1alpha1.ServiceTemplateChain{
						ObjectMeta: metav1.ObjectMeta{
							Name:      chain.Name,
							Namespace: chain.Namespace,
							Labels:    map[string]string{hmcmirantiscomv1alpha1.GenericComponentLabelName: hmcmirantiscomv1alpha1.GenericComponentLabelValueHMC},
						},
						Spec: hmcmirantiscomv1alpha1.TemplateChainSpec{SupportedTemplates: supportedServiceTemplates[chain.Name]},
					}
					Expect(k8sClient.Create(ctx, resource)).To(Succeed())
				}
			}
			By("creating the custom resource for the Kind ClusterTemplate")
			for name, template := range ctTemplates {
				ct := &hmcmirantiscomv1alpha1.ClusterTemplate{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: utils.DefaultSystemNamespace}, ct)
				if err != nil && errors.IsNotFound(err) {
					if template.Labels == nil {
						template.Labels = make(map[string]string)
					}
					template.Labels[hmcmirantiscomv1alpha1.GenericComponentLabelName] = hmcmirantiscomv1alpha1.GenericComponentLabelValueHMC
					template.Spec.Providers = ctProviders
					Expect(k8sClient.Create(ctx, template)).To(Succeed())
				}
				template.Status.ChartRef = chartRef
				Expect(k8sClient.Status().Update(ctx, template)).To(Succeed())
			}
			By("creating the custom resource for the Kind ServiceTemplate")
			for name, template := range stTemplates {
				st := &hmcmirantiscomv1alpha1.ServiceTemplate{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: utils.DefaultSystemNamespace}, st)
				if err != nil && errors.IsNotFound(err) {
					if template.Labels == nil {
						template.Labels = make(map[string]string)
					}
					template.Labels[hmcmirantiscomv1alpha1.GenericComponentLabelName] = hmcmirantiscomv1alpha1.GenericComponentLabelValueHMC
					template.Spec.Providers = stProviders
					Expect(k8sClient.Create(ctx, template)).To(Succeed())
				}
				template.Status.ChartRef = chartRef
				Expect(k8sClient.Status().Update(ctx, template)).To(Succeed())
			}
		})

		AfterEach(func() {
			templateChainReconciler := TemplateChainReconciler{
				Client:          mgrClient,
				SystemNamespace: utils.DefaultSystemNamespace,
			}

			for _, chain := range ctChainNames {
				clusterTemplateChainResource := &hmcmirantiscomv1alpha1.ClusterTemplateChain{}
				err := k8sClient.Get(ctx, chain, clusterTemplateChainResource)
				Expect(err).NotTo(HaveOccurred())

				By("Cleanup the specific resource instance ClusterTemplateChain")
				Expect(k8sClient.Delete(ctx, clusterTemplateChainResource)).To(Succeed())

				// Running reconcile to delete the ClusterTemplateChain
				templateChainReconciler.templateKind = hmcmirantiscomv1alpha1.ClusterTemplateKind
				clusterTemplateChainReconciler := &ClusterTemplateChainReconciler{TemplateChainReconciler: templateChainReconciler}
				_, err = clusterTemplateChainReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: chain})
				Expect(err).NotTo(HaveOccurred())

				Eventually(k8sClient.Get, 1*time.Minute, 5*time.Second).WithArguments(ctx, chain, clusterTemplateChainResource).Should(HaveOccurred())
			}
			for _, template := range []*hmcmirantiscomv1alpha1.ClusterTemplate{ctTemplates["test"], ctTemplates["ct0"], ctTemplates["ct2"]} {
				Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, template))).To(Succeed())
			}

			for _, chain := range stChainNames {
				serviceTemplateChainResource := &hmcmirantiscomv1alpha1.ServiceTemplateChain{}
				err := k8sClient.Get(ctx, chain, serviceTemplateChainResource)
				Expect(err).NotTo(HaveOccurred())

				By("Cleanup the specific resource instance ServiceTemplateChain")
				Expect(k8sClient.Delete(ctx, serviceTemplateChainResource)).To(Succeed())

				// Running reconcile to delete the ServiceTemplateChain
				templateChainReconciler.templateKind = hmcmirantiscomv1alpha1.ServiceTemplateKind
				serviceTemplateChainReconciler := &ServiceTemplateChainReconciler{TemplateChainReconciler: templateChainReconciler}
				_, err = serviceTemplateChainReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: chain})
				Expect(err).NotTo(HaveOccurred())

				Eventually(k8sClient.Get, 1*time.Minute, 5*time.Second).WithArguments(ctx, chain, serviceTemplateChainResource).Should(HaveOccurred())
			}
			for _, template := range []*hmcmirantiscomv1alpha1.ServiceTemplate{stTemplates["test"], stTemplates["st0"], stTemplates["st2"]} {
				Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, template))).To(Succeed())
			}

			By("Cleanup the namespace")
			err := k8sClient.Get(ctx, types.NamespacedName{Name: namespace.Name}, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, namespace))).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Get unmanaged templates before the reconciliation to verify it wasn't changed")
			ctUnmanagedBefore := &hmcmirantiscomv1alpha1.ClusterTemplate{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace.Name, Name: "ct2"}, ctUnmanagedBefore)
			Expect(err).NotTo(HaveOccurred())
			stUnmanagedBefore := &hmcmirantiscomv1alpha1.ServiceTemplate{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace.Name, Name: "st2"}, stUnmanagedBefore)
			Expect(err).NotTo(HaveOccurred())

			templateChainReconciler := TemplateChainReconciler{
				Client:          mgrClient,
				SystemNamespace: utils.DefaultSystemNamespace,
			}
			By("Reconciling the ClusterTemplateChain resources")
			for _, chain := range ctChainNames {
				templateChainReconciler.templateKind = hmcmirantiscomv1alpha1.ClusterTemplateKind
				clusterTemplateChainReconciler := &ClusterTemplateChainReconciler{TemplateChainReconciler: templateChainReconciler}
				_, err = clusterTemplateChainReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: chain})
				Expect(err).NotTo(HaveOccurred())

				ctChain := &hmcmirantiscomv1alpha1.ClusterTemplateChain{}
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}, ctChain)
				ctChainUIDs[ctChain.Name] = ctChain.UID
				Expect(err).NotTo(HaveOccurred())
			}

			By("Reconciling the ServiceTemplateChain resources")
			for _, chain := range stChainNames {
				templateChainReconciler.templateKind = hmcmirantiscomv1alpha1.ServiceTemplateKind
				serviceTemplateChainReconciler := &ServiceTemplateChainReconciler{TemplateChainReconciler: templateChainReconciler}
				_, err = serviceTemplateChainReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: chain})
				Expect(err).NotTo(HaveOccurred())

				stChain := &hmcmirantiscomv1alpha1.ServiceTemplateChain{}
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}, stChain)
				stChainUIDs[stChain.Name] = stChain.UID
				Expect(err).NotTo(HaveOccurred())
			}

			/*
				Expected state after Reconcile:
					* test-chains/test - should be created
					* test-chains/ct0 - should be created
					* test-chains/ct2 - should be unchanged (unmanaged by HMC)

					* test-chains/test - should be created
					* test-chains/st0 - should be created
					* test-chains/st2 - should be unchanged (unmanaged by HMC)
			*/

			ct1OwnerRef := metav1.OwnerReference{
				APIVersion: hmcmirantiscomv1alpha1.GroupVersion.String(),
				Kind:       hmcmirantiscomv1alpha1.ClusterTemplateChainKind,
				Name:       ctChain1Name,
				UID:        ctChainUIDs[ctChain1Name],
			}
			ct2OwnerRef := metav1.OwnerReference{
				APIVersion: hmcmirantiscomv1alpha1.GroupVersion.String(),
				Kind:       hmcmirantiscomv1alpha1.ClusterTemplateChainKind,
				Name:       ctChain2Name,
				UID:        ctChainUIDs[ctChain2Name],
			}
			st1OwnerRef := metav1.OwnerReference{
				APIVersion: hmcmirantiscomv1alpha1.GroupVersion.String(),
				Kind:       hmcmirantiscomv1alpha1.ServiceTemplateChainKind,
				Name:       stChain1Name,
				UID:        stChainUIDs[stChain1Name],
			}
			st2OwnerRef := metav1.OwnerReference{
				APIVersion: hmcmirantiscomv1alpha1.GroupVersion.String(),
				Kind:       hmcmirantiscomv1alpha1.ServiceTemplateChainKind,
				Name:       stChain2Name,
				UID:        stChainUIDs[stChain2Name],
			}
			verifyClusterTemplateCreated(ctx, namespace.Name, "test", ct1OwnerRef)
			verifyClusterTemplateCreated(ctx, namespace.Name, "ct0")

			verifyClusterTemplateCreated(ctx, namespace.Name, "ct1", ct1OwnerRef, ct2OwnerRef)

			verifyObjectUnchanged(ctx, namespace.Name, ctUnmanagedBefore, ctTemplates["ct2"])

			verifyServiceTemplateCreated(ctx, namespace.Name, "test", st1OwnerRef)
			verifyServiceTemplateCreated(ctx, namespace.Name, "st0")

			verifyServiceTemplateCreated(ctx, namespace.Name, "st1", st1OwnerRef, st2OwnerRef)

			verifyObjectUnchanged(ctx, namespace.Name, stUnmanagedBefore, stTemplates["st2"])
		})
	})
})

func getNamespacedChainName(namespace, name string) types.NamespacedName {
	return types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}
}

func verifyObjectCreated(ctx context.Context, namespace string, obj crclient.Object) {
	By(fmt.Sprintf("Verifying existence of %s/%s", namespace, obj.GetName()))
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: obj.GetName()}, obj)
	Expect(err).NotTo(HaveOccurred())
	checkHMCManagedLabelExistence(obj.GetLabels())
}

func verifyObjectDeleted(ctx context.Context, namespace string, obj crclient.Object) {
	By(fmt.Sprintf("Verifying %s/%s is deleted", namespace, obj.GetName()))
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: obj.GetName()}, obj)
	Expect(err).To(HaveOccurred())
}

func verifyObjectUnchanged(ctx context.Context, namespace string, oldObj, newObj crclient.Object) {
	By(fmt.Sprintf("Verifying %s/%s is unchanged", namespace, newObj.GetName()))
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: newObj.GetName()}, newObj)
	Expect(err).NotTo(HaveOccurred())
	Expect(oldObj).To(Equal(newObj))
	Expect(newObj.GetLabels()).NotTo(HaveKeyWithValue(hmcmirantiscomv1alpha1.HMCManagedLabelKey, hmcmirantiscomv1alpha1.HMCManagedLabelValue))
}

func verifyOwnerReferenceExistence(obj crclient.Object, ownerRef metav1.OwnerReference) {
	By(fmt.Sprintf("Verifying owner reference existence on %s/%s", obj.GetNamespace(), obj.GetName()))
	Expect(obj.GetOwnerReferences()).To(ContainElement(ownerRef))
}

func checkHMCManagedLabelExistence(labels map[string]string) {
	Expect(labels).To(HaveKeyWithValue(hmcmirantiscomv1alpha1.HMCManagedLabelKey, hmcmirantiscomv1alpha1.HMCManagedLabelValue))
}

func verifyClusterTemplateCreated(ctx context.Context, namespace, name string, ownerRef ...metav1.OwnerReference) {
	By(fmt.Sprintf("Verifying the ClusterTemplate %s/%s", namespace, name))
	source := &hmcmirantiscomv1alpha1.ClusterTemplate{}
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: utils.DefaultSystemNamespace, Name: name}, source)
	Expect(err).NotTo(HaveOccurred())

	target := &hmcmirantiscomv1alpha1.ClusterTemplate{}
	err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: source.Name}, target)
	Expect(err).NotTo(HaveOccurred())

	expectedSpec := source.Spec
	expectedSpec.Helm.ChartSpec = nil
	expectedSpec.Helm.ChartRef = source.Status.ChartRef
	Expect(expectedSpec).To(Equal(target.Spec))

	checkHMCManagedLabelExistence(target.GetLabels())
	for _, or := range ownerRef {
		verifyOwnerReferenceExistence(target, or)
	}
}

func verifyServiceTemplateCreated(ctx context.Context, namespace, name string, ownerRef ...metav1.OwnerReference) {
	By(fmt.Sprintf("Verifying the ServiceTemplate %s/%s", namespace, name))
	source := &hmcmirantiscomv1alpha1.ServiceTemplate{}
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: utils.DefaultSystemNamespace, Name: name}, source)
	Expect(err).NotTo(HaveOccurred())

	target := &hmcmirantiscomv1alpha1.ServiceTemplate{}
	err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, target)
	Expect(err).NotTo(HaveOccurred())

	expectedSpec := source.Spec
	expectedSpec.Helm.ChartSpec = nil
	expectedSpec.Helm.ChartRef = source.Status.ChartRef
	Expect(expectedSpec).To(Equal(target.Spec))

	checkHMCManagedLabelExistence(target.GetLabels())
	for _, or := range ownerRef {
		verifyOwnerReferenceExistence(target, or)
	}
}
