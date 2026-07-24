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
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	fluxconditions "github.com/fluxcd/pkg/runtime/conditions"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	validationcontent "k8s.io/apimachinery/pkg/api/validate/content"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	capioperator "sigs.k8s.io/cluster-api-operator/api/v1alpha2"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
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

				helmChartName, helmChartNamespace = "helm-chart-test-name", kubeutil.DefaultSystemNamespace

				helmReleaseName          = someComponentName // WARN: helm release name should be equal to the component name
				helmReleaseNamespace     = kubeutil.DefaultSystemNamespace
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
					Name: kubeutil.DefaultSystemNamespace,
				},
			}))).To(Succeed())
			Eventually(k8sClient.Get).WithArguments(ctx, client.ObjectKey{Name: kubeutil.DefaultSystemNamespace}, &corev1.Namespace{}).
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
					Labels:     map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
					Finalizers: []string{kcmv1.ManagementFinalizer},
				},
				Spec: kcmv1.ManagementSpec{
					Release: release.Name,
					ComponentsCommonSpec: kcmv1.ComponentsCommonSpec{
						Core: &kcmv1.Core{
							KCM: kcmv1.Component{
								Template: providerTemplateRequiredComponent,
							},
							CAPI: kcmv1.Component{
								Template: providerTemplateRequiredComponent,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, mgmt)).To(Succeed())
			mgmt.Status = kcmv1.ManagementStatus{
				ComponentsCommonStatus: kcmv1.ComponentsCommonStatus{
					AvailableProviders: []string{someComponentName},
					Components: map[string]kcmv1.ComponentStatus{
						someComponentName: {Template: providerTemplateName},
					},
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
				DynamicClient:   dynamicClient,
				SystemNamespace: kubeutil.DefaultSystemNamespace,
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
			const (
				helmReleaseConfigDigest           = "sha256:some_digest"
				helmReleaseSnapshotDeployedStatus = "deployed"
			)
			helmRelease.Status.History = helmcontrollerv2.Snapshots{
				{
					Name:          coreComponents[kcmv1.CoreKCMName].helmReleaseName,
					FirstDeployed: metav1.Now(),
					LastDeployed:  metav1.Now(),
					Status:        helmReleaseSnapshotDeployedStatus,
					ConfigDigest:  helmReleaseConfigDigest,
				},
			}
			helmRelease.Status.LastAttemptedConfigDigest = helmReleaseConfigDigest
			helmRelease.Status.ObservedGeneration = helmRelease.Generation
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

			By("Expecting condition Ready=False Management status")
			cond := meta.FindStatusCondition(mgmt.Status.Conditions, kcmv1.ReadyCondition)
			Expect(cond).NotTo(BeNil(), "Expected Ready condition to exist after reconcile")
			Expect(cond.Status).To(Equal(metav1.ConditionFalse), "Expected Ready to be False")
			Expect(cond.Reason).To(Equal(kcmv1.NotAllComponentsHealthyReason))

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
					Status:        helmReleaseSnapshotDeployedStatus,
					ConfigDigest:  helmReleaseConfigDigest,
				},
			}
			helmRelease.Status.LastAttemptedConfigDigest = helmReleaseConfigDigest
			helmRelease.Status.ObservedGeneration = helmRelease.Generation
			Expect(k8sClient.Status().Update(ctx, helmRelease)).To(Succeed())

			By("Creating Cluster API CoreProvider object")
			coreProvider := &capioperator.CoreProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "capi",
					Namespace: kubeutil.DefaultSystemNamespace,
					Labels: map[string]string{
						"helm.toolkit.fluxcd.io/name": coreComponents["capi"].helmReleaseName,
					},
				},
				Spec: capioperator.CoreProviderSpec{
					ProviderSpec: capioperator.ProviderSpec{
						Version: "v0.0.1",
					},
				},
			}
			Expect(k8sClient.Create(ctx, coreProvider)).To(Succeed())

			coreProvider.Status.ObservedGeneration = coreProvider.Generation
			coreProvider.Status.InstalledVersion = new("v0.0.1")
			coreProvider.Status.Conditions = []metav1.Condition{
				{
					Type:               clusterapiv1.ReadyCondition,
					Status:             metav1.ConditionTrue,
					Reason:             clusterapiv1.ReadyReason,
					Message:            "Provider is ready",
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
				if cond.Type != clusterapiv1.ReadyCondition || cond.Status != metav1.ConditionTrue {
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

			By("Expecting condition Ready=True Management status")
			cond = meta.FindStatusCondition(mgmt.Status.Conditions, kcmv1.ReadyCondition)
			Expect(cond).NotTo(BeNil(), "Expected Ready condition to exist")
			Expect(cond.Status).To(Equal(metav1.ConditionTrue), "Expected Ready to be True")
			Expect(cond.Reason).To(Equal(kcmv1.AllComponentsHealthyReason))

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

	Context("removeCRDsWithSelector", func() {
		const (
			timeout  = time.Second * 10
			interval = time.Millisecond * 250
		)
		newTestCRD := func(group string, lbls map[string]string) *apiextv1.CustomResourceDefinition {
			return &apiextv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "tests." + group,
					Labels: lbls,
				},
				Spec: apiextv1.CustomResourceDefinitionSpec{
					Group: group,
					Names: apiextv1.CustomResourceDefinitionNames{
						Plural:   "tests",
						Singular: "test",
						Kind:     "Test",
					},
					Scope: apiextv1.ClusterScoped,
					Versions: []apiextv1.CustomResourceDefinitionVersion{{
						Name:    "v1",
						Served:  true,
						Storage: true,
						Schema: &apiextv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextv1.JSONSchemaProps{Type: "object"},
						},
					}},
				},
			}
		}

		It("deletes CRDs matching the k0rdent label selector", func() {
			crd := newTestCRD("kcm-cleanup-test.io", map[string]string{
				kcmv1.FluxHelmChartNameKey: kcmv1.CoreKCMName,
			})
			Expect(k8sClient.Create(ctx, crd)).To(Succeed())
			Eventually(k8sClient.Get).WithArguments(ctx, client.ObjectKeyFromObject(crd), &apiextv1.CustomResourceDefinition{}).
				WithTimeout(timeout).WithPolling(interval).Should(Succeed())

			r := &ManagementReconciler{Client: k8sClient}
			sel := labels.SelectorFromSet(map[string]string{kcmv1.FluxHelmChartNameKey: kcmv1.CoreKCMName})
			Expect(r.removeCRDsWithSelector(ctx, sel)).To(Succeed())

			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(crd), &apiextv1.CustomResourceDefinition{}))
			}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())
		})

		It("deletes CRDs matching the CAPI provider label selector", func() {
			crd := newTestCRD("capi-cleanup-test.io", map[string]string{
				clusterapiv1.ProviderNameLabel: "infrastructure-test",
			})
			Expect(k8sClient.Create(ctx, crd)).To(Succeed())
			Eventually(k8sClient.Get).WithArguments(ctx, client.ObjectKeyFromObject(crd), &apiextv1.CustomResourceDefinition{}).
				WithTimeout(timeout).WithPolling(interval).Should(Succeed())

			r := &ManagementReconciler{Client: k8sClient}
			req, err := labels.NewRequirement(clusterapiv1.ProviderNameLabel, selection.Exists, nil)
			Expect(err).NotTo(HaveOccurred())
			sel := labels.NewSelector().Add(*req)
			Expect(r.removeCRDsWithSelector(ctx, sel)).To(Succeed())

			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(crd), &apiextv1.CustomResourceDefinition{}))
			}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())
		})

		It("does not delete CRDs that do not match the selector", func() {
			crd := newTestCRD("no-match-cleanup-test.io", map[string]string{
				"some-other-label": "value",
			})
			Expect(k8sClient.Create(ctx, crd)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, crd)
			})
			Eventually(k8sClient.Get).WithArguments(ctx, client.ObjectKeyFromObject(crd), &apiextv1.CustomResourceDefinition{}).
				WithTimeout(timeout).WithPolling(interval).Should(Succeed())

			r := &ManagementReconciler{Client: k8sClient}
			sel := labels.SelectorFromSet(map[string]string{kcmv1.FluxHelmChartNameKey: kcmv1.CoreKCMName})
			Expect(r.removeCRDsWithSelector(ctx, sel)).To(Succeed())

			Consistently(func() error {
				return k8sClient.Get(ctx, client.ObjectKeyFromObject(crd), &apiextv1.CustomResourceDefinition{})
			}).WithTimeout(500 * time.Millisecond).WithPolling(interval).Should(Succeed())
		})

		It("skips CRDs already marked for deletion", func() {
			const testFinalizer = "k0rdent.mirantis.com/test-finalizer"

			crd := newTestCRD("in-deletion-cleanup-test.io", map[string]string{
				kcmv1.FluxHelmChartNameKey: kcmv1.CoreKCMName,
			})
			crd.Finalizers = []string{testFinalizer}
			Expect(k8sClient.Create(ctx, crd)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(crd), crd)
				crd.Finalizers = nil
				_ = k8sClient.Update(ctx, crd)
				_ = k8sClient.Delete(ctx, crd)
			})

			By("marking the CRD for deletion, the finalizer keeps it around")
			Expect(k8sClient.Delete(ctx, crd)).To(Succeed())
			Eventually(func() bool {
				g := &apiextv1.CustomResourceDefinition{}
				return k8sClient.Get(ctx, client.ObjectKeyFromObject(crd), g) == nil && !g.DeletionTimestamp.IsZero()
			}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

			r := &ManagementReconciler{Client: k8sClient}
			sel := labels.SelectorFromSet(map[string]string{kcmv1.FluxHelmChartNameKey: kcmv1.CoreKCMName})
			Expect(r.removeCRDsWithSelector(ctx, sel)).To(Succeed())
		})
	})

	Context("ensureCldRegistryCredSecret", func() {
		const (
			systemNamespace    = "kcm-system-cld-test"
			registrySecretName = "registry-creds"
			globalRegistry     = "registry.example.com"
		)

		var mgmt *kcmv1.Management

		BeforeEach(func() {
			By("ensuring the system namespace exists")
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: systemNamespace},
			}))).To(Succeed())
			Eventually(k8sClient.Get).WithArguments(ctx, client.ObjectKey{Name: systemNamespace}, &corev1.Namespace{}).
				WithTimeout(3 * time.Second).WithPolling(pollingInterval).Should(Succeed())

			mgmt = &kcmv1.Management{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-mgmt-cld-reg-" + strconv.FormatInt(time.Now().UnixNano(), 10),
				},
				Spec: kcmv1.ManagementSpec{
					Release: "test-release",
				},
			}
			Expect(k8sClient.Create(ctx, mgmt)).To(Succeed())
			Eventually(k8sClient.Get).WithArguments(ctx, client.ObjectKeyFromObject(mgmt), mgmt).
				WithTimeout(3 * time.Second).WithPolling(pollingInterval).Should(Succeed())
		})

		AfterEach(func() {
			By("cleaning up management and secrets")
			mgmt.Finalizers = nil
			_ = k8sClient.Update(ctx, mgmt)
			_ = k8sClient.Delete(ctx, mgmt)
			_ = k8sClient.Delete(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: cldRegSecretName, Namespace: systemNamespace},
			})
			_ = k8sClient.Delete(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: registrySecretName, Namespace: systemNamespace},
			})
		})

		It("should return nil and do nothing when registry config is empty and no prior condition exists", func() {
			r := &ManagementReconciler{
				Client:                        k8sClient,
				SystemNamespace:               systemNamespace,
				RegistryCredentialsSecretName: "",
				GlobalRegistry:                "",
			}

			err := r.ensureCldRegistryCredSecret(ctx, mgmt)
			Expect(err).NotTo(HaveOccurred())

			secret := &corev1.Secret{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: cldRegSecretName, Namespace: systemNamespace}, secret)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cld secret should not exist")
		})

		It("should create the cld registry credential secret when registry config is provided", func() {
			regSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      registrySecretName,
					Namespace: systemNamespace,
				},
				Data: map[string][]byte{
					"username": []byte("testuser"),
					"password": []byte("testpass"),
				},
			}
			Expect(k8sClient.Create(ctx, regSecret)).To(Succeed())

			r := &ManagementReconciler{
				Client:                        k8sClient,
				SystemNamespace:               systemNamespace,
				RegistryCredentialsSecretName: registrySecretName,
				GlobalRegistry:                globalRegistry,
			}

			err := r.ensureCldRegistryCredSecret(ctx, mgmt)
			Expect(err).NotTo(HaveOccurred())

			cldSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: cldRegSecretName, Namespace: systemNamespace}, cldSecret)).To(Succeed())
			Expect(cldSecret.Data).To(HaveKey("registryCredential"))
			Expect(cldSecret.Data).To(HaveKey("containerdAuth"))

			By("checking the RegistryCredentialSecretReady condition is set on the in-memory object")
			// The condition is set in-memory by ensureCldRegistryCredSecret; the caller (update) persists it later.
			cond := meta.FindStatusCondition(mgmt.Status.Conditions, kcmv1.RegistryCredentialSecretReadyCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(kcmv1.SucceededReason))
		})

		It("should delete the cld secret when registry config is removed after it was set", func() {
			regSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      registrySecretName,
					Namespace: systemNamespace,
				},
				Data: map[string][]byte{
					"username": []byte("testuser"),
					"password": []byte("testpass"),
				},
			}
			Expect(k8sClient.Create(ctx, regSecret)).To(Succeed())

			r := &ManagementReconciler{
				Client:                        k8sClient,
				SystemNamespace:               systemNamespace,
				RegistryCredentialsSecretName: registrySecretName,
				GlobalRegistry:                globalRegistry,
			}

			By("first creating the secret")
			err := r.ensureCldRegistryCredSecret(ctx, mgmt)
			Expect(err).NotTo(HaveOccurred())

			cldSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: cldRegSecretName, Namespace: systemNamespace}, cldSecret)).To(Succeed())

			By("checking the condition was set on the in-memory object")
			cond := meta.FindStatusCondition(mgmt.Status.Conditions, kcmv1.RegistryCredentialSecretReadyCondition)
			Expect(cond).NotTo(BeNil())

			By("simulating removal of the registry config")
			r.RegistryCredentialsSecretName = ""
			r.GlobalRegistry = ""

			err = r.ensureCldRegistryCredSecret(ctx, mgmt)
			Expect(err).NotTo(HaveOccurred())

			By("the cld secret should be deleted")
			err = k8sClient.Get(ctx, client.ObjectKey{Name: cldRegSecretName, Namespace: systemNamespace}, cldSecret)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cld secret should have been deleted")

			By("the condition should be removed")
			cond = meta.FindStatusCondition(mgmt.Status.Conditions, kcmv1.RegistryCredentialSecretReadyCondition)
			Expect(cond).To(BeNil(), "RegistryCredentialSecretReady condition should have been removed")
		})

		It("should return error when the registry credential secret is missing", func() {
			r := &ManagementReconciler{
				Client:                        k8sClient,
				SystemNamespace:               systemNamespace,
				RegistryCredentialsSecretName: "nonexistent-secret",
				GlobalRegistry:                globalRegistry,
			}

			err := r.ensureCldRegistryCredSecret(ctx, mgmt)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get registry credential secret"))
		})

		It("should return error when registry credential secret is missing username", func() {
			regSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      registrySecretName,
					Namespace: systemNamespace,
				},
				Data: map[string][]byte{
					"password": []byte("testpass"),
				},
			}
			Expect(k8sClient.Create(ctx, regSecret)).To(Succeed())

			r := &ManagementReconciler{
				Client:                        k8sClient,
				SystemNamespace:               systemNamespace,
				RegistryCredentialsSecretName: registrySecretName,
				GlobalRegistry:                globalRegistry,
			}

			err := r.ensureCldRegistryCredSecret(ctx, mgmt)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("username"))
		})

		It("should return error when registry credential secret is missing password", func() {
			regSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      registrySecretName,
					Namespace: systemNamespace,
				},
				Data: map[string][]byte{
					"username": []byte("testuser"),
				},
			}
			Expect(k8sClient.Create(ctx, regSecret)).To(Succeed())

			r := &ManagementReconciler{
				Client:                        k8sClient,
				SystemNamespace:               systemNamespace,
				RegistryCredentialsSecretName: registrySecretName,
				GlobalRegistry:                globalRegistry,
			}

			err := r.ensureCldRegistryCredSecret(ctx, mgmt)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("password"))
		})
	})

	Context("setCondition", func() {
		It("should set a condition with error message", func() {
			mgmt := &kcmv1.Management{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-mgmt-set-cond",
					Generation: 2,
				},
			}

			r := &ManagementReconciler{}
			changed := r.setCondition(mgmt, kcmv1.RegistryCredentialSecretReadyCondition, kcmv1.FailedReason, metav1.ConditionFalse, errors.New("something went wrong"))
			Expect(changed).To(BeTrue())

			cond := meta.FindStatusCondition(mgmt.Status.Conditions, kcmv1.RegistryCredentialSecretReadyCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(kcmv1.FailedReason))
			Expect(cond.Message).To(Equal("something went wrong"))
			Expect(cond.ObservedGeneration).To(Equal(int64(2)))
		})

		It("should set a condition with empty message when err is nil", func() {
			mgmt := &kcmv1.Management{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-mgmt-set-cond-nil",
					Generation: 3,
				},
			}

			r := &ManagementReconciler{}
			changed := r.setCondition(mgmt, kcmv1.RegistryCredentialSecretReadyCondition, kcmv1.SucceededReason, metav1.ConditionTrue, nil)
			Expect(changed).To(BeTrue())

			cond := meta.FindStatusCondition(mgmt.Status.Conditions, kcmv1.RegistryCredentialSecretReadyCondition)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(kcmv1.SucceededReason))
			Expect(cond.Message).To(BeEmpty())
			Expect(cond.ObservedGeneration).To(Equal(int64(3)))
		})

		It("should return false when setting the same condition again", func() {
			mgmt := &kcmv1.Management{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-mgmt-set-cond-nochange",
					Generation: 1,
				},
			}

			r := &ManagementReconciler{}
			changed := r.setCondition(mgmt, kcmv1.RegistryCredentialSecretReadyCondition, kcmv1.SucceededReason, metav1.ConditionTrue, nil)
			Expect(changed).To(BeTrue())

			changed = r.setCondition(mgmt, kcmv1.RegistryCredentialSecretReadyCondition, kcmv1.SucceededReason, metav1.ConditionTrue, nil)
			Expect(changed).To(BeFalse(), "setting the same condition twice should return false")
		})
	})
})

func Test_boundedDNSName(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "short value",
			value: "v1.2.3",
			want:  "v1.2.3",
		},
		{
			name:  "long value",
			value: strings.Repeat("a", validationcontent.DNS1123SubdomainMaxLength),
			want:  strings.Repeat("a", 54) + "-32859a3a",
		},
	}

	r := &ManagementReconciler{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.boundedDNSName(tt.value, validationcontent.LabelValueMaxLength)
			require.Equal(t, tt.want, got)
			require.Empty(t, validationcontent.IsLabelValue(got))
		})
	}
}
