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
	"fmt"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	fluxconditions "github.com/fluxcd/pkg/runtime/conditions"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	capioperator "sigs.k8s.io/cluster-api-operator/api/v1alpha2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/utils"
)

var _ = Describe("Management Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		management := &kcmv1.Management{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Management")
			err := k8sClient.Get(ctx, typeNamespacedName, management)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &kcmv1.Management{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: kcmv1.ManagementSpec{
						Release: "test-release-name",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &kcmv1.Management{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Management")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			// NOTE: this node just checks that the finalizer has been set
			By("Reconciling the created resource")
			controllerReconciler := &ManagementReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should successfully delete providers components on its removal", func() {
			const (
				mgmtName = "test-management-name-mgmt-removal"

				providerTemplateName              = "test-provider-template-name-mgmt-removal"
				providerTemplateUID               = types.UID("some-uid")
				providerTemplateRequiredComponent = "test-provider-for-required-mgmt-removal"

				someComponentName = "test-component-name-mgmt-removal"

				helmChartName, helmChartNamespace = "helm-chart-test-name", utils.DefaultSystemNamespace

				helmReleaseName          = someComponentName // WARN: helm release name should be equal to the component name
				helmReleaseNamespace     = utils.DefaultSystemNamespace
				someOtherHelmReleaseName = "cluster-deployment-release-name"

				timeout  = time.Second * 10
				interval = time.Millisecond * 250
			)

			coreComponents := map[string]struct {
				templateName    string
				helmReleaseName string
			}{
				kcmv1.CoreKCMName: {
					templateName:    "test-release-kcm",
					helmReleaseName: kcmv1.CoreKCMName,
				},
				kcmv1.CoreCAPIName: {
					templateName:    "test-release-capi",
					helmReleaseName: kcmv1.CoreCAPIName,
				},
			}

			// NOTE: other tests for some reason are manipulating with the NS globally and interfer with each other,
			// so try to avoid depending on their implementation ignoring its removal
			By("Creating the kcm-system namespace")
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: utils.DefaultSystemNamespace,
				},
			}))).To(Succeed())
			Eventually(k8sClient.Get).WithArguments(ctx, client.ObjectKey{Name: utils.DefaultSystemNamespace}, &corev1.Namespace{}).
				WithTimeout(10 * time.Second).WithPolling(250 * time.Millisecond).Should(Succeed())

			By("Creating the Release object")
			release := &kcmv1.Release{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-release-name",
				},
				Spec: kcmv1.ReleaseSpec{
					Version: "test-version",
					KCM:     kcmv1.CoreProviderTemplate{Template: coreComponents[kcmv1.CoreKCMName].templateName},
					CAPI:    kcmv1.CoreProviderTemplate{Template: coreComponents[kcmv1.CoreCAPIName].templateName},
				},
			}
			Expect(k8sClient.Create(ctx, release)).To(Succeed())
			Eventually(k8sClient.Get).WithArguments(ctx, client.ObjectKeyFromObject(release), release).
				WithTimeout(10 * time.Second).WithPolling(250 * time.Millisecond).Should(Succeed())

			By("Creating a ProviderTemplate object for other required components")
			providerTemplateRequired := &kcmv1.ProviderTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name: providerTemplateRequiredComponent,
				},
				Spec: kcmv1.ProviderTemplateSpec{
					Helm: kcmv1.HelmSpec{
						ChartSpec: &sourcev1.HelmChartSpec{
							Chart:   "required-chart",
							Version: "required-version",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, providerTemplateRequired)).To(Succeed())
			providerTemplateRequired.Status = kcmv1.ProviderTemplateStatus{
				TemplateStatusCommon: kcmv1.TemplateStatusCommon{
					TemplateValidationStatus: kcmv1.TemplateValidationStatus{
						Valid: true,
					},
					ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
						Kind:      sourcev1.HelmChartKind,
						Name:      "required-chart",
						Namespace: helmChartNamespace,
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, providerTemplateRequired)).To(Succeed())

			By("Creating a HelmRelease object for the removed component")
			helmRelease := &helmcontrollerv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      helmReleaseName,
					Namespace: helmReleaseNamespace,
					Labels: map[string]string{
						kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
					},
				},
				Spec: helmcontrollerv2.HelmReleaseSpec{
					ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
						Kind:      sourcev1.HelmChartKind,
						Name:      helmChartName,
						Namespace: helmChartNamespace,
					},
				},
			}
			Expect(k8sClient.Create(ctx, helmRelease)).To(Succeed())

			By("Creating a HelmRelease object for some cluster deployment")
			someOtherHelmRelease := &helmcontrollerv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      someOtherHelmReleaseName,
					Namespace: helmReleaseNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: kcmv1.GroupVersion.String(),
							Kind:       kcmv1.ClusterDeploymentKind,
							Name:       "any-owner-ref",
							UID:        types.UID("some-owner-uid"),
						},
					},
					Labels: map[string]string{
						kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
					},
				},
				Spec: helmcontrollerv2.HelmReleaseSpec{
					ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
						Kind:      sourcev1.HelmChartKind,
						Name:      "any-chart-name",
						Namespace: helmChartNamespace,
					},
				},
			}
			Expect(k8sClient.Create(ctx, someOtherHelmRelease)).To(Succeed())

			By("Creating a Management object with removed component in the spec and containing it in the status")
			mgmt := &kcmv1.Management{
				ObjectMeta: metav1.ObjectMeta{
					Name:       mgmtName,
					Finalizers: []string{kcmv1.ManagementFinalizer},
				},
				Spec: kcmv1.ManagementSpec{
					Release: release.Name,
					Core: &kcmv1.Core{
						KCM: kcmv1.Component{
							Template: providerTemplateRequiredComponent,
						},
						CAPI: kcmv1.Component{
							Template: providerTemplateRequiredComponent,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, mgmt)).To(Succeed())
			mgmt.Status = kcmv1.ManagementStatus{
				AvailableProviders: []string{someComponentName},
				Components: map[string]kcmv1.ComponentStatus{
					someComponentName: {Template: providerTemplateName},
				},
			}
			Expect(k8sClient.Status().Update(ctx, mgmt)).To(Succeed())

			By("Checking created objects have expected spec and status")
			Eventually(func() error {
				// Management
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmt), mgmt); err != nil {
					return err
				}
				if l := len(mgmt.Status.AvailableProviders); l != 1 {
					return fmt.Errorf("expected .status.availableProviders length to be exactly 1, got %d", l)
				}
				if l := len(mgmt.Status.Components); l != 1 {
					return fmt.Errorf("expected .status.components length to be exactly 2, got %d", l)
				}
				if v := mgmt.Status.Components[someComponentName]; v.Template != providerTemplateName {
					return fmt.Errorf("expected .status.components[%s] template be %s, got %s", someComponentName, providerTemplateName, v.Template)
				}

				// HelmReleases
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: someOtherHelmReleaseName, Namespace: helmReleaseNamespace}, &helmcontrollerv2.HelmRelease{}); err != nil {
					return err
				}

				return k8sClient.Get(ctx, client.ObjectKey{Name: helmReleaseName, Namespace: helmReleaseNamespace}, &helmcontrollerv2.HelmRelease{})
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

			By("Reconciling the Management object")
			controllerReconciler := &ManagementReconciler{
				Client:          k8sClient,
				Scheme:          k8sClient.Scheme(),
				SystemNamespace: utils.DefaultSystemNamespace,
				DynamicClient:   dynamicClient,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(mgmt),
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking the HelmRelease objects have been removed")
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(helmRelease), &helmcontrollerv2.HelmRelease{}))
			}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

			By("Checking the Management object does not have the removed component in its spec")
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmt), mgmt)).To(Succeed())
			Expect(mgmt.Status.AvailableProviders).To(BeEquivalentTo(kcmv1.Providers{"infrastructure-internal"}))

			By("Checking the other (managed) helm-release has not been removed")
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(someOtherHelmRelease), someOtherHelmRelease)).To(Succeed())

			By("Checking the Management components status is populated")
			Expect(mgmt.Status.Components).To(HaveLen(2)) // required: capi, kcm
			Expect(mgmt.Status.Components).To(BeEquivalentTo(map[string]kcmv1.ComponentStatus{
				kcmv1.CoreKCMName: {
					Success:  false,
					Template: providerTemplateRequiredComponent,
					Error:    fmt.Sprintf("HelmRelease %s/%s Ready condition is not updated yet", helmReleaseNamespace, coreComponents[kcmv1.CoreKCMName].helmReleaseName),
				},
				kcmv1.CoreCAPIName: {
					Success:  false,
					Template: providerTemplateRequiredComponent,
					Error:    "Some dependencies are not ready yet. Waiting for kcm",
				},
			}))

			By("Updating kcm HelmRelease with Ready condition")
			helmRelease = &helmcontrollerv2.HelmRelease{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: helmReleaseNamespace,
				Name:      coreComponents[kcmv1.CoreKCMName].helmReleaseName,
			}, helmRelease)).To(Succeed())

			fluxconditions.Set(helmRelease, &metav1.Condition{
				Type:   fluxmeta.ReadyCondition,
				Reason: helmcontrollerv2.InstallSucceededReason,
				Status: metav1.ConditionTrue,
			})
			helmRelease.Status.History = helmcontrollerv2.Snapshots{
				{
					Name:          coreComponents[kcmv1.CoreKCMName].helmReleaseName,
					FirstDeployed: metav1.Now(),
					LastDeployed:  metav1.Now(),
				},
			}
			Expect(k8sClient.Status().Update(ctx, helmRelease)).To(Succeed())

			By("Reconciling the Management object")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(mgmt),
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking the Management components status is populated")
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmt), mgmt)).To(Succeed())
			Expect(mgmt.Status.Components).To(BeEquivalentTo(map[string]kcmv1.ComponentStatus{
				kcmv1.CoreKCMName: {
					Success:  true,
					Template: providerTemplateRequiredComponent,
				},
				kcmv1.CoreCAPIName: {
					Success:  false,
					Template: providerTemplateRequiredComponent,
					Error:    fmt.Sprintf("HelmRelease %s/%s Ready condition is not updated yet", helmReleaseNamespace, coreComponents[kcmv1.CoreCAPIName].helmReleaseName),
				},
			}))

			By("Updating capi HelmRelease with Ready condition")
			helmRelease = &helmcontrollerv2.HelmRelease{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: helmReleaseNamespace,
				Name:      coreComponents[kcmv1.CoreCAPIName].helmReleaseName,
			}, helmRelease)).To(Succeed())

			fluxconditions.Set(helmRelease, &metav1.Condition{
				Type:   fluxmeta.ReadyCondition,
				Reason: helmcontrollerv2.InstallSucceededReason,
				Status: metav1.ConditionTrue,
			})
			helmRelease.Status.History = helmcontrollerv2.Snapshots{
				{
					Name:          coreComponents[kcmv1.CoreCAPIName].helmReleaseName,
					FirstDeployed: metav1.Now(),
					LastDeployed:  metav1.Now(),
				},
			}
			Expect(k8sClient.Status().Update(ctx, helmRelease)).To(Succeed())

			By("Creating Cluster API CoreProvider object")
			coreProvider := &capioperator.CoreProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "capi",
					Namespace: utils.DefaultSystemNamespace,
					Labels: map[string]string{
						"helm.toolkit.fluxcd.io/name": coreComponents["capi"].helmReleaseName,
					},
				},
			}
			Expect(k8sClient.Create(ctx, coreProvider)).To(Succeed())
			coreProvider.Status.Conditions = clusterv1.Conditions{
				{
					Type:               clusterv1.ReadyCondition,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
				},
			}
			Expect(k8sClient.Status().Update(ctx, coreProvider)).To(Succeed())
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(coreProvider), coreProvider); err != nil {
					return err
				}
				if l := len(coreProvider.Status.Conditions); l != 1 {
					return fmt.Errorf("expected .status.conditions length to be exactly 1, got %d", l)
				}
				cond := coreProvider.Status.Conditions[0]
				if cond.Type != clusterv1.ReadyCondition || cond.Status != corev1.ConditionTrue {
					return fmt.Errorf("unexpected status condition: type %s, status %s", cond.Type, cond.Status)
				}

				return nil
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

			By("Reconciling the Management object again to ensure the components status is updated")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(mgmt),
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmt), mgmt)).To(Succeed())
			Expect(mgmt.Status.Components).To(BeEquivalentTo(map[string]kcmv1.ComponentStatus{
				kcmv1.CoreKCMName:  {Success: true, Template: providerTemplateRequiredComponent},
				kcmv1.CoreCAPIName: {Success: true, Template: providerTemplateRequiredComponent},
			}))

			By("Removing the leftover objects")
			mgmt.Finalizers = nil
			Expect(k8sClient.Update(ctx, mgmt)).To(Succeed())
			Expect(k8sClient.Delete(ctx, mgmt)).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmt), &kcmv1.Management{}))
			}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

			Expect(k8sClient.Delete(ctx, release)).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(release), &kcmv1.Release{}))
			}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

			Expect(k8sClient.Delete(ctx, providerTemplateRequired)).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(providerTemplateRequired), &kcmv1.ProviderTemplate{}))
			}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

			Expect(k8sClient.Delete(ctx, someOtherHelmRelease)).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(someOtherHelmRelease), &helmcontrollerv2.HelmRelease{}))
			}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

			coreProvider.Finalizers = nil
			Expect(k8sClient.Update(ctx, coreProvider)).To(Succeed())
			Expect(k8sClient.Delete(ctx, coreProvider)).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(coreProvider), &capioperator.CoreProvider{}))
			}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())
		})
	})
})
