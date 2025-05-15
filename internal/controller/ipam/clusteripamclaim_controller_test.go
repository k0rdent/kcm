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

	kcm "github.com/K0rdent/kcm/api/v1beta1"
)

var _ = Describe("ClusterIPAMClaim Controller", func() {
	createIPAMClaim := func(resourceName, namespace string) kcm.ClusterIPAMClaim {
		By("Creating a new ClusterIPAMClaim resource")
		ipPoolSpec := kcm.AddressSpaceSpec{}
		return kcm.ClusterIPAMClaim{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
			Spec: kcm.ClusterIPAMClaimSpec{
				Provider:        kcm.InClusterProviderName,
				ClusterNetwork:  ipPoolSpec,
				NodeNetwork:     ipPoolSpec,
				ExternalNetwork: ipPoolSpec,
			},
		}
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
			namespacedName := types.NamespacedName{Name: resourceName, Namespace: namespace.Name}
			reconciler := &ClusterIPAMClaimReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Fetching the reconciled ClusterIPAM resource")
			clusterIPAM := &kcm.ClusterIPAM{}
			Expect(k8sClient.Get(ctx, namespacedName, clusterIPAM)).To(Succeed())

			By("Verifying the provider")
			Expect(clusterIPAM.Spec.Provider).To(Equal(kcm.InClusterProviderName))
		})
	})
})
