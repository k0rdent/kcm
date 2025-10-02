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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

var _ = Describe("ClusterIPAM Controller", func() {
	createIPAMClaim := func(resourceName, namespace string) kcmv1.ClusterIPAMClaim {
		By("Creating a new ClusterIPAMClaim resource")
		ipPoolSpec := kcmv1.AddressSpaceSpec{}
		return kcmv1.ClusterIPAMClaim{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
			TypeMeta: metav1.TypeMeta{
				Kind:       "ClusterIPAMClaim",
				APIVersion: kcmv1.GroupVersion.String(),
			},
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
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
			TypeMeta: metav1.TypeMeta{
				Kind:       "ClusterIPAM",
				APIVersion: kcmv1.GroupVersion.String(),
			},
			Spec: kcmv1.ClusterIPAMSpec{
				Provider:            kcmv1.InClusterProviderName,
				ClusterIPAMClaimRef: resourceName,
			},
		}
	}

	createCluterDeployment := func(resourceName, namespace string) kcmv1.ClusterDeployment {
		By("Creating a new ClusterDeployment resource")

		return kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
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
				ObjectMeta: metav1.ObjectMeta{GenerateName: "test-namespace-"},
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
})
