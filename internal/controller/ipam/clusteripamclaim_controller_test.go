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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
)

var _ = Describe("ClusterIPAMClaim Controller", func() {
	updateIPAMCounts := func(resource *kcm.ClusterIPAMClaim, nodeDelta, externalDelta, clusterDelta int) {
		By("Modifying the IP count values")
		resource.Spec.NodeIPPool.Count += nodeDelta
		resource.Spec.ExternalIPPool.Count += externalDelta
		resource.Spec.ClusterIPPool.Count += clusterDelta
	}

	testCountUpdates := func(name, namespace string, resource *kcm.ClusterIPAMClaim) {
		By("Updating the ClusterIPAMClaim resource")
		Expect(k8sClient.Update(ctx, resource)).To(Succeed())

		By("Reconciling the resource after update")
		reconciler := &ClusterIPAMClaimReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())

		By("Fetching the updated ClusterIPAM resource")
		clusterIPAM := &kcm.ClusterIPAM{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, clusterIPAM)).To(Succeed())

		By("Validating that the updated claim counts match the expected values")
		Expect(clusterIPAM.Spec.Provider).To(Equal("azure"))
		Expect(clusterIPAM.Spec.ClusterIPClaims).To(HaveLen(resource.Spec.ClusterIPPool.Count))
		Expect(clusterIPAM.Spec.NodeIPClaims).To(HaveLen(resource.Spec.NodeIPPool.Count))
		Expect(clusterIPAM.Spec.ExternalIPClaims).To(HaveLen(resource.Spec.ExternalIPPool.Count))
	}

	createIPAMClaim := func(resourceName, namespace string) kcm.ClusterIPAMClaim {
		By("Creating a new ClusterIPAMClaim resource")
		ipPoolSpec := kcm.IPPoolSpec{
			Count:                     3,
			TypedLocalObjectReference: corev1.TypedLocalObjectReference{Name: "test", Kind: "test"},
		}
		return kcm.ClusterIPAMClaim{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
			Spec: kcm.ClusterIPAMClaimSpec{
				Provider:       "azure",
				ClusterIPPool:  ipPoolSpec,
				NodeIPPool:     ipPoolSpec,
				ExternalIPPool: ipPoolSpec,
			},
		}
	}

	baseReconciliation := func(namespacedName types.NamespacedName) {
		By("Starting reconciliation process")
		reconciler := &ClusterIPAMClaimReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		By("Fetching the reconciled ClusterIPAM resource")
		clusterIPAM := &kcm.ClusterIPAM{}
		Expect(k8sClient.Get(ctx, namespacedName, clusterIPAM)).To(Succeed())

		By("Verifying the provider and expected claim counts")
		Expect(clusterIPAM.Spec.Provider).To(Equal("azure"))
		Expect(clusterIPAM.Spec.ClusterIPClaims).To(HaveLen(3))
		Expect(clusterIPAM.Spec.NodeIPClaims).To(HaveLen(3))
		Expect(clusterIPAM.Spec.ExternalIPClaims).To(HaveLen(3))
	}

	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"
		var namespace corev1.Namespace
		var clusterIPAMClaim *kcm.ClusterIPAMClaim

		BeforeEach(func() {
			By("Ensuring namespace exists")
			namespace = corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{GenerateName: "test-namespace-"},
			}
			Expect(k8sClient.Create(ctx, &namespace)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &namespace)

			By("Creating the custom resource for ClusterIPAMClaim")
			clusterIPAMClaim = &kcm.ClusterIPAMClaim{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: namespace.Name}, clusterIPAMClaim); errors.IsNotFound(err) {
				resource := createIPAMClaim(resourceName, namespace.Name)
				Expect(k8sClient.Create(ctx, &resource)).To(Succeed())
			}
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			baseReconciliation(types.NamespacedName{Name: resourceName, Namespace: namespace.Name})
		})

		DescribeTable("should successfully update IP address claims when updating the counts",
			func(nodeDelta, externalDelta, clusterDelta int) {
				baseReconciliation(types.NamespacedName{Name: resourceName, Namespace: namespace.Name})

				By("Fetching the latest state of the ClusterIPAMClaim")
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: namespace.Name}, clusterIPAMClaim)).To(Succeed())

				By("Updating the IPAM counts")
				updateIPAMCounts(clusterIPAMClaim, nodeDelta, externalDelta, clusterDelta)

				By("Validating the updated count")
				testCountUpdates(resourceName, namespace.Name, clusterIPAMClaim)
			},
			Entry("Increase counts 1, 2 ,3", 1, 2, 3),
			Entry("Decrease counts -1, -2, -3", -1, -2, -3),
			Entry("Increase counts 1, 1, 1", 1, 1, 1),
		)
	})
})
