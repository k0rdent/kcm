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
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	am "github.com/K0rdent/kcm/test/objects/accessmanagement"
	"github.com/K0rdent/kcm/test/objects/clusterauditpolicy"
	"github.com/K0rdent/kcm/test/objects/clusterauthentication"
	"github.com/K0rdent/kcm/test/objects/credential"
	"github.com/K0rdent/kcm/test/objects/datasource"
	"github.com/K0rdent/kcm/test/objects/management"
	tc "github.com/K0rdent/kcm/test/objects/templatechain"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

const (
	genericTestSystemNamespace = "kcm-system"
	genericTestTargetNamespace = "team-a"
)

var (
	widgetGVK = schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	widgetGVR = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}

	clusterWidgetGVK = schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ClusterWidget"}
)

var _ = Describe("Template Management Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			amName = "kcm-am"

			ctChainName = "kcm-ct-chain"
			stChainName = "kcm-st-chain"
			credName    = "test-cred"
			clAuthName  = "cl-auth"
			dsName      = "datasource-name"
			capName     = "cl-audit-policy"

			ctChainToDeleteName = "kcm-ct-chain-to-delete"
			stChainToDeleteName = "kcm-st-chain-to-delete"
			credToDeleteName    = "test-cred-to-delete"
			clAuthToDeleteName  = "cl-auth-to-delete"
			dsToDeleteName      = "datasource-to-delete"
			capToDeleteName     = "cl-audit-policy-to-delete"

			namespace1Name = "namespace1"
			namespace2Name = "namespace2"
			namespace3Name = "namespace3"

			ctChainUnmanagedName = "ct-chain-unmanaged"
			stChainUnmanagedName = "st-chain-unmanaged"
			credUnmanagedName    = "test-cred-unmanaged"
			clAuthUnmanagedName  = "cl-auth-unmanaged"
			dsUnmanagedName      = "datasource-unmanaged"
			capUnmanagedName     = "cl-audit-policy-unmanaged"
		)

		credIdentityRef := &corev1.ObjectReference{
			Kind: "AWSClusterStaticIdentity",
			Name: "awsclid",
		}

		caSecretRef := kcmv1.SecretKeyReference{
			SecretReference: corev1.SecretReference{
				Name: "ca-secret",
			},
			Key: "ca.crt",
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
				ClusterTemplateChains:  []string{ctChainName},
				Credentials:            []string{credName},
				ClusterAuthentications: []string{clAuthName},
				DataSources:            []string{dsName},
				ClusterAuditPolicies:   []string{capName},
			},
			{
				// Target namespace: namespace1
				TargetNamespaces: kcmv1.TargetNamespaces{
					StringSelector: "environment=dev",
				},
				ClusterTemplateChains:  []string{ctChainName},
				ServiceTemplateChains:  []string{stChainName},
				Credentials:            []string{credName},
				ClusterAuthentications: []string{clAuthName},
				DataSources:            []string{dsName},
				ClusterAuditPolicies:   []string{capName},
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

		clAuth := clusterauthentication.New(
			clusterauthentication.WithName(clAuthName),
			clusterauthentication.WithNamespace(systemNamespace.Name),
			clusterauthentication.WithCASecretRef(caSecretRef),
			clusterauthentication.ManagedByKCM(),
		)
		clAuthToDelete := clusterauthentication.New(
			clusterauthentication.WithName(clAuthToDeleteName),
			clusterauthentication.WithNamespace(namespace3Name),
			clusterauthentication.WithCASecretRef(caSecretRef),
			clusterauthentication.ManagedByKCM(),
		)
		clAuthUnmanaged := clusterauthentication.New(
			clusterauthentication.WithName(clAuthUnmanagedName),
			clusterauthentication.WithNamespace(namespace2Name),
			clusterauthentication.WithCASecretRef(caSecretRef),
		)

		dsObj := datasource.New(
			datasource.WithName(dsName),
			datasource.WithNamespace(systemNamespace.Name),
			datasource.WithLabels(kcmv1.KCMManagedLabelKey, kcmv1.KCMManagedLabelValue),
		)
		dsToDelete := datasource.New(
			datasource.WithName(dsToDeleteName),
			datasource.WithNamespace(namespace3Name),
			datasource.WithLabels(kcmv1.KCMManagedLabelKey, kcmv1.KCMManagedLabelValue),
		)
		dsUnmanaged := datasource.New(
			datasource.WithName(dsUnmanagedName),
			datasource.WithNamespace(namespace2Name),
		)

		capSpec := kcmv1.ClusterAuditPolicySpec{
			Policy: kcmv1.Policy{
				Rules: []auditv1.PolicyRule{
					{
						Level: auditv1.LevelMetadata,
					},
				},
			},
		}

		capObj := clusterauditpolicy.New(
			clusterauditpolicy.WithName(capName),
			clusterauditpolicy.WithNamespace(systemNamespace.Name),
			clusterauditpolicy.WithSpec(capSpec),
			clusterauditpolicy.ManagedByKCM(),
		)
		capToDelete := clusterauditpolicy.New(
			clusterauditpolicy.WithName(capToDeleteName),
			clusterauditpolicy.WithNamespace(namespace3Name),
			clusterauditpolicy.WithSpec(capSpec),
			clusterauditpolicy.ManagedByKCM(),
		)
		capUnmanaged := clusterauditpolicy.New(
			clusterauditpolicy.WithName(capUnmanagedName),
			clusterauditpolicy.WithNamespace(namespace2Name),
			clusterauditpolicy.WithSpec(capSpec),
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

			By("creating custom resources for the Kind ClusterTemplateChain, ServiceTemplateChain, Credentials, ClusterAuthentications, DataSources, ClusterAuditPolicies")
			for _, obj := range []client.Object{
				ctChain, ctChainToDelete, ctChainUnmanaged,
				stChain, stChainToDelete, stChainUnmanaged,
				cred, credToDelete, credUnmanaged,
				clAuth, clAuthToDelete, clAuthUnmanaged,
				dsObj, dsToDelete, dsUnmanaged,
				capObj, capToDelete, capUnmanaged,
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
					Expect(client.IgnoreNotFound(err)).To(Succeed())
				}
			}
			for _, chain := range []*kcmv1.ServiceTemplateChain{stChain, stChainToDelete, stChainUnmanaged} {
				for _, ns := range []*corev1.Namespace{systemNamespace, namespace1, namespace2, namespace3} {
					chain.Namespace = ns.Name
					err := k8sClient.Delete(ctx, chain)
					Expect(client.IgnoreNotFound(err)).To(Succeed())
				}
			}
			for _, c := range []*kcmv1.Credential{cred, credToDelete, credUnmanaged} {
				for _, ns := range []*corev1.Namespace{systemNamespace, namespace1, namespace2, namespace3} {
					c.Namespace = ns.Name
					err := k8sClient.Delete(ctx, c)
					Expect(client.IgnoreNotFound(err)).To(Succeed())
				}
			}
			for _, clAuth := range []*kcmv1.ClusterAuthentication{clAuth, clAuthToDelete, clAuthUnmanaged} {
				for _, ns := range []*corev1.Namespace{systemNamespace, namespace1, namespace2, namespace3} {
					clAuth.Namespace = ns.Name
					err := k8sClient.Delete(ctx, clAuth)
					Expect(client.IgnoreNotFound(err)).To(Succeed())
				}
			}

			for _, ds := range []*kcmv1.DataSource{dsObj, dsToDelete, dsUnmanaged} {
				for _, ns := range []*corev1.Namespace{systemNamespace, namespace1, namespace2, namespace3} {
					ds.Namespace = ns.Name
					Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ds))).To(Succeed())
				}
			}

			for _, cap := range []*kcmv1.ClusterAuditPolicy{capObj, capToDelete, capUnmanaged} {
				for _, ns := range []*corev1.Namespace{systemNamespace, namespace1, namespace2, namespace3} {
					cap.Namespace = ns.Name
					Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cap))).To(Succeed())
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
			By("Get unmanaged objects before the reconciliation to verify it wasn't changed")
			ctChainUnmanagedBefore := &kcmv1.ClusterTemplateChain{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ctChainUnmanaged.Namespace, Name: ctChainUnmanaged.Name}, ctChainUnmanagedBefore)
			Expect(err).NotTo(HaveOccurred())

			stChainUnmanagedBefore := &kcmv1.ServiceTemplateChain{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: stChainUnmanaged.Namespace, Name: stChainUnmanaged.Name}, stChainUnmanagedBefore)
			Expect(err).NotTo(HaveOccurred())

			credUnmanagedBefore := &kcmv1.Credential{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: credUnmanaged.Namespace, Name: credUnmanaged.Name}, credUnmanagedBefore)
			Expect(err).NotTo(HaveOccurred())

			clAuthUnmanagedBefore := &kcmv1.ClusterAuthentication{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: clAuthUnmanaged.Namespace, Name: clAuthUnmanaged.Name}, clAuthUnmanagedBefore)
			Expect(err).NotTo(HaveOccurred())

			dsUnmanagedBefore := new(kcmv1.DataSource)
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: dsUnmanaged.Namespace, Name: dsUnmanaged.Name}, dsUnmanagedBefore)
			Expect(err).NotTo(HaveOccurred())

			capUnmanagedBefore := new(kcmv1.ClusterAuditPolicy)
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: capUnmanaged.Namespace, Name: capUnmanaged.Name}, capUnmanagedBefore)
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling the created resource")
			controllerReconciler := &AccessManagementReconciler{
				Client:          k8sClient,
				SystemNamespace: systemNamespace.Name,
				RESTMapper:      k8sClient.RESTMapper(),
				DynamicClient:   dynamicClient,
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

					* namespace1/cl-auth - should be created
					* namespace2/cl-auth - should be created
					* namespace2/cl-auth-unmanaged - should be unchanged (unmanaged by KCM)
					* namespace3/cl-auth-to delete - should be deleted

					* namespace1/datasource-name - should be created
					* namespace2/datasource-name - should be created
					* namespace2/datasource-unmanaged - should be unchanged (unmanaged by KCM)
					* namespace3/datasource-to delete - should be deleted

					* namespace1/cl-audit-policy - should be created
					* namespace2/cl-audit-policy - should be created
					* namespace2/cl-audit-policy-unmanaged - should be unchanged (unmanaged by KCM)
					* namespace3/cl-audit-policy-to-delete - should be deleted
			*/
			verifyObjectCreated(ctx, namespace1Name, ctChain)
			verifyObjectCreated(ctx, namespace1Name, stChain)
			verifyObjectCreated(ctx, namespace2Name, ctChain)
			verifyObjectCreated(ctx, namespace3Name, stChain)
			verifyObjectCreated(ctx, namespace1Name, cred)
			verifyObjectCreated(ctx, namespace2Name, cred)
			verifyObjectCreated(ctx, namespace1Name, clAuth)
			verifyObjectCreated(ctx, namespace2Name, clAuth)
			verifyObjectCreated(ctx, namespace1Name, dsObj)
			verifyObjectCreated(ctx, namespace2Name, dsObj)
			verifyObjectCreated(ctx, namespace1Name, capObj)
			verifyObjectCreated(ctx, namespace2Name, capObj)

			verifyObjectUnchanged(ctx, namespace1Name, ctChainUnmanagedBefore, ctChainUnmanaged)
			verifyObjectUnchanged(ctx, namespace2Name, stChainUnmanagedBefore, stChainUnmanaged)
			verifyObjectUnchanged(ctx, namespace2Name, credUnmanagedBefore, credUnmanaged)
			verifyObjectUnchanged(ctx, namespace2Name, clAuthUnmanagedBefore, clAuthUnmanaged)
			verifyObjectUnchanged(ctx, namespace2Name, dsUnmanagedBefore, dsUnmanaged)
			verifyObjectUnchanged(ctx, namespace2Name, capUnmanagedBefore, capUnmanaged)

			verifyObjectDeleted(ctx, namespace2Name, ctChainToDelete)
			verifyObjectDeleted(ctx, namespace3Name, stChainToDelete)
			verifyObjectDeleted(ctx, namespace3Name, credToDelete)
			verifyObjectDeleted(ctx, namespace3Name, clAuthToDelete)
			verifyObjectDeleted(ctx, namespace3Name, dsToDelete)
			verifyObjectDeleted(ctx, namespace3Name, capToDelete)
		})
	})

	Context("ResourceRule CEL validation", func() {
		It("should reject a ResourceRule with an explicitly empty names list and no selector", func() {
			invalid := am.NewAccessManagement(
				am.WithName("kcm-am-cel-empty-names"),
				am.WithAccessRules([]kcmv1.AccessRule{
					{
						Resources: []kcmv1.ResourceRule{
							{Kind: kcmv1.CredentialKind, Names: []string{}},
						},
					},
				}),
			)

			err := k8sClient.Create(context.Background(), invalid)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("names must not be empty"))
		})
	})
})

func TestMapNamespaceToRequests(t *testing.T) {
	t.Parallel()

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "project-a",
			Labels: map[string]string{"env": "prod", "tier": "frontend"},
		},
	}

	accessManagements := []client.Object{
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "selector-string"},
			Spec: kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{
				TargetNamespaces: kcmv1.TargetNamespaces{StringSelector: "env=prod"},
			}}},
		},
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "selector-structured"},
			Spec: kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{
				TargetNamespaces: kcmv1.TargetNamespaces{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "frontend"}}},
			}}},
		},
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "list-target"},
			Spec: kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{
				TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"project-a"}},
			}}},
		},
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "all-targets"},
			Spec:       kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{}}},
		},
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "selector-no-match"},
			Spec: kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{
				TargetNamespaces: kcmv1.TargetNamespaces{StringSelector: "env=dev"},
			}}},
		},
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "selector-invalid"},
			Spec: kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{
				TargetNamespaces: kcmv1.TargetNamespaces{StringSelector: "env in (prod"},
			}}},
		},
	}

	reconciler := newAccessManagementReconcilerWithIndexes(t, accessManagements...)

	expected := map[types.NamespacedName]bool{
		{Name: "selector-string"}:     true,
		{Name: "selector-structured"}: true,
		{Name: "list-target"}:         true,
		{Name: "all-targets"}:         true,
	}

	requests := reconciler.mapNamespaceToRequests(t.Context(), namespace)
	if len(requests) != len(expected) {
		t.Fatalf("expected %d requests, got %d", len(expected), len(requests))
	}

	for _, req := range requests {
		if !expected[req.NamespacedName] {
			t.Fatalf("unexpected request queued: %s", req.String())
		}
		delete(expected, req.NamespacedName)
	}
	if len(expected) > 0 {
		t.Fatalf("missing queued requests: %v", expected)
	}
}

func Test_mapNamespaceLabelUpdateToRequests(t *testing.T) {
	t.Parallel()

	oldNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "project-a",
			Labels: map[string]string{"env": "dev", "team": "core", "component": "api"},
		},
	}
	newNamespace := oldNamespace.DeepCopy()
	newNamespace.Labels["env"] = "prod"
	newNamespace.Labels["team"] = "platform"
	newNamespace.Labels["noise"] = "changed"

	accessManagements := []client.Object{
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "selector-enter"},
			Spec: kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{
				TargetNamespaces: kcmv1.TargetNamespaces{StringSelector: "env=prod"},
			}}},
		},
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "selector-leave"},
			Spec: kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{
				TargetNamespaces: kcmv1.TargetNamespaces{StringSelector: "team=core"},
			}}},
		},
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "selector-stable"},
			Spec: kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{
				TargetNamespaces: kcmv1.TargetNamespaces{StringSelector: "component=api"},
			}}},
		},
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "selector-still-out"},
			Spec: kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{
				TargetNamespaces: kcmv1.TargetNamespaces{StringSelector: "zone=eu"},
			}}},
		},
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "list-target"},
			Spec: kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{
				TargetNamespaces: kcmv1.TargetNamespaces{List: []string{"project-a"}},
			}}},
		},
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "all-targets"},
			Spec:       kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{}}},
		},
		&kcmv1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{Name: "selector-invalid"},
			Spec: kcmv1.AccessManagementSpec{AccessRules: []kcmv1.AccessRule{{
				TargetNamespaces: kcmv1.TargetNamespaces{StringSelector: "env in (prod"},
			}}},
		},
	}

	reconciler := newAccessManagementReconcilerWithIndexes(t, accessManagements...)

	expected := map[types.NamespacedName]bool{
		{Name: "selector-enter"}: true,
		{Name: "selector-leave"}: true,
	}

	requests := reconciler.mapNamespaceLabelUpdateToRequests(t.Context(), oldNamespace, newNamespace)
	if len(requests) != len(expected) {
		t.Fatalf("expected %d requests, got %d", len(expected), len(requests))
	}

	for _, req := range requests {
		if !expected[req.NamespacedName] {
			t.Fatalf("unexpected request queued: %s", req.String())
		}
		delete(expected, req.NamespacedName)
	}

	if len(expected) > 0 {
		t.Fatalf("missing queued requests: %v", expected)
	}
}

func Test_getEventPredicates(t *testing.T) {
	t.Parallel()

	predicates := (&AccessManagementReconciler{}).getEventPredicates()

	if !predicates.Create(event.TypedCreateEvent[client.Object]{Object: &corev1.Namespace{}}) {
		t.Fatal("expected create event to trigger reconcile")
	}

	if predicates.Delete(event.TypedDeleteEvent[client.Object]{Object: &corev1.Namespace{}}) {
		t.Fatal("expected delete event to not trigger reconcile")
	}

	if predicates.Generic(event.TypedGenericEvent[client.Object]{Object: &corev1.Namespace{}}) {
		t.Fatal("expected generic event to not trigger reconcile")
	}

	oldNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", Labels: map[string]string{"env": "dev"}}}
	newNamespace := oldNamespace.DeepCopy()
	if predicates.Update(event.TypedUpdateEvent[client.Object]{ObjectOld: oldNamespace, ObjectNew: newNamespace}) {
		t.Fatal("expected update event with unchanged labels to not trigger reconcile")
	}

	newNamespace.Labels["env"] = "prod"
	if !predicates.Update(event.TypedUpdateEvent[client.Object]{ObjectOld: oldNamespace, ObjectNew: newNamespace}) {
		t.Fatal("expected update event with changed labels to trigger reconcile")
	}

	if predicates.Update(event.TypedUpdateEvent[client.Object]{}) {
		t.Fatal("expected update event with missing objects to not trigger reconcile")
	}
}

func TestBuiltinKindEventHandler(t *testing.T) {
	t.Parallel()

	newQueue := func() workqueue.TypedRateLimitingInterface[ctrl.Request] {
		return workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[ctrl.Request]())
	}
	wantEnqueued := ctrl.Request{NamespacedName: client.ObjectKey{Name: kcmv1.AccessManagementName}}

	r := &AccessManagementReconciler{SystemNamespace: genericTestSystemNamespace}
	h := r.builtinKindEventHandler()

	t.Run("create in the system namespace enqueues the singleton AccessManagement", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		q := newQueue()

		obj := &kcmv1.Credential{ObjectMeta: metav1.ObjectMeta{Namespace: genericTestSystemNamespace, Name: "c1"}}
		h.Create(t.Context(), event.TypedCreateEvent[client.Object]{Object: obj}, q)

		g.Expect(q.Len()).To(Equal(1))
		item, _ := q.Get()
		g.Expect(item).To(Equal(wantEnqueued))
	})

	t.Run("create outside the system namespace is ignored", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		q := newQueue()

		obj := &kcmv1.Credential{ObjectMeta: metav1.ObjectMeta{Namespace: "other-namespace", Name: "c1"}}
		h.Create(t.Context(), event.TypedCreateEvent[client.Object]{Object: obj}, q)

		g.Expect(q.Len()).To(Equal(0))
	})

	t.Run("update in the system namespace enqueues based on the new object", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		q := newQueue()

		oldObj := &kcmv1.Credential{ObjectMeta: metav1.ObjectMeta{Namespace: genericTestSystemNamespace, Name: "c1", Labels: map[string]string{"a": "b"}}}
		newObj := oldObj.DeepCopy()
		newObj.Labels["a"] = "c"
		h.Update(t.Context(), event.TypedUpdateEvent[client.Object]{ObjectOld: oldObj, ObjectNew: newObj}, q)

		g.Expect(q.Len()).To(Equal(1))
		item, _ := q.Get()
		g.Expect(item).To(Equal(wantEnqueued))
	})

	t.Run("update moving out of the system namespace is ignored", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		q := newQueue()

		oldObj := &kcmv1.Credential{ObjectMeta: metav1.ObjectMeta{Namespace: genericTestSystemNamespace, Name: "c1"}}
		newObj := oldObj.DeepCopy()
		newObj.Namespace = "other-namespace"
		h.Update(t.Context(), event.TypedUpdateEvent[client.Object]{ObjectOld: oldObj, ObjectNew: newObj}, q)

		g.Expect(q.Len()).To(Equal(0))
	})

	t.Run("nil object is ignored", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		q := newQueue()

		h.Create(t.Context(), event.TypedCreateEvent[client.Object]{Object: nil}, q)

		g.Expect(q.Len()).To(Equal(0))
	})
}

func TestBuiltinKindsAreWatched(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	kinds := builtinKinds()
	g.Expect(kinds).To(ConsistOf(
		&kcmv1.ClusterTemplateChain{},
		&kcmv1.ServiceTemplateChain{},
		&kcmv1.Credential{},
		&kcmv1.ClusterAuthentication{},
		&kcmv1.DataSource{},
		&kcmv1.ClusterAuditPolicy{},
	))
}

func newGenericTestRESTMapper() apimeta.RESTMapper {
	mapper := apimeta.NewDefaultRESTMapper([]schema.GroupVersion{widgetGVK.GroupVersion(), kcmv1.GroupVersion})
	mapper.Add(widgetGVK, apimeta.RESTScopeNamespace)
	mapper.Add(clusterWidgetGVK, apimeta.RESTScopeRoot)
	for _, kind := range []string{
		kcmv1.ClusterTemplateChainKind,
		kcmv1.ServiceTemplateChainKind,
		kcmv1.CredentialKind,
		kcmv1.ClusterAuthenticationKind,
		kcmv1.DataSourceKind,
		kcmv1.ClusterAuditPolicyKind,
	} {
		mapper.Add(kcmv1.GroupVersion.WithKind(kind), apimeta.RESTScopeNamespace)
	}
	return mapper
}

func newWidget(namespace, name string, labels map[string]string) *unstructured.Unstructured {
	w := &unstructured.Unstructured{}
	w.SetGroupVersionKind(widgetGVK)
	w.SetNamespace(namespace)
	w.SetName(name)
	if labels != nil {
		w.SetLabels(labels)
	}
	_ = unstructured.SetNestedField(w.Object, "bar", "spec", "foo")
	return w
}

func newFakeDynamicClient(objs ...runtime.Object) dynamic.Interface {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(widgetGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(widgetGVK.GroupVersion().WithKind("WidgetList"), &unstructured.UnstructuredList{})
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		widgetGVR: "WidgetList",
	}, objs...)
}

func newGenericTestReconciler(c client.Client, dyn dynamic.Interface) *AccessManagementReconciler {
	return &AccessManagementReconciler{
		Client:          c,
		SystemNamespace: genericTestSystemNamespace,
		RESTMapper:      newGenericTestRESTMapper(),
		DynamicClient:   dyn,
	}
}

func TestReconcileGenericResourceRuleByNames(t *testing.T) {
	g := NewWithT(t)
	ctx := t.Context()

	sourceWidget := newWidget(genericTestSystemNamespace, "widget-1", nil)
	staleWidget := newWidget(genericTestTargetNamespace, "widget-stale", map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue})

	dyn := newFakeDynamicClient(sourceWidget, staleWidget)

	accessMgmt := am.NewAccessManagement(
		am.WithName(kcmv1.AccessManagementName),
		am.WithLabels(kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM),
		am.WithAccessRules([]kcmv1.AccessRule{
			{
				TargetNamespaces: kcmv1.TargetNamespaces{List: []string{genericTestTargetNamespace}},
				Resources: []kcmv1.ResourceRule{
					{APIVersion: "example.com/v1", Kind: "Widget", Names: []string{"widget-1"}},
				},
			},
		}),
	)

	c := fake.NewClientBuilder().
		WithScheme(testscheme.Scheme).
		WithStatusSubresource(&kcmv1.AccessManagement{}).
		WithObjects(
			management.NewManagement(),
			accessMgmt,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: genericTestSystemNamespace}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: genericTestTargetNamespace}},
		).
		Build()

	r := newGenericTestReconciler(c, dyn)

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(accessMgmt)})
	g.Expect(err).NotTo(HaveOccurred())

	created, err := dyn.Resource(widgetGVR).Namespace(genericTestTargetNamespace).Get(ctx, "widget-1", metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(created.GetLabels()).To(HaveKeyWithValue(kcmv1.KCMManagedLabelKey, kcmv1.KCMManagedLabelValue))
	spec, found, err := unstructured.NestedString(created.Object, "spec", "foo")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(found).To(BeTrue())
	g.Expect(spec).To(Equal("bar"))

	_, err = dyn.Resource(widgetGVR).Namespace(genericTestTargetNamespace).Get(ctx, "widget-stale", metav1.GetOptions{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "stale managed widget should have been cleaned up")

	var updated kcmv1.AccessManagement
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(accessMgmt), &updated)).To(Succeed())
	g.Expect(updated.Status.Error).To(BeEmpty())
	g.Expect(updated.Status.Resources).To(ContainElement(kcmv1.ResourceKindStatus{APIVersion: "example.com/v1", Kind: "Widget"}))

	var clusterRole rbacv1.ClusterRole
	g.Expect(c.Get(ctx, client.ObjectKey{Name: accessMgmt.Name + accessManagementDynamicClusterRoleSuffix}, &clusterRole)).To(Succeed())
	g.Expect(clusterRole.Labels).To(HaveKeyWithValue(aggregateToManagerLabelKey, aggregateToManagerLabelValue))
	g.Expect(clusterRole.Rules).To(ContainElement(rbacv1.PolicyRule{
		APIGroups: []string{"example.com"},
		Resources: []string{"widgets"},
		Verbs:     []string{"get", "list", "watch", "create", "delete"},
	}))
	g.Expect(metav1.IsControlledBy(&clusterRole, &updated)).To(BeTrue(), "the dynamic-RBAC ClusterRole must be owned by the singleton AccessManagement, so Owns() can react promptly to drift")
}

// credentialGVR is the GVR for the built-in Credential Kind, used by the backward-compatibility
// tests below to talk to the fake dynamic client directly.
var credentialGVR = schema.GroupVersionResource{Group: kcmv1.GroupVersion.Group, Version: kcmv1.GroupVersion.Version, Resource: "credentials"}

// TestReconcileOldStyledRuleBackwardCompatibility verifies that an AccessRule populating only
// the deprecated one-field-per-Kind selectors (no Resources at all) is still fully processed by
// the controller: this is the fallback path for when the mutating webhook that normally
// migrates such rules into Resources on write is disabled or otherwise didn't run. Without it,
// previously-distributed objects for an old-styled rule would be frozen in place forever
// (neither refreshed nor cleaned up) the moment the controller stopped reading the deprecated
// fields itself.
func TestReconcileOldStyledRuleBackwardCompatibility(t *testing.T) {
	g := NewWithT(t)
	ctx := t.Context()

	credA := credential.NewCredential(credential.WithName("cred-a"), credential.WithNamespace(genericTestSystemNamespace))
	dyn := dynamicfake.NewSimpleDynamicClient(testscheme.Scheme, credA)

	accessMgmt := am.NewAccessManagement(
		am.WithName(kcmv1.AccessManagementName),
		am.WithLabels(kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM),
		am.WithAccessRules([]kcmv1.AccessRule{
			{
				TargetNamespaces: kcmv1.TargetNamespaces{List: []string{genericTestTargetNamespace}},
				Credentials:      []string{"cred-a"},
			},
		}),
	)

	c := fake.NewClientBuilder().
		WithScheme(testscheme.Scheme).
		WithStatusSubresource(&kcmv1.AccessManagement{}).
		WithObjects(
			management.NewManagement(),
			accessMgmt,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: genericTestSystemNamespace}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: genericTestTargetNamespace}},
		).
		Build()

	r := newGenericTestReconciler(c, dyn)

	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(accessMgmt)}
	_, err := r.Reconcile(ctx, req)
	g.Expect(err).NotTo(HaveOccurred())

	_, err = dyn.Resource(credentialGVR).Namespace(genericTestTargetNamespace).Get(ctx, "cred-a", metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred(), "an old-styled rule must still be distributed when Resources is empty")

	var updated kcmv1.AccessManagement
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(accessMgmt), &updated)).To(Succeed())
	g.Expect(updated.Status.Error).To(BeEmpty())
	g.Expect(updated.Status.Resources).To(ContainElement(kcmv1.ResourceKindStatus{APIVersion: kcmv1.GroupVersion.String(), Kind: kcmv1.CredentialKind}))

	// Reconciling again with the exact same old-styled rule must not delete what it already
	// distributed: EffectiveResources is re-derived fresh from the live deprecated fields on
	// every reconcile, so "cred-a" stays in the keep set indefinitely, not just on first sight.
	_, err = r.Reconcile(ctx, req)
	g.Expect(err).NotTo(HaveOccurred())

	_, err = dyn.Resource(credentialGVR).Namespace(genericTestTargetNamespace).Get(ctx, "cred-a", metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred(), "a steady-state old-styled rule must not have its previously-distributed object deleted")
}

// TestReconcileNewStyledResourcesTakePrecedenceOverOldStyled verifies that when an AccessRule
// populates both Resources and a deprecated field, only Resources is honored: the two are never
// merged, and the deprecated field's names are not distributed.
func TestReconcileNewStyledResourcesTakePrecedenceOverOldStyled(t *testing.T) {
	g := NewWithT(t)
	ctx := t.Context()

	credNew := credential.NewCredential(credential.WithName("cred-new"), credential.WithNamespace(genericTestSystemNamespace))
	credOld := credential.NewCredential(credential.WithName("cred-old"), credential.WithNamespace(genericTestSystemNamespace))
	dyn := dynamicfake.NewSimpleDynamicClient(testscheme.Scheme, credNew, credOld)

	accessMgmt := am.NewAccessManagement(
		am.WithName(kcmv1.AccessManagementName),
		am.WithLabels(kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM),
		am.WithAccessRules([]kcmv1.AccessRule{
			{
				TargetNamespaces: kcmv1.TargetNamespaces{List: []string{genericTestTargetNamespace}},
				Resources:        []kcmv1.ResourceRule{am.NewResourceRule(kcmv1.CredentialKind, "cred-new")},
				Credentials:      []string{"cred-old"},
			},
		}),
	)

	c := fake.NewClientBuilder().
		WithScheme(testscheme.Scheme).
		WithStatusSubresource(&kcmv1.AccessManagement{}).
		WithObjects(
			management.NewManagement(),
			accessMgmt,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: genericTestSystemNamespace}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: genericTestTargetNamespace}},
		).
		Build()

	r := newGenericTestReconciler(c, dyn)

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(accessMgmt)})
	g.Expect(err).NotTo(HaveOccurred())

	_, err = dyn.Resource(credentialGVR).Namespace(genericTestTargetNamespace).Get(ctx, "cred-new", metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred(), "the new-styled Resources entry must be distributed")

	_, err = dyn.Resource(credentialGVR).Namespace(genericTestTargetNamespace).Get(ctx, "cred-old", metav1.GetOptions{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the deprecated field must be ignored once Resources is set on the same rule")
}

func TestReconcileGenericResourceRuleBySelector(t *testing.T) {
	g := NewWithT(t)
	ctx := t.Context()

	matching := newWidget(genericTestSystemNamespace, "widget-prod", map[string]string{"tier": "prod"})
	nonMatching := newWidget(genericTestSystemNamespace, "widget-dev", map[string]string{"tier": "dev"})

	dyn := newFakeDynamicClient(matching, nonMatching)

	accessMgmt := am.NewAccessManagement(
		am.WithName(kcmv1.AccessManagementName),
		am.WithLabels(kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM),
		am.WithAccessRules([]kcmv1.AccessRule{
			{
				TargetNamespaces: kcmv1.TargetNamespaces{List: []string{genericTestTargetNamespace}},
				Resources: []kcmv1.ResourceRule{
					{APIVersion: "example.com/v1", Kind: "Widget", StringSelector: "tier=prod"},
				},
			},
		}),
	)

	c := fake.NewClientBuilder().
		WithScheme(testscheme.Scheme).
		WithStatusSubresource(&kcmv1.AccessManagement{}).
		WithObjects(
			management.NewManagement(),
			accessMgmt,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: genericTestSystemNamespace}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: genericTestTargetNamespace}},
		).
		Build()

	r := newGenericTestReconciler(c, dyn)

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(accessMgmt)})
	g.Expect(err).NotTo(HaveOccurred())

	_, err = dyn.Resource(widgetGVR).Namespace(genericTestTargetNamespace).Get(ctx, "widget-prod", metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())

	_, err = dyn.Resource(widgetGVR).Namespace(genericTestTargetNamespace).Get(ctx, "widget-dev", metav1.GetOptions{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "non-matching widget must not be distributed")
}

func TestReconcileSkipsClusterScopedKindWithWarning(t *testing.T) {
	g := NewWithT(t)
	ctx := t.Context()

	dyn := newFakeDynamicClient()

	accessMgmt := am.NewAccessManagement(
		am.WithName(kcmv1.AccessManagementName),
		am.WithLabels(kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM),
		am.WithAccessRules([]kcmv1.AccessRule{
			{
				TargetNamespaces: kcmv1.TargetNamespaces{List: []string{genericTestTargetNamespace}},
				Resources: []kcmv1.ResourceRule{
					{APIVersion: "example.com/v1", Kind: "ClusterWidget", Names: []string{"cw1"}},
				},
			},
		}),
	)

	c := fake.NewClientBuilder().
		WithScheme(testscheme.Scheme).
		WithStatusSubresource(&kcmv1.AccessManagement{}).
		WithObjects(
			management.NewManagement(),
			accessMgmt,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: genericTestSystemNamespace}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: genericTestTargetNamespace}},
		).
		Build()

	r := newGenericTestReconciler(c, dyn)

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(accessMgmt)})
	g.Expect(err).NotTo(HaveOccurred(), "a cluster-scoped Kind must not fail the reconciliation")

	var updated kcmv1.AccessManagement
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(accessMgmt), &updated)).To(Succeed())
	g.Expect(updated.Status.Error).To(BeEmpty(), "a skipped Kind must not fail the overall reconciliation")
	g.Expect(updated.Status.Resources).To(HaveLen(1))
	g.Expect(updated.Status.Resources[0].APIVersion).To(Equal("example.com/v1"))
	g.Expect(updated.Status.Resources[0].Kind).To(Equal("ClusterWidget"))
	g.Expect(updated.Status.Resources[0].Error).To(ContainSubstring("cluster-scoped"), "a skipped Kind must not be reported as successfully processed")

	var clusterRole rbacv1.ClusterRole
	err = c.Get(ctx, client.ObjectKey{Name: accessMgmt.Name + accessManagementDynamicClusterRoleSuffix}, &clusterRole)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "no RBAC should ever be granted for a cluster-scoped Kind")
}

func TestResolveResourceRuleNames(t *testing.T) {
	system := map[string]*unstructured.Unstructured{
		"a": newWidget(genericTestSystemNamespace, "a", map[string]string{"tier": "prod"}),
		"b": newWidget(genericTestSystemNamespace, "b", map[string]string{"tier": "dev"}),
	}

	tests := []struct {
		name    string
		rule    kcmv1.ResourceRule
		want    []string
		wantErr bool
	}{
		{
			name: "explicit names are returned verbatim, even if not present in system",
			rule: kcmv1.ResourceRule{Names: []string{"a", "missing"}},
			want: []string{"a", "missing"},
		},
		{
			name: "explicitly empty names list selects nothing, and must not fall back to matching everything",
			rule: kcmv1.ResourceRule{Names: []string{}},
			want: []string{},
		},
		{
			name: "string selector matches by label",
			rule: kcmv1.ResourceRule{StringSelector: "tier=prod"},
			want: []string{"a"},
		},
		{
			name: "structured selector matches by label",
			rule: kcmv1.ResourceRule{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "dev"}}},
			want: []string{"b"},
		},
		{
			name: "empty structured selector matches everything",
			rule: kcmv1.ResourceRule{Selector: &metav1.LabelSelector{}},
			want: []string{"a", "b"},
		},
		{
			name:    "invalid string selector errors",
			rule:    kcmv1.ResourceRule{StringSelector: "tier in (prod"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			got, err := resolveResourceRuleNames(tt.rule, system)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got).To(Equal(tt.want))
		})
	}
}

func TestBuildResourceRBACRules(t *testing.T) {
	g := NewWithT(t)

	r := &AccessManagementReconciler{RESTMapper: newGenericTestRESTMapper()}

	rules := r.buildResourceRBACRules([]schema.GroupVersionKind{
		widgetGVK,
		widgetGVK, // duplicate should not produce a duplicate rule
		{Group: "example.com", Version: "v1", Kind: "DoesNotExist"}, // unresolvable: must be skipped
	})

	g.Expect(rules).To(ConsistOf(rbacv1.PolicyRule{
		APIGroups: []string{"example.com"},
		Resources: []string{"widgets"},
		Verbs:     []string{"get", "list", "watch", "create", "delete"},
	}))
}

func TestRewriteNamespaceHelpers(t *testing.T) {
	t.Run("rewriteNamespaceIfSet only rewrites when already non-empty", func(t *testing.T) {
		g := NewWithT(t)

		obj := &unstructured.Unstructured{Object: map[string]any{}}
		rewriteNamespaceIfSet(obj, "target-ns", "spec", "identityRef", "namespace")
		_, found, _ := unstructured.NestedString(obj.Object, "spec", "identityRef", "namespace")
		g.Expect(found).To(BeFalse(), "must not set a namespace field that was never present")

		_ = unstructured.SetNestedField(obj.Object, "system-ns", "spec", "identityRef", "namespace")
		rewriteNamespaceIfSet(obj, "target-ns", "spec", "identityRef", "namespace")
		got, _, _ := unstructured.NestedString(obj.Object, "spec", "identityRef", "namespace")
		g.Expect(got).To(Equal("target-ns"))
	})

	t.Run("rewriteNamespaceIfEmpty only rewrites when the parent exists and namespace is unset", func(t *testing.T) {
		g := NewWithT(t)

		obj := &unstructured.Unstructured{Object: map[string]any{}}
		rewriteNamespaceIfEmpty(obj, "system-ns", "spec", "caSecret", "namespace")
		_, found, _ := unstructured.NestedString(obj.Object, "spec", "caSecret", "namespace")
		g.Expect(found).To(BeFalse(), "must not create a caSecret block that never existed")

		_ = unstructured.SetNestedMap(obj.Object, map[string]any{"name": "ca"}, "spec", "caSecret")
		rewriteNamespaceIfEmpty(obj, "system-ns", "spec", "caSecret", "namespace")
		got, _, _ := unstructured.NestedString(obj.Object, "spec", "caSecret", "namespace")
		g.Expect(got).To(Equal("system-ns"))

		// an explicitly-set namespace must not be overridden
		_ = unstructured.SetNestedField(obj.Object, "explicit-ns", "spec", "caSecret", "namespace")
		rewriteNamespaceIfEmpty(obj, "system-ns", "spec", "caSecret", "namespace")
		got, _, _ = unstructured.NestedString(obj.Object, "spec", "caSecret", "namespace")
		g.Expect(got).To(Equal("explicit-ns"))
	})
}

func newAccessManagementReconcilerWithIndexes(t *testing.T, objs ...client.Object) *AccessManagementReconciler {
	t.Helper()

	return &AccessManagementReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(testscheme.Scheme).
			WithIndex(&kcmv1.AccessManagement{}, kcmv1.AccessManagementTargetNamespaceListIndexKey, kcmv1.ExtractAccessManagementTargetNamespaceLists).
			WithIndex(&kcmv1.AccessManagement{}, kcmv1.AccessManagementUsesSelectorIndexKey, kcmv1.ExtractAccessManagementUsesSelector).
			WithIndex(&kcmv1.AccessManagement{}, kcmv1.AccessManagementTargetsAllNamespacesIndexKey, kcmv1.ExtractAccessManagementTargetsAllNamespaces).
			WithObjects(objs...).
			Build(),
	}
}
