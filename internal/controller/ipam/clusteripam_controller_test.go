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

package ipam

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	inclusteripam "sigs.k8s.io/cluster-api-ipam-provider-in-cluster/api/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

var _ = Describe("ClusterIPAM Controller", func() {
	createIPAMClaim := func(resourceName, namespace string) kcmv1.ClusterIPAMClaim {
		By("Creating a new ClusterIPAMClaim resource")
		ipPoolSpec := kcmv1.AddressSpaceSpec{}
		return kcmv1.ClusterIPAMClaim{
			Name: resourceName, Namespace: namespace,
			Kind:       "ClusterIPAMClaim",
			APIVersion: kcmv1.GroupVersion.String(),
			Spec: kcmv1.ClusterIPAMClaimSpec{
				Provider:        kcmv1.InClusterProviderName,
				ClusterNetwork:  ipPoolSpec,
				NodeNetwork:     ipPoolSpec,
				ExternalNetwork: ipPoolSpec,
				Cluster:         resourceName,
			},
		}
	}

	createIPAM := func(resourceName, namespace string) kcmv1.ClusterIPAM {
		By("Creating a new ClusterIPAM resource")
		return kcmv1.ClusterIPAM{
			Name: resourceName, Namespace: namespace,
			Kind:       "ClusterIPAM",
			APIVersion: kcmv1.GroupVersion.String(),
			Spec: kcmv1.ClusterIPAMSpec{
				Provider:            kcmv1.InClusterProviderName,
				ClusterIPAMClaimRef: resourceName,
			},
		}
	}

	createCluterDeployment := func(resourceName, namespace string) kcmv1.ClusterDeployment {
		By("Creating a new ClusterDeployment resource")

		return kcmv1.ClusterDeployment{
			Name: resourceName, Namespace: namespace,
			Spec: kcmv1.ClusterDeploymentSpec{
				Template: "test",
			},
		}
	}

	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"
		var namespace corev1.Namespace
		var clusterIPAMClaim *kcmv1.ClusterIPAMClaim

		BeforeEach(func() {
			By("Ensuring namespace exists")
			namespace = corev1.Namespace{
				GenerateName: "test-namespace-",
			}
			Expect(k8sClient.Create(ctx, &namespace)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &namespace)

			By("Creating the custom resource for ClusterIPAMClaim")
			clusterIPAMClaim = &kcmv1.ClusterIPAMClaim{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: namespace.Name}, clusterIPAMClaim); apierrors.IsNotFound(err) {
				resource := createIPAMClaim(resourceName, namespace.Name)
				Expect(k8sClient.Create(ctx, &resource)).To(Succeed())
			}

			clusterIPAM := &kcmv1.ClusterIPAM{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: namespace.Name}, clusterIPAM); apierrors.IsNotFound(err) {
				resource := createIPAM(resourceName, namespace.Name)
				Expect(k8sClient.Create(ctx, &resource)).To(Succeed())
			}

			clusterDeployment := &kcmv1.ClusterDeployment{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: namespace.Name}, clusterDeployment); apierrors.IsNotFound(err) {
				resource := createCluterDeployment(resourceName, namespace.Name)
				Expect(k8sClient.Create(ctx, &resource)).To(Succeed())
			}
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")

			namespacedName := types.NamespacedName{Name: resourceName, Namespace: namespace.Name}
			reconciler := &ClusterIPAMReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Fetching the reconciled ClusterIPAM resource")
			clusterIPAM := &kcmv1.ClusterIPAM{}
			Expect(k8sClient.Get(ctx, namespacedName, clusterIPAM)).To(Succeed())

			By("Verifying the provider")
			Expect(clusterIPAM.Spec.Provider).To(Equal(kcmv1.InClusterProviderName))
		})
	})

	Context("When processProvider is called with an unsupported provider", func() {
		It("returns an adapter builder error", func() {
			reconciler := &ClusterIPAMReconciler{Client: k8sClient}
			claim := &kcmv1.ClusterIPAMClaim{
				Spec: kcmv1.ClusterIPAMClaimSpec{Provider: "unknown-provider"},
			}
			_, err := reconciler.processProvider(ctx, claim)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to build IPAM adapter"))
		})
	})

	Context("When ClusterIPAM does not exist", func() {
		It("should return nil without error", func() {
			reconciler := &ClusterIPAMReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				Name: "does-not-exist", Namespace: "default",
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When processProvider fails because the IPAM provider CRD is not installed", func() {
		const name = "infoblox-no-crd"
		var ns corev1.Namespace

		BeforeEach(func() {
			ns = newNamespace()
			Expect(k8sClient.Create(ctx, &ns)).To(Succeed())

			claim := kcmv1.ClusterIPAMClaim{
				Name: name, Namespace: ns.Name,
				Spec: kcmv1.ClusterIPAMClaimSpec{
					Provider: kcmv1.InfobloxProviderName,
					Cluster:  name,
					NodeNetwork: kcmv1.AddressSpaceSpec{
						CIDR: "10.0.0.0/24",
					},
				},
			}
			Expect(k8sClient.Create(ctx, &claim)).To(Succeed())

			ipam := createIPAM(name, ns.Name)
			Expect(k8sClient.Create(ctx, &ipam)).To(Succeed())
		})

		AfterEach(func() {
			ipam := &kcmv1.ClusterIPAM{Name: name, Namespace: ns.Name}
			_ = k8sClient.Delete(ctx, ipam)
			claim := &kcmv1.ClusterIPAMClaim{Name: name, Namespace: ns.Name}
			_ = k8sClient.Delete(ctx, claim)
			Expect(k8sClient.Delete(ctx, &ns)).To(Succeed())
		})

		It("should return an error from Reconcile containing processProvider failure", func() {
			reconciler := &ClusterIPAMReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				Name: name, Namespace: ns.Name,
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create provider specific data"))
		})
	})

	Context("When the ClusterIPAMClaim referenced by ClusterIPAM does not exist", func() {
		const name = "orphan-ipam"
		var ns corev1.Namespace

		BeforeEach(func() {
			ns = newNamespace()
			Expect(k8sClient.Create(ctx, &ns)).To(Succeed())

			ipam := createIPAM(name, ns.Name)
			Expect(k8sClient.Create(ctx, &ipam)).To(Succeed())
		})

		AfterEach(func() {
			ipam := &kcmv1.ClusterIPAM{Name: name, Namespace: ns.Name}
			_ = k8sClient.Delete(ctx, ipam)
			Expect(k8sClient.Delete(ctx, &ns)).To(Succeed())
		})

		It("should return an error for missing ClusterIPAMClaim", func() {
			reconciler := &ClusterIPAMReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				Name: name, Namespace: ns.Name,
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get ClusterIPAMClaim"))
		})
	})

	Context("When the InCluster adapter pool is ready", func() {
		const name = "ready-ipam-test"
		var ns corev1.Namespace

		BeforeEach(func() {
			ns = newNamespace()
			Expect(k8sClient.Create(ctx, &ns)).To(Succeed())

			claim := kcmv1.ClusterIPAMClaim{
				Name: name, Namespace: ns.Name,
				Spec: kcmv1.ClusterIPAMClaimSpec{
					Provider: kcmv1.InClusterProviderName,
					Cluster:  name,
					NodeNetwork: kcmv1.AddressSpaceSpec{
						CIDR:    "192.168.1.0/24",
						Gateway: "192.168.1.1",
						Prefix:  24,
					},
				},
			}
			Expect(k8sClient.Create(ctx, &claim)).To(Succeed())

			pool := &inclusteripam.InClusterIPPool{
				Name: name, Namespace: ns.Name,
				Spec: inclusteripam.InClusterIPPoolSpec{
					Addresses: []string{"192.168.1.0/24"},
					Prefix:    24,
					Gateway:   "192.168.1.1",
				},
			}
			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
			pool.Status = inclusteripam.InClusterIPPoolStatus{
				Addresses: &inclusteripam.InClusterIPPoolStatusIPAddresses{Total: 10},
			}
			Expect(k8sClient.Status().Update(ctx, pool)).To(Succeed())

			ipam := createIPAM(name, ns.Name)
			Expect(k8sClient.Create(ctx, &ipam)).To(Succeed())
		})

		AfterEach(func() {
			ipam := &kcmv1.ClusterIPAM{Name: name, Namespace: ns.Name}
			_ = k8sClient.Delete(ctx, ipam)
			pool := &inclusteripam.InClusterIPPool{Name: name, Namespace: ns.Name}
			_ = k8sClient.Delete(ctx, pool)
			claim := &kcmv1.ClusterIPAMClaim{Name: name, Namespace: ns.Name}
			_ = k8sClient.Delete(ctx, claim)
			Expect(k8sClient.Delete(ctx, &ns)).To(Succeed())
		})

		It("should set ClusterIPAM Phase to Bound", func() {
			namespacedName := types.NamespacedName{Name: name, Namespace: ns.Name}
			reconciler := &ClusterIPAMReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			updated := &kcmv1.ClusterIPAM{}
			Expect(k8sClient.Get(ctx, namespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(kcmv1.ClusterIPAMPhaseBound))
		})
	})
})
