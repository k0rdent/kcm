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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	pointerutil "github.com/K0rdent/kcm/internal/util/pointer"
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
		Spec: clusterapiv1.ClusterSpec{Paused: pointerutil.To(false)},
	}
}
