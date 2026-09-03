// Copyright 2026
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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/serviceset"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

// This suite exercises NamespacedMultiClusterServiceReconciler.Reconcile end-to-end against
// envtest, focused on what genuinely differs from MultiClusterService (already covered by
// multiclusterservice_controller_test.go): its own-namespace scoping for both ServiceTemplate
// resolution and ClusterDeployment matching, selfManagement being ignored, and dependency
// validation being namespace-scoped. Generic reconcile behavior already exercised via
// MultiClusterService (upgrade paths, condition bookkeeping, blocked-cluster accounting, etc.) is
// not re-derived here, since MultiClusterServiceCommonReconciler implements it once for both types.
var _ = Describe("NamespacedMultiClusterService Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			nmcsNamespace         = "nmcs-test-ns"
			otherNamespace        = "nmcs-test-other-ns"
			serviceTemplate       = "nmcs-test-service-template"
			nmcsName              = "test-namespacedmulticlusterservice"
			clusterDeploymentName = "test-nmcs-clusterdeployment"
			matchLabel            = "test"
		)

		namespace := &corev1.Namespace{}
		otherNS := &corev1.Namespace{}
		svcTemplate := &kcmv1.ServiceTemplate{}
		nmcs := &kcmv1.NamespacedMultiClusterService{}
		clusterDeployment := kcmv1.ClusterDeployment{}

		svcTemplateRef := types.NamespacedName{Namespace: nmcsNamespace, Name: serviceTemplate}
		nmcsRef := types.NamespacedName{Namespace: nmcsNamespace, Name: nmcsName}
		serviceSetKey := types.NamespacedName{}

		newReconciler := func() *NamespacedMultiClusterServiceReconciler {
			return &NamespacedMultiClusterServiceReconciler{
				MultiClusterServiceCommonReconciler{
					Client:          mgrClient,
					timeFunc:        func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
					SystemNamespace: testSystemNamespace,
				},
			}
		}

		BeforeEach(func() {
			By("creating the system namespace, needed by the cross-namespace template test below")
			sysNS := &corev1.Namespace{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: testSystemNamespace}, sysNS)
			if err != nil && apierrors.IsNotFound(err) {
				sysNS = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testSystemNamespace}}
				Expect(k8sClient.Create(ctx, sysNS)).To(Succeed())
			}

			By("creating the NamespacedMultiClusterService's own namespace")
			err = k8sClient.Get(ctx, types.NamespacedName{Name: nmcsNamespace}, namespace)
			if err != nil && apierrors.IsNotFound(err) {
				namespace = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nmcsNamespace}}
				Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			}

			By("creating a second namespace used to prove cross-namespace isolation")
			err = k8sClient.Get(ctx, types.NamespacedName{Name: otherNamespace}, otherNS)
			if err != nil && apierrors.IsNotFound(err) {
				otherNS = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: otherNamespace}}
				Expect(k8sClient.Create(ctx, otherNS)).To(Succeed())
			}

			By("creating a valid ServiceTemplate in the NamespacedMultiClusterService's own namespace")
			err = k8sClient.Get(ctx, svcTemplateRef, svcTemplate)
			if err != nil && apierrors.IsNotFound(err) {
				svcTemplate = &kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      serviceTemplate,
						Namespace: nmcsNamespace,
						Labels:    map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
					},
					Spec: kcmv1.ServiceTemplateSpec{
						// Resources-only, no Helm chart machinery needed: ServicesHaveValidTemplates
						// only checks existence + .Status.Valid, and fillServiceVersions falls back
						// to the template name as the effective version when none is set.
						Resources: &kcmv1.SourceSpec{
							DeploymentType: "Local",
							LocalSourceRef: &kcmv1.LocalSourceRef{Kind: "ConfigMap", Name: "manifests"},
						},
					},
				}
				Expect(k8sClient.Create(ctx, svcTemplate)).To(Succeed())
				svcTemplate.Status = kcmv1.ServiceTemplateStatus{
					TemplateStatusCommon: kcmv1.TemplateStatusCommon{
						TemplateValidationStatus: kcmv1.TemplateValidationStatus{Valid: true},
					},
				}
				Expect(k8sClient.Status().Update(ctx, svcTemplate)).To(Succeed())
			}

			By("creating a matching ClusterDeployment in the same namespace")
			clusterDeployment = kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: clusterDeploymentName + "-",
					Namespace:    nmcsNamespace,
					Labels:       map[string]string{matchLabel: "true"},
				},
				Spec: kcmv1.ClusterDeploymentSpec{
					Template:   "sample-template",
					Credential: "sample-credential",
					Config:     &apiextv1.JSON{Raw: []byte(`{"foo":"bar"}`)},
				},
			}
			Expect(k8sClient.Create(ctx, &clusterDeployment)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &clusterDeployment)

			serviceSetKey = types.NamespacedName{
				Namespace: nmcsNamespace,
				Name:      serviceset.ObjectKey(testSystemNamespace, &clusterDeployment, &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: nmcsNamespace, Name: nmcsName}}).Name,
			}

			By("creating NamespacedMultiClusterService")
			err = k8sClient.Get(ctx, nmcsRef, nmcs)
			if err != nil && apierrors.IsNotFound(err) {
				nmcs = &kcmv1.NamespacedMultiClusterService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      nmcsName,
						Namespace: nmcsNamespace,
						Labels:    map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
						// Preset so Reconcile does not stop after adding it, keeping each It a
						// single-pass reconcile - mirrors the MultiClusterService suite's pattern.
						Finalizers: []string{kcmv1.NamespacedMultiClusterServiceFinalizer},
					},
					Spec: kcmv1.MultiClusterServiceSpec{
						ClusterSelector: metav1.LabelSelector{MatchLabels: map[string]string{matchLabel: "true"}},
						ServiceSpec: kcmv1.ServiceSpec{
							Provider: kcmv1.StateManagementProviderConfig{Name: kubeutil.DefaultStateManagementProvider},
							Services: []kcmv1.Service{{Template: serviceTemplate, Name: "svc", Namespace: "ns"}},
						},
					},
				}
				Expect(k8sClient.Create(ctx, nmcs)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("cleaning up")

			nmcsToDelete := &kcmv1.NamespacedMultiClusterService{}
			if err := k8sClient.Get(ctx, nmcsRef, nmcsToDelete); err == nil {
				if len(nmcsToDelete.Finalizers) > 0 {
					nmcsToDelete.Finalizers = nil
					Expect(k8sClient.Update(ctx, nmcsToDelete)).To(Succeed())
				}
				Expect(k8sClient.Delete(ctx, nmcsToDelete)).To(Succeed())
				Eventually(func() bool {
					return apierrors.IsNotFound(k8sClient.Get(ctx, nmcsRef, &kcmv1.NamespacedMultiClusterService{}))
				}).Should(BeTrue())
			} else if !apierrors.IsNotFound(err) {
				Expect(err).ToNot(HaveOccurred())
			}

			if err := k8sClient.Get(ctx, svcTemplateRef, svcTemplate); err == nil {
				Expect(k8sClient.Delete(ctx, svcTemplate)).To(Succeed())
			}

			sset := &kcmv1.ServiceSet{}
			if err := k8sClient.Get(ctx, serviceSetKey, sset); err == nil {
				Expect(k8sClient.Delete(ctx, sset)).To(Succeed())
			}
		})

		It("should successfully reconcile: create a namespace-scoped ServiceSet and report ClusterInReadyState", func() {
			reconciler := newReconciler()

			By("reconciling")
			Eventually(func(g Gomega) {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nmcsRef})
				g.Expect(err).NotTo(HaveOccurred())

				sset := &kcmv1.ServiceSet{}
				g.Expect(k8sClient.Get(ctx, serviceSetKey, sset)).To(Succeed())
				g.Expect(sset.Spec.NamespacedMultiClusterService).To(Equal(nmcsNamespace + "/" + nmcsName))
				g.Expect(sset.Spec.MultiClusterService).To(BeEmpty())
				g.Expect(sset.Spec.Cluster).To(Equal(clusterDeployment.Name))
				// The ClusterDeployment owns this ServiceSet, not the NamespacedMultiClusterService -
				// see serviceset.Builder.Build, which makes the ClusterDeployment the owner whenever
				// one is set. The NamespacedMultiClusterService reference is still recorded, just
				// not as the owner - checked above via .Spec.NamespacedMultiClusterService.
				g.Expect(sset.OwnerReferences).To(ContainElement(SatisfyAll(
					HaveField("Kind", kcmv1.ClusterDeploymentKind),
					HaveField("Name", clusterDeployment.Name),
				)))
			}).Should(Succeed())

			By("simulating the ServiceSet becoming Deployed, as the (not-running-in-this-suite) ServiceSet controller normally would")
			Eventually(func(g Gomega) {
				sset := &kcmv1.ServiceSet{}
				g.Expect(k8sClient.Get(ctx, serviceSetKey, sset)).To(Succeed())
				sset.Status.Deployed = true
				g.Expect(k8sClient.Status().Update(ctx, sset)).To(Succeed())
			}).Should(Succeed())

			By("reconciling again and observing ClusterInReadyState reach 1/1")
			Eventually(func(g Gomega) {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nmcsRef})
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(k8sClient.Get(ctx, nmcsRef, nmcs)).To(Succeed())
				g.Expect(nmcs.Status.Conditions).To(ContainElement(SatisfyAll(
					HaveField("Type", kcmv1.ClusterInReadyStateCondition),
					HaveField("Status", metav1.ConditionTrue),
					HaveField("Message", "1/1"),
				)))
			}).Should(Succeed())
		})

		It("should fail template validation when the referenced ServiceTemplate lives outside its own namespace", func() {
			By("moving the NamespacedMultiClusterService's service to a template that only exists in the system namespace")
			sysTemplate := &kcmv1.ServiceTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceTemplate + "-system-only",
					Namespace: testSystemNamespace,
					Labels:    map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
				},
				Spec: kcmv1.ServiceTemplateSpec{
					Resources: &kcmv1.SourceSpec{
						DeploymentType: "Local",
						LocalSourceRef: &kcmv1.LocalSourceRef{Kind: "ConfigMap", Name: "manifests"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, sysTemplate)).To(Succeed())
			sysTemplate.Status.Valid = true
			Expect(k8sClient.Status().Update(ctx, sysTemplate)).To(Succeed())
			DeferCleanup(k8sClient.Delete, sysTemplate)

			Expect(k8sClient.Get(ctx, nmcsRef, nmcs)).To(Succeed())
			nmcs.Spec.ServiceSpec.Services = []kcmv1.Service{{Template: sysTemplate.Name, Name: "svc", Namespace: "ns"}}
			Expect(k8sClient.Update(ctx, nmcs)).To(Succeed())

			reconciler := newReconciler()
			Eventually(func(g Gomega) {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nmcsRef})
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(k8sClient.Get(ctx, nmcsRef, nmcs)).To(Succeed())
				g.Expect(nmcs.Status.Conditions).To(ContainElement(SatisfyAll(
					HaveField("Type", kcmv1.ServicesReferencesValidationCondition),
					HaveField("Status", metav1.ConditionFalse),
				)))

				// Confirms the failure is namespace resolution, not something else: the
				// template is real and valid, just not visible from nmcsNamespace.
				g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Namespace: nmcsNamespace, Name: sysTemplate.Name}, &kcmv1.ServiceTemplate{}))).To(BeTrue())
			}).Should(Succeed())
		})

		It("should ignore selfManagement and never create a management ServiceSet", func() {
			mgmtServiceSetKey := types.NamespacedName{
				Namespace: testSystemNamespace,
				Name:      serviceset.ObjectKey(testSystemNamespace, nil, &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: nmcsNamespace, Name: nmcsName}}).Name,
			}

			Expect(k8sClient.Get(ctx, nmcsRef, nmcs)).To(Succeed())
			nmcs.Spec.ServiceSpec.Provider.SelfManagement = true
			Expect(k8sClient.Update(ctx, nmcs)).To(Succeed())

			reconciler := newReconciler()
			Eventually(func(g Gomega) {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nmcsRef})
				g.Expect(err).NotTo(HaveOccurred())

				// The CD-matched ServiceSet is still created ...
				g.Expect(k8sClient.Get(ctx, serviceSetKey, &kcmv1.ServiceSet{})).To(Succeed())
				// ... but no self-management ServiceSet ever appears, unlike MultiClusterService.
				g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, mgmtServiceSetKey, &kcmv1.ServiceSet{}))).To(BeTrue())
			}).Should(Succeed())
		})

		It("should not match a ClusterDeployment in a different namespace", func() {
			otherCD := kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: clusterDeploymentName + "-other-",
					Namespace:    otherNamespace,
					Labels:       map[string]string{matchLabel: "true"},
				},
				Spec: kcmv1.ClusterDeploymentSpec{
					Template:   "sample-template",
					Credential: "sample-credential",
					Config:     &apiextv1.JSON{Raw: []byte(`{"foo":"bar"}`)},
				},
			}
			Expect(k8sClient.Create(ctx, &otherCD)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &otherCD)

			reconciler := newReconciler()
			Eventually(func(g Gomega) {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nmcsRef})
				g.Expect(err).NotTo(HaveOccurred())

				// Only the same-namespace CD's ServiceSet exists.
				g.Expect(k8sClient.Get(ctx, serviceSetKey, &kcmv1.ServiceSet{})).To(Succeed())

				otherCDSSKey := client.ObjectKey{
					Namespace: otherNamespace,
					Name:      serviceset.ObjectKey(testSystemNamespace, &otherCD, &kcmv1.NamespacedMultiClusterService{ObjectMeta: metav1.ObjectMeta{Namespace: nmcsNamespace, Name: nmcsName}}).Name,
				}
				g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, otherCDSSKey, &kcmv1.ServiceSet{}))).To(BeTrue())

				g.Expect(k8sClient.Get(ctx, nmcsRef, nmcs)).To(Succeed())
				g.Expect(nmcs.Status.Conditions).To(ContainElement(SatisfyAll(
					HaveField("Type", kcmv1.ClusterInReadyStateCondition),
					HaveField("Message", "0/1"),
				)))
			}).Should(Succeed())
		})

		It("should block deletion while another NamespacedMultiClusterService in the same namespace depends on it, and allow it once that dependency is removed", func() {
			dependent := &kcmv1.NamespacedMultiClusterService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nmcsName + "-dependent",
					Namespace: nmcsNamespace,
					Labels:    map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
				},
				Spec: kcmv1.MultiClusterServiceSpec{DependsOn: []string{nmcsName}},
			}
			Expect(k8sClient.Create(ctx, dependent)).To(Succeed())
			DeferCleanup(func() {
				fresh := &kcmv1.NamespacedMultiClusterService{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dependent), fresh); err == nil {
					Expect(k8sClient.Delete(ctx, fresh)).To(Succeed())
				}
			})

			reconciler := newReconciler()

			By("deleting the NamespacedMultiClusterService while the dependent still exists")
			Expect(k8sClient.Delete(ctx, nmcs)).To(Succeed())

			Eventually(func(g Gomega) {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nmcsRef})
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("depend on it"))

				fresh := &kcmv1.NamespacedMultiClusterService{}
				g.Expect(k8sClient.Get(ctx, nmcsRef, fresh)).To(Succeed(), "should still exist, blocked from deletion")
				g.Expect(fresh.Status.Conditions).To(ContainElement(SatisfyAll(
					HaveField("Type", kcmv1.MultiClusterServiceDependencyValidationCondition),
					HaveField("Status", metav1.ConditionFalse),
				)))
			}).Should(Succeed())

			By("removing the dependency and reconciling again: deletion proceeds")
			Expect(k8sClient.Delete(ctx, dependent)).To(Succeed())

			Eventually(func(g Gomega) {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: nmcsRef})
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, nmcsRef, &kcmv1.NamespacedMultiClusterService{}))).To(BeTrue())
			}).Should(Succeed())
		})
	})
})
