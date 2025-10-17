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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	am "github.com/K0rdent/kcm/test/objects/accessmanagement"
	"github.com/K0rdent/kcm/test/objects/credential"
	tc "github.com/K0rdent/kcm/test/objects/templatechain"
)

var _ = Describe("Template Management Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			amName = "kcm-am"

			ctChainName = "kcm-ct-chain"
			stChainName = "kcm-st-chain"
			credName    = "test-cred"

			ctChainToDeleteName = "kcm-ct-chain-to-delete"
			stChainToDeleteName = "kcm-st-chain-to-delete"
			credToDeleteName    = "test-cred-to-delete"

			namespace1Name = "namespace1"
			namespace2Name = "namespace2"
			namespace3Name = "namespace3"

			ctChainUnmanagedName = "ct-chain-unmanaged"
			stChainUnmanagedName = "st-chain-unmanaged"
			credUnmanagedName    = "test-cred-unmanaged"
		)

		credIdentityRef := &corev1.ObjectReference{
			Kind: "AWSClusterStaticIdentity",
			Name: "awsclid",
		}

		ctx := context.Background()

		systemNamespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kcm",
			},
		}

		namespace1 := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   namespace1Name,
				Labels: map[string]string{"environment": "dev", "test": "test"},
			},
		}
		namespace2 := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   namespace2Name,
				Labels: map[string]string{"environment": "prod"},
			},
		}
		namespace3 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace3Name}}

		accessRules := []kcmv1.AccessRule{
			{
				// Target namespaces: namespace1, namespace2
				TargetNamespaces: kcmv1.TargetNamespaces{
					Selector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "environment",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"prod", "dev"},
							},
						},
					},
				},
				ClusterTemplateChains: []string{ctChainName},
				Credentials:           []string{credName},
			},
			{
				// Target namespace: namespace1
				TargetNamespaces: kcmv1.TargetNamespaces{
					StringSelector: "environment=dev",
				},
				ClusterTemplateChains: []string{ctChainName},
				ServiceTemplateChains: []string{stChainName},
				Credentials:           []string{credName},
			},
			{
				// Target namespace: namespace3
				TargetNamespaces: kcmv1.TargetNamespaces{
					List: []string{namespace3Name},
				},
				ServiceTemplateChains: []string{stChainName},
			},
		}

		am := am.NewAccessManagement(
			am.WithName(amName),
			am.WithAccessRules(accessRules),
			am.WithLabels(kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM),
		)

		ctChain := tc.NewClusterTemplateChain(tc.WithName(ctChainName), tc.WithNamespace(systemNamespace.Name), tc.ManagedByKCM())
		stChain := tc.NewServiceTemplateChain(tc.WithName(stChainName), tc.WithNamespace(systemNamespace.Name), tc.ManagedByKCM())

		ctChainToDelete := tc.NewClusterTemplateChain(tc.WithName(ctChainToDeleteName), tc.WithNamespace(namespace2Name), tc.ManagedByKCM())
		stChainToDelete := tc.NewServiceTemplateChain(tc.WithName(stChainToDeleteName), tc.WithNamespace(namespace3Name), tc.ManagedByKCM())

		ctChainUnmanaged := tc.NewClusterTemplateChain(tc.WithName(ctChainUnmanagedName), tc.WithNamespace(namespace1Name))
		stChainUnmanaged := tc.NewServiceTemplateChain(tc.WithName(stChainUnmanagedName), tc.WithNamespace(namespace2Name))

		cred := credential.NewCredential(
			credential.WithName(credName),
			credential.WithNamespace(systemNamespace.Name),
			credential.ManagedByKCM(),
			credential.WithIdentityRef(credIdentityRef),
		)
		credToDelete := credential.NewCredential(
			credential.WithName(credToDeleteName),
			credential.WithNamespace(namespace3Name),
			credential.ManagedByKCM(),
			credential.WithIdentityRef(credIdentityRef),
		)
		credUnmanaged := credential.NewCredential(
			credential.WithName(credUnmanagedName),
			credential.WithNamespace(namespace2Name),
			credential.WithIdentityRef(credIdentityRef),
		)

		BeforeEach(func() {
			By("creating test namespaces")
			var err error
			for _, ns := range []*corev1.Namespace{systemNamespace, namespace1, namespace2, namespace3} {
				err = k8sClient.Get(ctx, types.NamespacedName{Name: ns.Name}, ns)
				if err != nil && apierrors.IsNotFound(err) {
					Expect(k8sClient.Create(ctx, ns)).To(Succeed())
				}
			}
			By("creating the custom resource for the Kind AccessManagement")
			err = k8sClient.Get(ctx, types.NamespacedName{Name: amName}, am)
			if err != nil && apierrors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, am)).To(Succeed())
			}

			By("creating custom resources for the Kind ClusterTemplateChain, ServiceTemplateChain and Credentials")
			for _, obj := range []crclient.Object{
				ctChain, ctChainToDelete, ctChainUnmanaged,
				stChain, stChainToDelete, stChainUnmanaged,
				cred, credToDelete, credUnmanaged,
			} {
				err = k8sClient.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, obj)
				if err != nil && apierrors.IsNotFound(err) {
					Expect(k8sClient.Create(ctx, obj)).To(Succeed())
				}
			}
		})

		AfterEach(func() {
			for _, chain := range []*kcmv1.ClusterTemplateChain{ctChain, ctChainToDelete, ctChainUnmanaged} {
				for _, ns := range []*corev1.Namespace{systemNamespace, namespace1, namespace2, namespace3} {
					chain.Namespace = ns.Name
					err := k8sClient.Delete(ctx, chain)
					Expect(crclient.IgnoreNotFound(err)).To(Succeed())
				}
			}
			for _, chain := range []*kcmv1.ServiceTemplateChain{stChain, stChainToDelete, stChainUnmanaged} {
				for _, ns := range []*corev1.Namespace{systemNamespace, namespace1, namespace2, namespace3} {
					chain.Namespace = ns.Name
					err := k8sClient.Delete(ctx, chain)
					Expect(crclient.IgnoreNotFound(err)).To(Succeed())
				}
			}
			for _, c := range []*kcmv1.Credential{cred, credToDelete, credUnmanaged} {
				for _, ns := range []*corev1.Namespace{systemNamespace, namespace1, namespace2, namespace3} {
					c.Namespace = ns.Name
					err := k8sClient.Delete(ctx, c)
					Expect(crclient.IgnoreNotFound(err)).To(Succeed())
				}
			}
			for _, ns := range []*corev1.Namespace{namespace1, namespace2, namespace3} {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ns.Name}, ns)
				Expect(err).NotTo(HaveOccurred())
				By("Cleanup the namespace")
				Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
			}
		})
		It("should successfully reconcile the resource", func() {
			By("Get unmanaged template chains before the reconciliation to verify it wasn't changed")
			ctChainUnmanagedBefore := &kcmv1.ClusterTemplateChain{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ctChainUnmanaged.Namespace, Name: ctChainUnmanaged.Name}, ctChainUnmanagedBefore)
			Expect(err).NotTo(HaveOccurred())

			stChainUnmanagedBefore := &kcmv1.ServiceTemplateChain{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: stChainUnmanaged.Namespace, Name: stChainUnmanaged.Name}, stChainUnmanagedBefore)
			Expect(err).NotTo(HaveOccurred())

			credUnmanagedBefore := &kcmv1.Credential{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: credUnmanaged.Namespace, Name: credUnmanaged.Name}, credUnmanagedBefore)
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling the created resource")
			controllerReconciler := &AccessManagementReconciler{
				Client:          k8sClient,
				SystemNamespace: systemNamespace.Name,
			}
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: amName},
			})
			Expect(err).NotTo(HaveOccurred())
			/*
				Expected state:
					* namespace1/kcm-ct-chain - should be created
					* namespace1/kcm-st-chain - should be created
					* namespace2/kcm-ct-chain - should be created
					* namespace3/kcm-st-chain - should be created
					* namespace1/ct-chain-unmanaged - should be unchanged (unmanaged by KCM)
					* namespace2/st-chain-unmanaged - should be unchanged (unmanaged by KCM)
					* namespace2/kcm-ct-chain-to-delete - should be deleted
					* namespace3/kcm-st-chain-to-delete - should be deleted

					* namespace1/test-cred - should be created
					* namespace2/test-cred - should be created
					* namespace2/test-cred-unmanaged - should be unchanged (unmanaged by KCM)
					* namespace3/test-cred-to delete - should be deleted
			*/
			verifyObjectCreated(ctx, namespace1Name, ctChain)
			verifyObjectCreated(ctx, namespace1Name, stChain)
			verifyObjectCreated(ctx, namespace2Name, ctChain)
			verifyObjectCreated(ctx, namespace3Name, stChain)
			verifyObjectCreated(ctx, namespace1Name, cred)
			verifyObjectCreated(ctx, namespace2Name, cred)

			verifyObjectUnchanged(ctx, namespace1Name, ctChainUnmanaged, ctChainUnmanagedBefore)
			verifyObjectUnchanged(ctx, namespace2Name, stChainUnmanaged, stChainUnmanagedBefore)
			verifyObjectUnchanged(ctx, namespace2Name, credUnmanaged, credUnmanagedBefore)

			verifyObjectDeleted(ctx, namespace2Name, ctChainToDelete)
			verifyObjectDeleted(ctx, namespace3Name, stChainToDelete)
			verifyObjectDeleted(ctx, namespace3Name, credToDelete)
		})
	})
})
