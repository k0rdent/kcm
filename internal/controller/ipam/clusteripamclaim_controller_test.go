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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func newNamespace() corev1.Namespace {
	return corev1.Namespace{GenerateName: "ipam-test-ns-"}
}

var _ = Describe("ClusterIPAMClaim Controller", func() {
	createIPAMClaim := func(resourceName, namespace string) kcmv1.ClusterIPAMClaim {
		By("Creating a new ClusterIPAMClaim resource")
		ipPoolSpec := kcmv1.AddressSpaceSpec{}
		return kcmv1.ClusterIPAMClaim{
			Name: resourceName, Namespace: namespace,
			Spec: kcmv1.ClusterIPAMClaimSpec{
				Provider:        kcmv1.InClusterProviderName,
				ClusterNetwork:  ipPoolSpec,
				NodeNetwork:     ipPoolSpec,
				ExternalNetwork: ipPoolSpec,
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
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			namespacedName := types.NamespacedName{Name: resourceName, Namespace: namespace.Name}
			reconciler := &ClusterIPAMClaimReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Fetching the reconciled ClusterIPAM resource")
			clusterIPAM := &kcmv1.ClusterIPAM{}
			Expect(k8sClient.Get(ctx, namespacedName, clusterIPAM)).To(Succeed())

			By("Verifying the provider")
			Expect(clusterIPAM.Spec.Provider).To(Equal(kcmv1.InClusterProviderName))
		})
	})

	Context("When reconciling a non-existent ClusterIPAMClaim", func() {
		It("should return nil without error", func() {
			reconciler := &ClusterIPAMClaimReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				Name: "does-not-exist", Namespace: "default",
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When the claim has a deletion timestamp", func() {
		const name = "deletion-timestamp-claim"
		var ns corev1.Namespace

		BeforeEach(func() {
			ns = newNamespace()
			Expect(k8sClient.Create(ctx, &ns)).To(Succeed())

			claim := createIPAMClaim(name, ns.Name)
			Expect(k8sClient.Create(ctx, &claim)).To(Succeed())

			claim.Finalizers = []string{"test.io/fake-finalizer"}
			Expect(k8sClient.Update(ctx, &claim)).To(Succeed())

			Expect(k8sClient.Delete(ctx, &claim)).To(Succeed())
		})

		AfterEach(func() {
			existing := &kcmv1.ClusterIPAMClaim{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns.Name}, existing); err == nil {
				existing.Finalizers = nil
				Expect(k8sClient.Update(ctx, existing)).To(Succeed())
			}
			Expect(k8sClient.Delete(ctx, &ns)).To(Succeed())
		})

		It("should return nil and not create a ClusterIPAM", func() {
			reconciler := &ClusterIPAMClaimReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				Name: name, Namespace: ns.Name,
			})
			Expect(err).NotTo(HaveOccurred())

			clusterIPAM := &kcmv1.ClusterIPAM{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns.Name}, clusterIPAM)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When the claim has an invalid CIDR", func() {
		const name = "invalid-cidr-claim"
		var ns corev1.Namespace

		BeforeEach(func() {
			ns = newNamespace()
			Expect(k8sClient.Create(ctx, &ns)).To(Succeed())

			claim := kcmv1.ClusterIPAMClaim{
				Name: name, Namespace: ns.Name,
				Spec: kcmv1.ClusterIPAMClaimSpec{
					Provider: kcmv1.InClusterProviderName,
					NodeNetwork: kcmv1.AddressSpaceSpec{
						CIDR: "not-a-valid-cidr",
					},
				},
			}
			Expect(k8sClient.Create(ctx, &claim)).To(Succeed())
		})

		AfterEach(func() {
			claim := &kcmv1.ClusterIPAMClaim{Name: name, Namespace: ns.Name}
			Expect(k8sClient.Delete(ctx, claim)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &ns)).To(Succeed())
		})

		It("should return nil without creating a ClusterIPAM", func() {
			reconciler := &ClusterIPAMClaimReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				Name: name, Namespace: ns.Name,
			})
			Expect(err).NotTo(HaveOccurred())

			clusterIPAM := &kcmv1.ClusterIPAM{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns.Name}, clusterIPAM)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When updateStatus is called with a claim containing an invalid CIDR", func() {
		const name = "updatestatus-invalid-cidr"
		var ns corev1.Namespace

		BeforeEach(func() {
			ns = newNamespace()
			Expect(k8sClient.Create(ctx, &ns)).To(Succeed())

			claim := kcmv1.ClusterIPAMClaim{
				Name: name, Namespace: ns.Name,
				Spec: kcmv1.ClusterIPAMClaimSpec{
					Provider: kcmv1.InClusterProviderName,
					NodeNetwork: kcmv1.AddressSpaceSpec{
						CIDR: "192.168.1.0/33",
					},
				},
			}
			Expect(k8sClient.Create(ctx, &claim)).To(Succeed())
		})

		AfterEach(func() {
			claim := &kcmv1.ClusterIPAMClaim{Name: name, Namespace: ns.Name}
			_ = k8sClient.Delete(ctx, claim)
			Expect(k8sClient.Delete(ctx, &ns)).To(Succeed())
		})

		It("should persist an InvalidClaimCondition in status", func() {
			namespacedName := types.NamespacedName{Name: name, Namespace: ns.Name}
			claim := &kcmv1.ClusterIPAMClaim{}
			Expect(k8sClient.Get(ctx, namespacedName, claim)).To(Succeed())

			reconciler := &ClusterIPAMClaimReconciler{Client: k8sClient}
			Expect(reconciler.updateStatus(ctx, claim)).To(Succeed())

			updated := &kcmv1.ClusterIPAMClaim{}
			Expect(k8sClient.Get(ctx, namespacedName, updated)).To(Succeed())
			Expect(updated.Status.Conditions).To(ContainElement(
				HaveField("Type", kcmv1.InvalidClaimConditionType),
			))
		})
	})

	Context("When ClusterIPAMRef is already set on the claim", func() {
		const name = "ref-already-set-claim"
		var ns corev1.Namespace

		BeforeEach(func() {
			ns = newNamespace()
			Expect(k8sClient.Create(ctx, &ns)).To(Succeed())

			claim := createIPAMClaim(name, ns.Name)
			claim.Spec.ClusterIPAMRef = name
			Expect(k8sClient.Create(ctx, &claim)).To(Succeed())
		})

		AfterEach(func() {
			clusterIPAM := &kcmv1.ClusterIPAM{Name: name, Namespace: ns.Name}
			_ = k8sClient.Delete(ctx, clusterIPAM)
			claim := &kcmv1.ClusterIPAMClaim{Name: name, Namespace: ns.Name}
			Expect(k8sClient.Delete(ctx, claim)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &ns)).To(Succeed())
		})

		It("should reconcile without updating the claim and create ClusterIPAM", func() {
			namespacedName := types.NamespacedName{Name: name, Namespace: ns.Name}
			reconciler := &ClusterIPAMClaimReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			clusterIPAM := &kcmv1.ClusterIPAM{}
			Expect(k8sClient.Get(ctx, namespacedName, clusterIPAM)).To(Succeed())
			Expect(clusterIPAM.Spec.Provider).To(Equal(kcmv1.InClusterProviderName))
		})
	})
})
