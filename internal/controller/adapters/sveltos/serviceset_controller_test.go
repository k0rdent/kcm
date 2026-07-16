// Copyright 2025
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

package sveltos

import (
	"reflect"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	"github.com/projectsveltos/addon-controller/lib/clusterops"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	emptyString         = ""
	multiClusterService = "sample-multiclusterservice"

	adapterAPIVersion = "sample-version/v1"
	adapterKind       = "SampleAdapter"
	adapterName       = "sample-adapter"
	adapterNamespace  = "sample-namespace"

	provisionerAPIVersion = "sample-version/v1"
	provisionerKind       = "SampleProvisioner"
	provisionerName       = "sample-provisioner"
	provisionerNamespace  = "sample-namespace"

	provisionerCRDGroup    = "sample-crd-group"
	provisionerCRDResource = "sample-crd-resources"
)

var testLabel = map[string]string{"integration-test": "true"}

var _ = Describe("ServiceSet Controller integration tests", Ordered, func() {
	var (
		reconciler ServiceSetReconciler

		namespace               corev1.Namespace
		credential              kcmv1.Credential
		clusterDeployment       kcmv1.ClusterDeployment
		serviceSet              kcmv1.ServiceSet
		stateManagementProvider kcmv1.StateManagementProvider

		cluster clusterapiv1.Cluster

		profile        addoncontrollerv1beta1.Profile
		clusterProfile addoncontrollerv1beta1.ClusterProfile
	)

	BeforeEach(func() {
		By("creating a namespace", func() {
			namespace = corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "namespace-test-"}}
			Expect(cl.Create(ctx, &namespace)).To(Succeed())
		})

		By("creating a StateManagementProvider", func() {
			stateManagementProvider = prepareStateManagementProvider()
			Expect(cl.Create(ctx, &stateManagementProvider)).To(Succeed())
		})

		By("creating a Credential", func() {
			credential = prepareCredential(namespace.Name)
			Expect(cl.Create(ctx, &credential)).To(Succeed())
		})

		By("creating a ClusterDeployment", func() {
			clusterDeployment = prepareClusterDeployment(namespace.Name, credential.Name)
			Expect(cl.Create(ctx, &clusterDeployment)).To(Succeed())
		})

		By("creating a CAPI Cluster", func() {
			cluster = prepareCAPICluster(clusterDeployment.Name, clusterDeployment.Namespace)
			Expect(cl.Create(ctx, &cluster)).To(Succeed())
		})

		By("creating a ServiceSet", func() {
			serviceSet = prepareServiceSet(namespace.Name, stateManagementProvider.Name, clusterDeployment.Name)
			Expect(cl.Create(ctx, &serviceSet)).To(Succeed())
		})

		By("creating reconciler", func() {
			reconciler = ServiceSetReconciler{
				Client:          cl,
				timeFunc:        func() time.Time { return time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC) },
				requeueInterval: 1 * time.Second,
			}
		})
	})

	AfterEach(func() {
		Expect(client.IgnoreNotFound(cl.Delete(ctx, &serviceSet))).To(Succeed())
		Expect(client.IgnoreNotFound(cl.Delete(ctx, &clusterDeployment))).To(Succeed())
		Expect(client.IgnoreNotFound(cl.Delete(ctx, &credential))).To(Succeed())
		Expect(client.IgnoreNotFound(cl.Delete(ctx, &stateManagementProvider))).To(Succeed())
		Expect(client.IgnoreNotFound(cl.Delete(ctx, &namespace))).To(Succeed())
	})

	type tc struct {
		name string
		src  *kcmv1.ServiceHelmOptions
		dst  *kcmv1.ServiceHelmOptions
		want *kcmv1.ServiceHelmOptions
	}
	testTimeout := &metav1.Duration{Duration: time.Minute * 5}
	DescribeTable("merge behavior",
		func(t tc) {
			mergeHelmOptions(t.src, t.dst)
			Expect(reflect.DeepEqual(t.dst, t.want)).To(BeTrue(),
				"test case failed: %s\ndst=%#v\nwant=%#v", t.name, t.dst, t.want)
		},
		Entry("src=nil → no change",
			tc{
				name: "src nil",
				src:  nil,
				dst:  &kcmv1.ServiceHelmOptions{},
				want: &kcmv1.ServiceHelmOptions{},
			},
		),

		Entry("dst=nil → safely does nothing",
			tc{
				name: "dst nil",
				src:  &kcmv1.ServiceHelmOptions{Atomic: new(true)},
				dst:  nil,
				want: nil,
			},
		),

		Entry("src empty → dst unchanged",
			tc{
				name: "src empty",
				src:  &kcmv1.ServiceHelmOptions{},
				dst:  &kcmv1.ServiceHelmOptions{Atomic: new(false)},
				want: &kcmv1.ServiceHelmOptions{Atomic: new(false)},
			},
		),

		Entry("copy all boolean fields",
			tc{
				name: "copy all bools",
				src: &kcmv1.ServiceHelmOptions{
					EnableClientCache:        new(true),
					DependencyUpdate:         new(true),
					Wait:                     new(false),
					WaitForJobs:              new(true),
					CreateNamespace:          new(false),
					SkipCRDs:                 new(true),
					Atomic:                   new(false),
					DisableHooks:             new(true),
					DisableOpenAPIValidation: new(true),
					SkipSchemaValidation:     new(true),
					Replace:                  new(false),
				},
				dst: &kcmv1.ServiceHelmOptions{},
				want: &kcmv1.ServiceHelmOptions{
					EnableClientCache:        new(true),
					DependencyUpdate:         new(true),
					Wait:                     new(false),
					WaitForJobs:              new(true),
					CreateNamespace:          new(false),
					SkipCRDs:                 new(true),
					Atomic:                   new(false),
					DisableHooks:             new(true),
					DisableOpenAPIValidation: new(true),
					SkipSchemaValidation:     new(true),
					Replace:                  new(false),
				},
			},
		),

		Entry("copy Timeout",
			tc{
				name: "copy timeout",
				src:  &kcmv1.ServiceHelmOptions{Timeout: testTimeout},
				dst:  &kcmv1.ServiceHelmOptions{},
				want: &kcmv1.ServiceHelmOptions{Timeout: testTimeout},
			},
		),

		Entry("copy map",
			tc{
				name: "copy labels map",
				src:  &kcmv1.ServiceHelmOptions{Labels: &map[string]string{"env": "prod"}},
				dst:  &kcmv1.ServiceHelmOptions{},
				want: &kcmv1.ServiceHelmOptions{Labels: &map[string]string{"env": "prod"}},
			},
		),

		Entry("merge maps",
			tc{
				name: "merge labels map",
				src:  &kcmv1.ServiceHelmOptions{Labels: &map[string]string{"env": "prod"}},
				dst:  &kcmv1.ServiceHelmOptions{Labels: &map[string]string{"test": "true"}},
				want: &kcmv1.ServiceHelmOptions{Labels: &map[string]string{"test": "true", "env": "prod"}},
			},
		),
		Entry("copy Description",
			tc{
				name: "copy description",
				src:  &kcmv1.ServiceHelmOptions{Description: new("hello")},
				dst:  &kcmv1.ServiceHelmOptions{},
				want: &kcmv1.ServiceHelmOptions{Description: new("hello")},
			},
		),

		Entry("src non-zero only → dst keeps existing values",
			tc{
				name: "src non-zero only",
				src: &kcmv1.ServiceHelmOptions{
					Atomic: new(true),
				},
				dst: &kcmv1.ServiceHelmOptions{
					Timeout: testTimeout,
				},
				want: &kcmv1.ServiceHelmOptions{
					Atomic:  new(true),
					Timeout: testTimeout,
				},
			},
		),

		Entry("full mixed merge",
			tc{
				name: "mixed merge",
				src: &kcmv1.ServiceHelmOptions{
					EnableClientCache: new(true),
					Description:       new("new"),
				},
				dst: &kcmv1.ServiceHelmOptions{
					Atomic:      new(false),
					SkipCRDs:    new(true),
					Description: new("old"),
					Timeout:     testTimeout,
				},
				want: &kcmv1.ServiceHelmOptions{
					EnableClientCache: new(true),
					Description:       new("new"),
					Atomic:            new(false),
					SkipCRDs:          new(true),
					Timeout:           testTimeout,
				},
			},
		),
	)

	Context("When StateManagementProvider is not ready", func() {
		It("should only update the status of the ServiceSet", func() {
			By("checking the StateManagementProvider is not ready", func() {
				Expect(Object(&stateManagementProvider)()).Should(SatisfyAll(
					HaveField("Status.Ready", BeFalse()),
				))
			})

			By("reconciling the ServiceSet", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
				Expect(Object(&serviceSet)()).Should(SatisfyAll(
					HaveField("Status.Provider.Ready", BeFalse()),
					HaveField("Status.Provider.Suspended", BeFalse()),
					HaveField("Status.Cluster.APIVersion", kcmv1.GroupVersion.WithKind(kcmv1.ClusterDeploymentKind).GroupVersion().String()),
					HaveField("Status.Cluster.Kind", kcmv1.ClusterDeploymentKind),
					HaveField("Status.Cluster.Name", clusterDeployment.Name),
					HaveField("Status.Cluster.Namespace", clusterDeployment.Namespace),
				))

				profile = addoncontrollerv1beta1.Profile{}
				err = cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &profile)
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsNotFound(err)).To(BeTrue())
			})
		})
	})

	Context("When StateManagementProvider is suspended", func() {
		It("should only update the status of the ServiceSet", func() {
			By("updating the StateManagementProvider to be ready and suspended", func() {
				stateManagementProvider.Spec.Suspend = true
				Expect(cl.Update(ctx, &stateManagementProvider)).To(Succeed())
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
				Expect(Object(&stateManagementProvider)()).Should(SatisfyAll(
					HaveField("Spec.Suspend", BeTrue()),
					HaveField("Status.Ready", BeTrue()),
				))
			})

			By("reconciling the ServiceSet", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
				Expect(Object(&serviceSet)()).Should(SatisfyAll(
					HaveField("Status.Provider.Ready", BeTrue()),
					HaveField("Status.Provider.Suspended", BeTrue()),
					HaveField("Status.Cluster.APIVersion", kcmv1.GroupVersion.WithKind(kcmv1.ClusterDeploymentKind).GroupVersion().String()),
					HaveField("Status.Cluster.Kind", kcmv1.ClusterDeploymentKind),
					HaveField("Status.Cluster.Name", clusterDeployment.Name),
					HaveField("Status.Cluster.Namespace", clusterDeployment.Namespace),
				))

				profile = addoncontrollerv1beta1.Profile{}
				err = cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &profile)
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsNotFound(err)).To(BeTrue())
			})
		})
	})

	Context("When ServiceSet is SelfManagement", func() {
		It("should create ClusterProfile", func() {
			By("updating the StateManagementProvider to be ready", func() {
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
				Expect(Object(&stateManagementProvider)()).Should(SatisfyAll(
					HaveField("Status.Ready", BeTrue()),
				))
			})

			By("updating the ServiceSet to be SelfManagement", func() {
				serviceSet.Spec.Cluster = emptyString
				serviceSet.Spec.MultiClusterService = multiClusterService
				serviceSet.Spec.Provider.SelfManagement = true
				Expect(cl.Update(ctx, &serviceSet)).To(Succeed())
				Expect(Object(&serviceSet)()).Should(SatisfyAll(
					HaveField("Spec.Provider.SelfManagement", BeTrue()),
				))
			})

			By("reconciling the ServiceSet", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
				Expect(Object(&serviceSet)()).Should(SatisfyAll(
					HaveField("Status.Provider.Ready", BeTrue()),
					HaveField("Status.Provider.Suspended", BeFalse()),
					HaveField("Status.Cluster.APIVersion", libsveltosv1beta1.GroupVersion.WithKind(libsveltosv1beta1.SveltosClusterKind).GroupVersion().String()),
					HaveField("Status.Cluster.Kind", libsveltosv1beta1.SveltosClusterKind),
					HaveField("Status.Cluster.Name", managementSveltosCluster),
					HaveField("Status.Cluster.Namespace", managementSveltosCluster),
				))

				clusterProfile = addoncontrollerv1beta1.ClusterProfile{}
				err = cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &clusterProfile)
				Expect(err).ShouldNot(HaveOccurred())
			})
		})
	})

	Context("When ServiceSet provider configuration is defined", func() {
		It("should create Profile and pass config to it", func() {
			By("updating the StateManagementProvider to be ready", func() {
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
				Expect(Object(&stateManagementProvider)()).Should(SatisfyAll(
					HaveField("Status.Ready", BeTrue()),
				))
			})

			By("updating the ServiceSet with provider config", func() {
				providerConfig := `
{
	"stopMatchingBehavior": "LeavePolicies",
	"syncMode": "OneTime"
}
`
				serviceSet.Spec.Provider.Config = &apiextv1.JSON{
					Raw: []byte(providerConfig),
				}
				Expect(cl.Update(ctx, &serviceSet)).To(Succeed())
				Expect(Object(&serviceSet)()).Should(SatisfyAll(
					HaveField("Spec.Provider.Config", Not(BeNil())),
				))
			})

			By("reconciling the ServiceSet", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
				Expect(Object(&serviceSet)()).Should(SatisfyAll(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.ServiceSetProfileCondition),
						HaveField("Status", metav1.ConditionTrue),
						HaveField("Reason", kcmv1.ServiceSetProfileReadyReason),
					))),
					HaveField("Status.Cluster.APIVersion", kcmv1.GroupVersion.WithKind(kcmv1.ClusterDeploymentKind).GroupVersion().String()),
					HaveField("Status.Cluster.Kind", kcmv1.ClusterDeploymentKind),
					HaveField("Status.Cluster.Name", clusterDeployment.Name),
					HaveField("Status.Cluster.Namespace", clusterDeployment.Namespace),
				))

				profile = addoncontrollerv1beta1.Profile{}
				err = cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &profile)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(Object(&profile)()).Should(SatisfyAll(
					HaveField("Spec.StopMatchingBehavior", Equal(addoncontrollerv1beta1.LeavePolicies)),
					HaveField("Spec.SyncMode", Equal(addoncontrollerv1beta1.SyncModeOneTime)),
				))
			})
		})
	})

	Context("When ServiceSet provider configuration has an invalid priority", func() {
		It("should surface the build failure in the ServiceSet status", func() {
			By("updating the StateManagementProvider to be ready", func() {
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
				Expect(Object(&stateManagementProvider)()).Should(SatisfyAll(
					HaveField("Status.Ready", BeTrue()),
				))
			})

			By("updating the ServiceSet with an invalid priority in the provider config and a pending service", func() {
				providerConfig := `
{
	"priority": 0
}
`
				serviceSet.Spec.Provider.Config = &apiextv1.JSON{
					Raw: []byte(providerConfig),
				}
				// a service declared in spec but not yet reflected in status
				// reproduces the case where fillNotDeployedServices would run
				// before the service type was ever resolved.
				serviceSet.Spec.Services = []kcmv1.ServiceWithValues{
					{Name: "test-svc", Namespace: namespace.Name, Template: "does-not-exist"},
				}
				Expect(cl.Update(ctx, &serviceSet)).To(Succeed())
				Expect(Object(&serviceSet)()).Should(SatisfyAll(
					HaveField("Spec.Provider.Config", Not(BeNil())),
				))
			})

			By("reconciling and expecting the failure reason and message on the ServiceSet status", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to build Profile"))
				// the status update itself must succeed, otherwise the failure
				// reason/message never gets persisted to the ServiceSet.
				Expect(err.Error()).NotTo(ContainSubstring("is invalid"))
				Expect(Object(&serviceSet)()).Should(SatisfyAll(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.ServiceSetProfileCondition),
						HaveField("Status", metav1.ConditionFalse),
						HaveField("Reason", kcmv1.ServiceSetProfileBuildFailedReason),
						HaveField("Message", ContainSubstring("priority has to be between")),
					))),
				))
			})
		})
	})

	Context("When ServiceSet provider configuration got updated", func() {
		It("should create Profile and update its config on ServiceSet config update", func() {
			By("updating the StateManagementProvider to be ready", func() {
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
				Expect(Object(&stateManagementProvider)()).Should(SatisfyAll(
					HaveField("Status.Ready", BeTrue()),
				))
			})

			By("updating the ServiceSet with provider config", func() {
				providerConfig := `
{
	"stopMatchingBehavior": "LeavePolicies",
	"syncMode": "OneTime"
}
`
				serviceSet.Spec.Provider.Config = &apiextv1.JSON{
					Raw: []byte(providerConfig),
				}
				Expect(cl.Update(ctx, &serviceSet)).To(Succeed())
				Expect(Object(&serviceSet)()).Should(SatisfyAll(
					HaveField("Spec.Provider.Config", Not(BeNil())),
				))
			})

			By("reconciling the ServiceSet", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
				Expect(Object(&serviceSet)()).Should(SatisfyAll(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.ServiceSetProfileCondition),
						HaveField("Status", metav1.ConditionTrue),
						HaveField("Reason", kcmv1.ServiceSetProfileReadyReason),
					))),
					HaveField("Status.Cluster.APIVersion", kcmv1.GroupVersion.WithKind(kcmv1.ClusterDeploymentKind).GroupVersion().String()),
					HaveField("Status.Cluster.Kind", kcmv1.ClusterDeploymentKind),
					HaveField("Status.Cluster.Name", clusterDeployment.Name),
					HaveField("Status.Cluster.Namespace", clusterDeployment.Namespace),
				))

				profile = addoncontrollerv1beta1.Profile{}
				err = cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &profile)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(Object(&profile)()).Should(SatisfyAll(
					HaveField("Spec.StopMatchingBehavior", Equal(addoncontrollerv1beta1.LeavePolicies)),
					HaveField("Spec.SyncMode", Equal(addoncontrollerv1beta1.SyncModeOneTime)),
				))
			})

			By("updating the ServiceSet with provider config", func() {
				providerConfig := `
{
	"stopMatchingBehavior": "WithdrawPolicies",
	"continueOnError": true
}
`
				serviceSet.Spec.Provider.Config = &apiextv1.JSON{
					Raw: []byte(providerConfig),
				}
				Expect(cl.Update(ctx, &serviceSet)).To(Succeed())
				Expect(Object(&serviceSet)()).Should(SatisfyAll(
					HaveField("Spec.Provider.Config", Not(BeNil())),
				))
			})

			By("reconciling ServiceSet again", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
				Expect(Object(&serviceSet)()).Should(SatisfyAll(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.ServiceSetProfileCondition),
						HaveField("Status", metav1.ConditionTrue),
						HaveField("Reason", kcmv1.ServiceSetProfileReadyReason),
					))),
					HaveField("Status.Cluster.APIVersion", kcmv1.GroupVersion.WithKind(kcmv1.ClusterDeploymentKind).GroupVersion().String()),
					HaveField("Status.Cluster.Kind", kcmv1.ClusterDeploymentKind),
					HaveField("Status.Cluster.Name", clusterDeployment.Name),
					HaveField("Status.Cluster.Namespace", clusterDeployment.Namespace),
				))

				profile = addoncontrollerv1beta1.Profile{}
				err = cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &profile)
				Expect(err).ShouldNot(HaveOccurred())
				Expect(Object(&profile)()).Should(SatisfyAll(
					HaveField("Spec.StopMatchingBehavior", Equal(addoncontrollerv1beta1.WithdrawPolicies)),
					HaveField("Spec.SyncMode", Equal(addoncontrollerv1beta1.SyncModeContinuous)),
					HaveField("Spec.ContinueOnError", BeTrue()),
				))
			})
		})
	})

	Context("When ServiceSet does not exist in the cluster", func() {
		It("should return nil without error", func() {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "non-existent-ss", Namespace: namespace.Name},
			})
			Expect(err).To(Succeed())
		})
	})

	Context("When ServiceSet has no finalizer on first reconcile", func() {
		It("should add the finalizer and return without further processing", func() {
			noFinalizerSS := kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "ss-no-fin-",
					Namespace:    namespace.Name,
					Labels:       testLabel,
				},
				Spec: kcmv1.ServiceSetSpec{
					Cluster: clusterDeployment.Name,
					Provider: kcmv1.StateManagementProviderConfig{
						Name: stateManagementProvider.Name,
					},
				},
			}
			Expect(cl.Create(ctx, &noFinalizerSS)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&noFinalizerSS)})
			Expect(err).To(Succeed())

			Expect(Object(&noFinalizerSS)()).Should(
				HaveField("Finalizers", ContainElement(kcmv1.ServiceSetFinalizer)),
			)

			noFinalizerSS.Finalizers = nil
			Expect(cl.Update(ctx, &noFinalizerSS)).To(Succeed())
			Expect(cl.Delete(ctx, &noFinalizerSS)).To(Succeed())
		})
	})

	Context("When ServiceSet has a service referencing a non-existent ServiceTemplate", func() {
		It("should fail reconciliation with a helm charts build error", func() {
			By("making StateManagementProvider ready", func() {
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
			})

			By("adding a service with a non-existent template reference", func() {
				serviceSet.Spec.Services = []kcmv1.ServiceWithValues{
					{
						Name:      "test-svc",
						Namespace: namespace.Name,
						Template:  "does-not-exist",
					},
				}
				Expect(cl.Update(ctx, &serviceSet)).To(Succeed())
			})

			By("reconciling and expecting an error", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to build Profile"))
			})
		})
	})

	Context("When ServiceSet has a service referencing a Kustomize ServiceTemplate", func() {
		It("should create a Profile with KustomizationRef", func() {
			By("making StateManagementProvider ready", func() {
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
			})

			By("creating a Kustomize ServiceTemplate and setting its status", func() {
				tmpl := &kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{Name: "kust-tmpl", Namespace: namespace.Name},
					Spec: kcmv1.ServiceTemplateSpec{
						Kustomize: &kcmv1.SourceSpec{
							DeploymentType: "Local",
							LocalSourceRef: &kcmv1.LocalSourceRef{Kind: "ConfigMap", Name: "kust-src"},
						},
					},
				}
				Expect(cl.Create(ctx, tmpl)).To(Succeed())
				tmpl.Status.Valid = true
				tmpl.Status.SourceStatus = &kcmv1.SourceStatus{Kind: "ConfigMap", Name: "kust-src", Namespace: namespace.Name}
				Expect(cl.Status().Update(ctx, tmpl)).To(Succeed())
				DeferCleanup(cl.Delete, tmpl)
			})

			By("adding a service referencing the Kustomize template", func() {
				serviceSet.Spec.Services = []kcmv1.ServiceWithValues{
					{Name: "kust-svc", Namespace: namespace.Name, Template: "kust-tmpl"},
				}
				Expect(cl.Update(ctx, &serviceSet)).To(Succeed())
			})

			By("reconciling and verifying Profile has KustomizationRef", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())

				prof := addoncontrollerv1beta1.Profile{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &prof)).To(Succeed())
				Expect(prof.Spec.KustomizationRefs).To(HaveLen(1))
				Expect(prof.Spec.KustomizationRefs[0].Kind).To(Equal("ConfigMap"))
			})
		})
	})

	Context("When ServiceSet has a service referencing a Resources ServiceTemplate", func() {
		It("should create a Profile with PolicyRef from Resources template", func() {
			By("making StateManagementProvider ready", func() {
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
			})

			By("creating a Resources ServiceTemplate and setting its status", func() {
				tmpl := &kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{Name: "res-tmpl", Namespace: namespace.Name},
					Spec: kcmv1.ServiceTemplateSpec{
						Resources: &kcmv1.SourceSpec{
							DeploymentType: "Local",
							LocalSourceRef: &kcmv1.LocalSourceRef{Kind: "ConfigMap", Name: "res-src"},
						},
					},
				}
				Expect(cl.Create(ctx, tmpl)).To(Succeed())
				tmpl.Status.Valid = true
				tmpl.Status.SourceStatus = &kcmv1.SourceStatus{Kind: "ConfigMap", Name: "res-src", Namespace: namespace.Name}
				Expect(cl.Status().Update(ctx, tmpl)).To(Succeed())
				DeferCleanup(cl.Delete, tmpl)
			})

			By("adding a service referencing the Resources template", func() {
				serviceSet.Spec.Services = []kcmv1.ServiceWithValues{
					{Name: "res-svc", Namespace: namespace.Name, Template: "res-tmpl"},
				}
				Expect(cl.Update(ctx, &serviceSet)).To(Succeed())
			})

			By("reconciling and verifying Profile has the Resources PolicyRef", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())

				prof := addoncontrollerv1beta1.Profile{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &prof)).To(Succeed())
				Expect(prof.Spec.PolicyRefs).To(ContainElement(
					HaveField("Kind", "ConfigMap"),
				))
			})
		})
	})

	Context("When ServiceSet has a service with a Helm template that has no status ChartRef", func() {
		It("should fail with 'status not updated' error covering helmChartFromSpecOrRef", func() {
			By("making StateManagementProvider ready", func() {
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
			})

			By("creating a Helm ServiceTemplate with ChartRef but no status ChartRef", func() {
				tmpl := &kcmv1.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{Name: "helm-no-status", Namespace: namespace.Name},
					Spec: kcmv1.ServiceTemplateSpec{
						Helm: &kcmv1.HelmSpec{
							ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
								Kind: "HelmChart",
								Name: "test-chart",
							},
						},
					},
				}
				Expect(cl.Create(ctx, tmpl)).To(Succeed())
				DeferCleanup(cl.Delete, tmpl)
			})

			By("adding a service referencing the Helm template", func() {
				serviceSet.Spec.Services = []kcmv1.ServiceWithValues{
					{Name: "helm-svc", Namespace: namespace.Name, Template: "helm-no-status"},
				}
				Expect(cl.Update(ctx, &serviceSet)).To(Succeed())
			})

			By("reconciling and expecting a 'status not updated' error", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to build Profile"))
			})
		})
	})

	invalidSourceTemplateTest := func(contextDesc, itDesc, tmplName, srcName, svcName string, specFn func(*kcmv1.ServiceTemplateSpec, *kcmv1.SourceSpec)) {
		Context(contextDesc, func() {
			It(itDesc, func() {
				By("making StateManagementProvider ready", func() {
					stateManagementProvider.Status.Ready = true
					Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
				})

				By("creating a ServiceTemplate with Valid=false", func() {
					spec := &kcmv1.ServiceTemplateSpec{}
					src := &kcmv1.SourceSpec{
						DeploymentType: "Local",
						LocalSourceRef: &kcmv1.LocalSourceRef{Kind: "ConfigMap", Name: srcName},
					}
					specFn(spec, src)
					tmpl := &kcmv1.ServiceTemplate{
						ObjectMeta: metav1.ObjectMeta{Name: tmplName, Namespace: namespace.Name},
						Spec:       *spec,
					}
					Expect(cl.Create(ctx, tmpl)).To(Succeed())
					tmpl.Status.SourceStatus = &kcmv1.SourceStatus{Kind: "ConfigMap", Name: srcName, Namespace: namespace.Name}
					Expect(cl.Status().Update(ctx, tmpl)).To(Succeed())
					DeferCleanup(cl.Delete, tmpl)
				})

				By("adding a service referencing the invalid template", func() {
					serviceSet.Spec.Services = []kcmv1.ServiceWithValues{
						{Name: svcName, Namespace: namespace.Name, Template: tmplName},
					}
					Expect(cl.Update(ctx, &serviceSet)).To(Succeed())
				})

				By("reconciling and expecting a profile build error", func() {
					_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("failed to build Profile"))
				})
			})
		})
	}

	invalidSourceTemplateTest(
		"When ServiceSet has a Kustomize template with invalid status (Valid=false)",
		"should fail with kustomization refs build error",
		"kust-invalid", "kust-src", "kust-inv-svc",
		func(spec *kcmv1.ServiceTemplateSpec, src *kcmv1.SourceSpec) { spec.Kustomize = src },
	)

	invalidSourceTemplateTest(
		"When ServiceSet has a Resources template with invalid status (Valid=false)",
		"should fail with policy refs build error",
		"res-invalid", "res-src", "res-inv-svc",
		func(spec *kcmv1.ServiceTemplateSpec, src *kcmv1.SourceSpec) { spec.Resources = src },
	)

	Context("When ServiceSet labels do not match the StateManagementProvider selector", func() {
		It("should skip reconciliation without creating a Profile", func() {
			By("marking StateManagementProvider as ready", func() {
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
			})

			By("clearing ServiceSet labels so they no longer match the provider selector", func() {
				serviceSet.Labels = nil
				Expect(cl.Update(ctx, &serviceSet)).To(Succeed())
			})

			By("reconciling the ServiceSet", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
			})

			By("verifying no Profile was created", func() {
				prof := addoncontrollerv1beta1.Profile{}
				err := cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &prof)
				Expect(apierrors.IsNotFound(err)).To(BeTrue())
			})
		})
	})

	Context("When ServiceSet is deleted after a Profile was created", func() {
		It("should delete the Profile then remove the finalizer on the next reconcile", func() {
			By("making StateManagementProvider ready", func() {
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
			})

			By("reconciling to create the Profile", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
				prof := addoncontrollerv1beta1.Profile{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &prof)).To(Succeed())
			})

			By("deleting the ServiceSet", func() {
				Expect(cl.Delete(ctx, &serviceSet)).To(Succeed())
			})

			By("first reconcile: should delete the Profile", func() {
				result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(reconciler.requeueInterval))

				prof := addoncontrollerv1beta1.Profile{}
				err = cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &prof)
				Expect(apierrors.IsNotFound(err)).To(BeTrue())
			})

			By("second reconcile: should remove the finalizer", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
			})

			By("verifying the ServiceSet is eventually removed", func() {
				Eventually(func() bool {
					ss := &kcmv1.ServiceSet{}
					return apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), ss))
				}).Should(BeTrue())
			})
		})
	})

	Context("When ServiceSet is deleted with no Profile and empty service list", func() {
		It("should remove the finalizer and allow object deletion", func() {
			By("deleting the ServiceSet to trigger reconcileDelete", func() {
				Expect(cl.Delete(ctx, &serviceSet)).To(Succeed())
			})

			By("reconciling the deleted ServiceSet", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
			})

			By("verifying the ServiceSet is eventually deleted", func() {
				Eventually(func() bool {
					ss := &kcmv1.ServiceSet{}
					return apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), ss))
				}).Should(BeTrue())
			})
		})
	})

	Context("When ServiceSet is deleted but has services in non-Deleting state", func() {
		It("should transition service states to Deleting and requeue", func() {
			now := metav1.Now()
			By("seeding a service status on the ServiceSet", func() {
				serviceSet.Status.Services = []kcmv1.ServiceState{
					{
						Name:                    "test-service",
						Type:                    kcmv1.ServiceTypeHelm,
						State:                   kcmv1.ServiceStateProvisioning,
						LastStateTransitionTime: &now,
					},
				}
				Expect(cl.Status().Update(ctx, &serviceSet)).To(Succeed())
			})

			By("deleting the ServiceSet", func() {
				Expect(cl.Delete(ctx, &serviceSet)).To(Succeed())
			})

			By("reconciling the deleted ServiceSet", func() {
				result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
				// reconcileDelete returns immediately after updating statuses
				Expect(result.RequeueAfter).To(BeZero())
			})

			By("verifying services transitioned to Deleting state", func() {
				ss := &kcmv1.ServiceSet{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), ss)).To(Succeed())
				Expect(ss.Status.Services).To(HaveLen(1))
				Expect(ss.Status.Services[0].State).To(Equal(kcmv1.ServiceStateDeleting))
			})

			By("cleaning up: remove finalizer so AfterEach can delete the ServiceSet", func() {
				ss := &kcmv1.ServiceSet{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), ss)).To(Succeed())
				ss.Finalizers = nil
				Expect(cl.Update(ctx, ss)).To(Succeed())
			})
		})
	})

	Context("When Profile has MatchingClusterRefs and a ClusterSummary exists", func() {
		It("should successfully collect service statuses via collectServiceStatusesFromProfileOrClusterProfile", func() {
			By("making StateManagementProvider ready", func() {
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
			})

			By("first reconcile to create the Profile", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
				prof := &addoncontrollerv1beta1.Profile{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), prof)).To(Succeed())
			})

			By("setting Profile.Status.MatchingClusterRefs to the CAPI cluster", func() {
				prof := &addoncontrollerv1beta1.Profile{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), prof)).To(Succeed())
				prof.Status.MatchingClusterRefs = []corev1.ObjectReference{
					{
						APIVersion: clusterapiv1.GroupVersion.String(),
						Kind:       "Cluster",
						Name:       clusterDeployment.Name,
						Namespace:  namespace.Name,
					},
				}
				Expect(cl.Status().Update(ctx, prof)).To(Succeed())
			})

			By("creating a ClusterSummary with the name computed by clusterops", func() {
				summaryName := clusterops.GetClusterSummaryName(
					addoncontrollerv1beta1.ProfileKind,
					serviceSet.Name,
					clusterDeployment.Name,
					false,
				)
				summary := &addoncontrollerv1beta1.ClusterSummary{
					ObjectMeta: metav1.ObjectMeta{
						Name:      summaryName,
						Namespace: namespace.Name,
					},
					Spec: addoncontrollerv1beta1.ClusterSummarySpec{
						ClusterNamespace: namespace.Name,
						ClusterName:      clusterDeployment.Name,
						ClusterType:      libsveltosv1beta1.ClusterTypeCapi,
					},
				}
				Expect(cl.Create(ctx, summary)).To(Succeed())
				DeferCleanup(cl.Delete, summary)
			})

			By("second reconcile should succeed and collectServiceStatuses finds the ClusterSummary", func() {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
			})
		})
	})

	Context("Profile owner references", func() {
		var profileSpec addoncontrollerv1beta1.Spec

		BeforeEach(func() {
			profileSpec = addoncontrollerv1beta1.Spec{
				SyncMode: addoncontrollerv1beta1.SyncModeContinuous,
			}
		})

		It("should set the controller owner reference on the Profile when no region is in play", func() {
			By("creating the Profile", func() {
				Expect(reconciler.createOrUpdateProfile(ctx, cl, &serviceSet, &profileSpec)).To(Succeed())
				profile = addoncontrollerv1beta1.Profile{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &profile)).To(Succeed())
				DeferCleanup(func() {
					Expect(client.IgnoreNotFound(cl.Delete(ctx, &profile))).To(Succeed())
				})
				Expect(profile.OwnerReferences).To(HaveLen(1))
				Expect(profile.OwnerReferences[0]).To(SatisfyAll(
					HaveField("Kind", kcmv1.ServiceSetKind),
					HaveField("Name", serviceSet.Name),
					HaveField("UID", serviceSet.UID),
					HaveField("Controller", HaveValue(BeTrue())),
				))
			})

			By("updating the Profile", func() {
				updatedSpec := profileSpec
				updatedSpec.StopMatchingBehavior = addoncontrollerv1beta1.LeavePolicies
				Expect(reconciler.createOrUpdateProfile(ctx, cl, &serviceSet, &updatedSpec)).To(Succeed())
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &profile)).To(Succeed())
				Expect(profile.Spec.StopMatchingBehavior).To(Equal(addoncontrollerv1beta1.LeavePolicies))
				Expect(profile.OwnerReferences).To(HaveLen(1))
			})
		})

		It("should not set owner references on the Profile when using a regional (non-management) client", func() {
			// Simulate a regional client by creating a separate client instance (even if it points
			// at the same API server); owner references are gated on the client being the exact
			// same object as the reconciler's client.
			rgnCl, err := client.New(config, client.Options{Scheme: scheme.Scheme})
			Expect(err).NotTo(HaveOccurred())

			By("creating the Profile", func() {
				Expect(reconciler.createOrUpdateProfile(ctx, rgnCl, &serviceSet, &profileSpec)).To(Succeed())
				profile = addoncontrollerv1beta1.Profile{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &profile)).To(Succeed())
				DeferCleanup(func() {
					Expect(client.IgnoreNotFound(cl.Delete(ctx, &profile))).To(Succeed())
				})
				Expect(profile.OwnerReferences).To(BeEmpty())
			})

			By("updating the Profile", func() {
				updatedSpec := profileSpec
				updatedSpec.StopMatchingBehavior = addoncontrollerv1beta1.LeavePolicies
				Expect(reconciler.createOrUpdateProfile(ctx, rgnCl, &serviceSet, &updatedSpec)).To(Succeed())
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), &profile)).To(Succeed())
				Expect(profile.Spec.StopMatchingBehavior).To(Equal(addoncontrollerv1beta1.LeavePolicies))
				Expect(profile.OwnerReferences).To(BeEmpty())
			})
		})
	})

	// Regression coverage for reviewer comment
	// https://github.com/k0rdent/kcm/pull/2891#discussion_r3596677308 — the
	// verifier must never promote a Provisioning service to Deployed when
	// the sveltos-side fingerprint has advanced past what we last confirmed
	// on cluster, because the healthy pods the verifier just observed may
	// be stale pre-rollout pods.
	Context("Verifier state decision — hash-gated promotion", func() {
		const (
			releaseName  = "verifier-hashgate"
			chartRepo    = "https://example.invalid/charts"
			chartVersion = "1.0.0"
			appVersion   = "1.0.0"
			specVersion  = "1.1.0"
		)
		valuesHash := []byte("values-hash-verifier-hashgate")

		// releaseNs is set per-It to the ServiceSet's own namespace so the
		// helper cleans up alongside the outer AfterEach. Envtest cannot
		// actually finish namespace teardown (no ns-lifecycle controller),
		// so reusing a shared release namespace would leak state across
		// test cases.
		var releaseNs string

		// prepareVerifierArtifacts wires up everything verifyServiceStates
		// needs: rules ConfigMap, healthy target Deployment, Profile with
		// MatchingClusterRefs, ClusterSummary with the release, and a
		// ClusterConfiguration owned by the Profile carrying the chart
		// entry. Returns the service hash the verifier will compute from
		// these artifacts — callers pre-seed LastDeployedHash to make the
		// hash match or mismatch as required by the test case.
		prepareVerifierArtifacts := func() string {
			releaseNs = namespace.Name

			By("targeting the reconciler at the ServiceSet's namespace for tier-1 rules", func() {
				reconciler.SystemNamespace = namespace.Name
			})

			By("creating a healthy Deployment labeled with the release name", func() {
				var replicas int32 = 1
				d := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:       releaseName,
						Namespace:  releaseNs,
						Generation: 1,
						Labels:     map[string]string{"release": releaseName},
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: &replicas,
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": releaseName}},
						Template: corev1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": releaseName}},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{{Name: "c", Image: "busybox"}},
							},
						},
					},
				}
				Expect(cl.Create(ctx, d)).To(Succeed())
				d.Status = appsv1.DeploymentStatus{
					ObservedGeneration: 1,
					Replicas:           1,
					ReadyReplicas:      1,
					UpdatedReplicas:    1,
				}
				Expect(cl.Status().Update(ctx, d)).To(Succeed())
				DeferCleanup(cl.Delete, d)
			})

			By("creating a namespace-global rules ConfigMap", func() {
				cm := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "verifier-hashgate-rules",
						Namespace: namespace.Name,
						Labels:    map[string]string{healthRuleTargetLabel: healthRuleTargetGlobal},
					},
					Data: map[string]string{healthRuleConfigMapDataKey: testRulesYAML},
				}
				Expect(cl.Create(ctx, cm)).To(Succeed())
				DeferCleanup(cl.Delete, cm)
			})

			By("marking the StateManagementProvider ready and reconciling to create the Profile", func() {
				stateManagementProvider.Status.Ready = true
				Expect(cl.Status().Update(ctx, &stateManagementProvider)).To(Succeed())
				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&serviceSet)})
				Expect(err).To(Succeed())
			})

			prof := &addoncontrollerv1beta1.Profile{}
			By("setting Profile.Status.MatchingClusterRefs and capturing its UID", func() {
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&serviceSet), prof)).To(Succeed())
				prof.Status.MatchingClusterRefs = []corev1.ObjectReference{
					{
						APIVersion: clusterapiv1.GroupVersion.String(),
						Kind:       "Cluster",
						Name:       clusterDeployment.Name,
						Namespace:  namespace.Name,
					},
				}
				Expect(cl.Status().Update(ctx, prof)).To(Succeed())
			})

			var expectedHash string
			By("creating a ClusterSummary with a HelmReleaseSummary for the release", func() {
				summaryName := clusterops.GetClusterSummaryName(
					addoncontrollerv1beta1.ProfileKind,
					serviceSet.Name,
					clusterDeployment.Name,
					false,
				)
				summary := &addoncontrollerv1beta1.ClusterSummary{
					ObjectMeta: metav1.ObjectMeta{
						Name:      summaryName,
						Namespace: namespace.Name,
					},
					Spec: addoncontrollerv1beta1.ClusterSummarySpec{
						ClusterNamespace: namespace.Name,
						ClusterName:      clusterDeployment.Name,
						ClusterType:      libsveltosv1beta1.ClusterTypeCapi,
					},
				}
				Expect(cl.Create(ctx, summary)).To(Succeed())
				summary.Status.HelmReleaseSummaries = []addoncontrollerv1beta1.HelmChartSummary{
					{
						ReleaseName:      releaseName,
						ReleaseNamespace: releaseNs,
						Status:           addoncontrollerv1beta1.HelmChartStatusManaging,
						ValuesHash:       valuesHash,
					},
				}
				Expect(cl.Status().Update(ctx, summary)).To(Succeed())
				DeferCleanup(cl.Delete, summary)

				now := metav1.NewTime(reconciler.timeFunc())
				chart := &addoncontrollerv1beta1.Chart{
					RepoURL:         chartRepo,
					ReleaseName:     releaseName,
					Namespace:       releaseNs,
					ChartVersion:    chartVersion,
					AppVersion:      appVersion,
					LastAppliedTime: &now,
				}
				expectedHash = computeServiceHash(&summary.Status.HelmReleaseSummaries[0], chart)
				Expect(expectedHash).ToNot(BeEmpty())
			})

			By("creating a ClusterConfiguration owned by the Profile with the chart entry", func() {
				cc := &addoncontrollerv1beta1.ClusterConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cc-" + serviceSet.Name,
						Namespace: namespace.Name,
						OwnerReferences: []metav1.OwnerReference{{
							APIVersion: addoncontrollerv1beta1.GroupVersion.String(),
							Kind:       addoncontrollerv1beta1.ProfileKind,
							Name:       prof.Name,
							UID:        prof.UID,
						}},
					},
				}
				Expect(cl.Create(ctx, cc)).To(Succeed())
				now := metav1.NewTime(reconciler.timeFunc())
				cc.Status.ProfileResources = []addoncontrollerv1beta1.ProfileResource{
					{
						ProfileName: serviceSet.Name,
						Features: []addoncontrollerv1beta1.Feature{
							{
								FeatureID: libsveltosv1beta1.FeatureHelm,
								Charts: []addoncontrollerv1beta1.Chart{
									{
										RepoURL:         chartRepo,
										ReleaseName:     releaseName,
										Namespace:       releaseNs,
										ChartVersion:    chartVersion,
										AppVersion:      appVersion,
										LastAppliedTime: &now,
									},
								},
							},
						},
					},
				}
				Expect(cl.Status().Update(ctx, cc)).To(Succeed())
				DeferCleanup(cl.Delete, cc)
			})

			return expectedHash
		}

		// seedServiceStatus prepares the in-memory ServiceSet with a Helm
		// service in the given state, so verifyServiceStates has a target
		// to iterate. Spec.Services carries the "next intended version"
		// used by the stamp block.
		seedServiceStatus := func(state, lastDeployedHash, currentVersion string) {
			specVer := specVersion
			serviceSet.Spec.Services = []kcmv1.ServiceWithValues{{
				Name:      releaseName,
				Namespace: releaseNs,
				Version:   &specVer,
			}}
			curVer := currentVersion
			serviceSet.Status.Services = []kcmv1.ServiceState{{
				Type:             kcmv1.ServiceTypeHelm,
				Name:             releaseName,
				Namespace:        releaseNs,
				State:            state,
				LastDeployedHash: lastDeployedHash,
				Version:          &curVer,
			}}
		}

		It("Case A: promotes on hash match (unrelated apply demoted us)", func() {
			expectedHash := prepareVerifierArtifacts()
			// Sveltos-side hash equals what we last confirmed → the pods
			// the verifier sees ARE the confirmed version → safe to
			// promote back from the aggregate-demoted Provisioning.
			seedServiceStatus(kcmv1.ServiceStateProvisioning, expectedHash, "1.0.0")

			Expect(reconciler.verifyServiceStates(ctx, cl, &serviceSet)).To(Succeed())

			svc := serviceSet.Status.Services[0]
			Expect(svc.State).To(Equal(kcmv1.ServiceStateDeployed), "should promote to Deployed on hash match")
			Expect(svc.LastDeployedHash).To(Equal(expectedHash), "hash unchanged")
			Expect(*svc.Version).To(Equal("1.0.0"), "version unchanged — no stamp on hash match")
		})

		It("Case B: does NOT promote when hash advanced (our real apply in progress)", func() {
			_ = prepareVerifierArtifacts()
			// Sveltos-side hash has moved past what we last confirmed →
			// sveltos is applying a new version of THIS service. The
			// healthy pods the verifier just observed could be stale
			// previous-version pods. Refuse to promote.
			staleHash := "sha256:previous-confirmed-hash"
			seedServiceStatus(kcmv1.ServiceStateProvisioning, staleHash, "1.0.0")

			Expect(reconciler.verifyServiceStates(ctx, cl, &serviceSet)).To(Succeed())

			svc := serviceSet.Status.Services[0]
			Expect(svc.State).To(Equal(kcmv1.ServiceStateProvisioning), "must stay Provisioning when hash advanced")
			Expect(svc.LastDeployedHash).To(Equal(staleHash), "hash must NOT advance without sveltos-side confirmation")
			Expect(*svc.Version).To(Equal("1.0.0"), "version must not advance to spec value prematurely")
		})

		It("Case C: stamps hash+version when both sveltos and verifier agree on new hash", func() {
			expectedHash := prepareVerifierArtifacts()
			// Sveltos already says Deployed, verifier confirms healthy,
			// hash has advanced since last confirmed. This is the "both
			// authoritative signals agree" intersection — safe to record
			// the new version as landed.
			staleHash := "sha256:previous-confirmed-hash"
			seedServiceStatus(kcmv1.ServiceStateDeployed, staleHash, "1.0.0")

			Expect(reconciler.verifyServiceStates(ctx, cl, &serviceSet)).To(Succeed())

			svc := serviceSet.Status.Services[0]
			Expect(svc.State).To(Equal(kcmv1.ServiceStateDeployed), "both agreed → stays Deployed")
			Expect(svc.LastDeployedHash).To(Equal(expectedHash), "hash advances to sveltos-side fingerprint")
			Expect(*svc.Version).To(Equal(specVersion), "version stamps to Spec.Services[i].Version")
		})
	})
})

func prepareStateManagementProvider() kcmv1.StateManagementProvider {
	return kcmv1.StateManagementProvider{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "state-management-provider-",
		},
		Spec: kcmv1.StateManagementProviderSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: testLabel,
			},
			Adapter: kcmv1.ResourceReference{
				APIVersion: adapterAPIVersion,
				Kind:       adapterKind,
				Name:       adapterName,
				Namespace:  adapterNamespace,
			},
			Provisioner: []kcmv1.ResourceReference{
				{
					APIVersion: provisionerAPIVersion,
					Kind:       provisionerKind,
					Name:       provisionerName,
					Namespace:  provisionerNamespace,
				},
			},
			ProvisionerCRDs: []kcmv1.ProvisionerCRD{
				{
					Group: provisionerCRDGroup,
					Resources: []string{
						provisionerCRDResource,
					},
				},
			},
		},
	}
}

func prepareServiceSet(namespace, providerName, clusterName string) kcmv1.ServiceSet {
	return kcmv1.ServiceSet{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "service-set-",
			Namespace:    namespace,
			Labels:       testLabel,
			Finalizers:   []string{kcmv1.ServiceSetFinalizer},
		},
		Spec: kcmv1.ServiceSetSpec{
			Cluster: clusterName,
			Provider: kcmv1.StateManagementProviderConfig{
				Name: providerName,
			},
		},
	}
}

func prepareCredential(namespace string) kcmv1.Credential {
	return kcmv1.Credential{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-credential-aws-",
			Namespace:    namespace,
		},
		Spec: kcmv1.CredentialSpec{
			IdentityRef: &corev1.ObjectReference{
				APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
				Kind:       "AWSClusterStaticIdentity",
				Name:       "foo",
			},
		},
	}
}

func prepareClusterDeployment(namespace, credentialName string) kcmv1.ClusterDeployment {
	return kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "cluster-deployment-",
			Namespace:    namespace,
		},
		Spec: kcmv1.ClusterDeploymentSpec{
			Template:   "sample-template",
			Credential: credentialName,
			Config: &apiextv1.JSON{
				Raw: []byte(`{"foo":"bar"}`),
			},
		},
	}
}

func prepareCAPICluster(name, namespace string) clusterapiv1.Cluster {
	return clusterapiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: clusterapiv1.ClusterSpec{Paused: new(false)},
	}
}
