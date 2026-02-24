// Copyright 2026
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
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const systemNamespace = "kcm-system"

type credTestCase struct {
	credName                  types.NamespacedName
	credLabels                map[string]string
	region                    string
	createClusterIdentityFunc func() (crclient.Object, error)
	validateCredentialFunc    func(*kcmv1.Credential)
	expectedErr               string
}

var _ = Describe("Credential Controller", Ordered, func() {
	const (
		testNamespace1 = "test-namespace-1"
		testNamespace2 = "test-namespace-2"

		identityRefName = "test-secret"
	)

	var (
		reconciler *CredentialReconciler

		defaultIdentityRef = corev1.ObjectReference{
			APIVersion: corev1.SchemeGroupVersion.Version,
			Kind:       "Secret",
			Namespace:  testNamespace1,
			Name:       identityRefName,
		}

		defaultIdentityData = map[string][]byte{
			"key": []byte("value"),
		}

		providerInterface = &kcmv1.ProviderInterface{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-provider-interface",
			},
			Spec: kcmv1.ProviderInterfaceSpec{
				ClusterIdentities: []kcmv1.ClusterIdentity{
					{
						GroupVersionKind: kcmv1.GroupVersionKind{
							Group:   corev1.GroupName,
							Version: corev1.SchemeGroupVersion.Version,
							Kind:    "Secret",
						},
					},
				},
			},
		}

		createdClusterIdentities []crclient.Object
	)

	BeforeAll(func() {
		Expect(crclient.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: systemNamespace},
		}))).To(Succeed())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: testNamespace1},
		})).To(Succeed())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: testNamespace2},
		})).To(Succeed())

		Expect(k8sClient.Create(ctx, providerInterface)).To(Succeed())

		reconciler = newTestReconciler()
	})

	AfterAll(func() {
		// cleanup all credentials and cluster identities after tests
		for _, ns := range []string{testNamespace1, testNamespace2, systemNamespace} {
			creds := &kcmv1.CredentialList{}
			Expect(k8sClient.List(ctx, creds, crclient.InNamespace(ns))).To(Succeed())
			for _, cred := range creds.Items {
				Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, &cred))).To(Succeed())
			}
		}

		for _, identity := range createdClusterIdentities {
			Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, identity))).To(Succeed())
		}

		Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, providerInterface))).To(Succeed())

		for _, ns := range []string{testNamespace1, testNamespace2} {
			Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}))).To(Succeed())
		}
	})

	DescribeTable("Credential Reconciliation",
		func(tc credTestCase) {
			var identity crclient.Object
			identityRef := defaultIdentityRef
			if tc.createClusterIdentityFunc != nil {
				var err error
				identity, err = tc.createClusterIdentityFunc()
				Expect(err).NotTo(HaveOccurred())
				createdClusterIdentities = append(createdClusterIdentities, identity)
				identityRef = corev1.ObjectReference{
					APIVersion: corev1.SchemeGroupVersion.Version,
					Kind:       "Secret",
					Namespace:  identity.GetNamespace(),
					Name:       identity.GetName(),
				}
			}

			initializeCredential(tc, identityRef)
			testCredentialReconciliation(reconciler, tc)

			deleteCredential(tc)
			testCredentialCleanup(reconciler, tc)
		},
		Entry("Invalid Credential: no cluster identity exists", credTestCase{
			credName:    types.NamespacedName{Namespace: testNamespace1, Name: "cred1"},
			expectedErr: fmt.Sprintf("ClusterIdentity object of Kind=Secret %s/%s not found", testNamespace1, identityRefName),
			validateCredentialFunc: func(cred *kcmv1.Credential) {
				validateCredentialIsNotReady(cred.Status.Conditions, fmt.Sprintf("ClusterIdentity object of Kind=Secret %s/%s not found", testNamespace1, identityRefName))
			},
		}),
		Entry("Valid Credential: Cluster identity exists", credTestCase{
			credName: types.NamespacedName{Namespace: testNamespace1, Name: "cred3"},
			createClusterIdentityFunc: func() (crclient.Object, error) {
				identity := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testNamespace1,
						Name:      identityRefName,
					},
					Data: defaultIdentityData,
				}
				return identity, k8sClient.Create(ctx, identity)
			},
			validateCredentialFunc: func(cred *kcmv1.Credential) { validateCredentialIsReady(cred.Status.Conditions) },
		}),
		Entry("Credential managed by Access Management: should distribute all cluster identities", credTestCase{
			credName:   types.NamespacedName{Namespace: testNamespace2, Name: "cred4"},
			credLabels: map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue},
			createClusterIdentityFunc: func() (crclient.Object, error) {
				identity := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: systemNamespace,
						Name:      identityRefName,
					},
					Data: defaultIdentityData,
				}
				return identity, k8sClient.Create(ctx, identity)
			},
			validateCredentialFunc: func(cred *kcmv1.Credential) {
				validateManagedClusterIdentityExists(cred, testNamespace2, identityRefName, defaultIdentityData)
				validateCredentialIsReady(cred.Status.Conditions)
			},
		}),
		Entry("Invalid Credential: region does not exist", credTestCase{
			credName: types.NamespacedName{Namespace: testNamespace1, Name: "cred5"},
			region:   "rgn1",
			validateCredentialFunc: func(cred *kcmv1.Credential) {
				validateCredentialIsNotReady(cred.Status.Conditions, "regions.k0rdent.mirantis.com \"rgn1\" not found")
			},
		}),
		// TODO: add more tests for regional credential scenarios
	)
})

func newTestReconciler() *CredentialReconciler {
	return &CredentialReconciler{
		MgmtClient:      k8sClient,
		SystemNamespace: systemNamespace,
	}
}

func initializeCredential(tc credTestCase, identityRef corev1.ObjectReference) {
	cred := &kcmv1.Credential{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tc.credName.Name,
			Namespace: tc.credName.Namespace,
			Labels:    tc.credLabels,
		},
		Spec: kcmv1.CredentialSpec{
			IdentityRef: &identityRef,
			Region:      tc.region,
		},
	}
	Expect(k8sClient.Create(ctx, cred)).To(Succeed())
}

func deleteCredential(tc credTestCase) {
	cred := &kcmv1.Credential{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tc.credName.Name,
			Namespace: tc.credName.Namespace,
		},
	}
	Expect(k8sClient.Delete(ctx, cred)).To(Succeed())
}

func testCredentialReconciliation(reconciler *CredentialReconciler, t credTestCase) {
	By("Reconciling the Credential: should add finalizer")
	_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.credName})
	Expect(err).NotTo(HaveOccurred())

	cred := &kcmv1.Credential{}
	err = k8sClient.Get(ctx, t.credName, cred)
	Expect(err).NotTo(HaveOccurred())
	Expect(cred.Finalizers).To(ContainElement(kcmv1.CredentialFinalizer))

	By("Reconciling the Credential: should add KCM component label")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.credName})
	Expect(err).NotTo(HaveOccurred())

	err = k8sClient.Get(ctx, t.credName, cred)
	Expect(err).NotTo(HaveOccurred())
	Expect(cred.Labels[kcmv1.GenericComponentNameLabel]).To(Equal(kcmv1.GenericComponentLabelValueKCM))

	By("Reconciling the Credential: initial setup")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.credName})
	if t.expectedErr != "" {
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(Equal(t.expectedErr))
	} else {
		Expect(err).NotTo(HaveOccurred())
	}

	err = k8sClient.Get(ctx, t.credName, cred)
	Expect(err).NotTo(HaveOccurred())

	if t.validateCredentialFunc != nil {
		t.validateCredentialFunc(cred)
	}
}

func testCredentialCleanup(reconciler *CredentialReconciler, t credTestCase) {
	By("Deleting the Credential: should remove finalizer")
	_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.credName})
	Expect(err).NotTo(HaveOccurred())

	cred := &kcmv1.Credential{}
	err = k8sClient.Get(ctx, t.credName, cred)
	Expect(err).To(HaveOccurred())
	Expect(apierrors.IsNotFound(err)).To(BeTrue())
}

func validateStatusConditionExistsAndEqual(conditions []metav1.Condition, conditionType string, status metav1.ConditionStatus, reason, message string) {
	cond := apimeta.FindStatusCondition(conditions, conditionType)
	Expect(cond).NotTo(BeNil(), "expected condition %s to exist", conditionType)
	if cond == nil {
		return
	}
	Expect(cond.Status).To(Equal(status))
	Expect(cond.Reason).To(Equal(reason))
	Expect(cond.Message).To(Equal(message))
}

func validateCredentialIsReady(conditions []metav1.Condition) {
	validateStatusConditionExistsAndEqual(conditions,
		kcmv1.CredentialReadyCondition,
		metav1.ConditionTrue,
		kcmv1.SucceededReason,
		"Credential is ready")
}

func validateCredentialIsNotReady(conditions []metav1.Condition, expectedMessage string) {
	validateStatusConditionExistsAndEqual(conditions,
		kcmv1.CredentialReadyCondition,
		metav1.ConditionFalse,
		kcmv1.FailedReason,
		expectedMessage)
}

func validateManagedClusterIdentityExists(cred *kcmv1.Credential, namespace, name string, data map[string][]byte) {
	ci := &corev1.Secret{}
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, ci)
	Expect(err).NotTo(HaveOccurred())
	Expect(ci.Data).To(Equal(data))
	Expect(ci.Labels).To(Equal(map[string]string{
		kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
		strings.Join([]string{kcmv1.CredentialLabelKeyPrefix, cred.Namespace, cred.Name}, "."): "true",
	}))
}
