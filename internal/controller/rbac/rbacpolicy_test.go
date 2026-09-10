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

package rbac

import (
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func TestToSubjects(t *testing.T) {
	g := NewWithT(t)

	subjects := []kcmv1.RBACPolicySubject{
		{Kind: rbacv1.UserKind, Name: "k0rdent:user:a36bda60-922a-49de-9742-da0db13c3a6b"},
		{Kind: rbacv1.GroupKind, Name: "k0rdent:project:project-2:compute-admin"},
	}

	g.Expect(toSubjects(subjects)).To(Equal([]rbacv1.Subject{
		{Kind: rbacv1.UserKind, Name: "k0rdent:user:a36bda60-922a-49de-9742-da0db13c3a6b", APIGroup: rbacv1.GroupName},
		{Kind: rbacv1.GroupKind, Name: "k0rdent:project:project-2:compute-admin", APIGroup: rbacv1.GroupName},
	}))
}

func TestSync(t *testing.T) {
	g := NewWithT(t)

	policy := &kcmv1.RBACPolicy{
		Spec: kcmv1.RBACPolicySpec{
			Bindings: []kcmv1.RBACPolicyBinding{
				{
					Name:        "compute-admin",
					ClusterRole: "custom-admin",
					Rules: []rbacv1.PolicyRule{
						{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
					},
					Subjects: []kcmv1.RBACPolicySubject{
						{Kind: rbacv1.GroupKind, Name: "k0rdent:project:project-2:compute-admin"},
					},
				},
				{
					Name:        "compute-viewer",
					ClusterRole: "view", // built-in, no Rules
					Subjects: []kcmv1.RBACPolicySubject{
						{Kind: rbacv1.UserKind, Name: "k0rdent:user:a36bda60-922a-49de-9742-da0db13c3a6b"},
					},
				},
			},
		},
	}

	childCl := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

	desiredRoles, desiredBindings, changed, err := Sync(t.Context(), childCl, policy)
	g.Expect(err).To(Succeed())
	g.Expect(changed).To(BeTrue())
	// every referenced ClusterRole is desired, with or without Rules: a binding must keep the
	// role it points at alive even after its rules are dropped
	g.Expect(desiredRoles).To(HaveKey("custom-admin"))
	g.Expect(desiredRoles).To(HaveKey("view"))
	g.Expect(desiredRoles).To(HaveLen(2))
	g.Expect(desiredBindings).To(HaveKey("k0rdent-compute-admin"))
	g.Expect(desiredBindings).To(HaveKey("k0rdent-compute-viewer"))
	g.Expect(desiredBindings).To(HaveLen(2))

	role := &rbacv1.ClusterRole{}
	g.Expect(childCl.Get(t.Context(), client.ObjectKey{Name: "custom-admin"}, role)).To(Succeed())
	g.Expect(role.Rules).To(HaveLen(1))
	g.Expect(role.Labels).To(HaveKeyWithValue(kcmv1.KCMManagedLabelKey, kcmv1.KCMManagedLabelValue))
	g.Expect(role.Labels).To(HaveKeyWithValue(ManagedByLabelKey, ManagedByLabelValue))

	binding := &rbacv1.ClusterRoleBinding{}
	g.Expect(childCl.Get(t.Context(), client.ObjectKey{Name: "k0rdent-compute-admin"}, binding)).To(Succeed())
	g.Expect(binding.RoleRef.Name).To(Equal("custom-admin"))
	g.Expect(binding.Subjects).To(ContainElement(rbacv1.Subject{
		Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: "k0rdent:project:project-2:compute-admin",
	}))

	// re-syncing the identical policy reports no change
	_, _, changed, err = Sync(t.Context(), childCl, policy)
	g.Expect(err).To(Succeed())
	g.Expect(changed).To(BeFalse())

	// change roleRef and confirm the ClusterRoleBinding is recreated (roleRef is immutable)
	policy.Spec.Bindings[0].ClusterRole = "other-admin"
	policy.Spec.Bindings[0].Rules = nil
	_, _, changed, err = Sync(t.Context(), childCl, policy)
	g.Expect(err).To(Succeed())
	g.Expect(changed).To(BeTrue())

	updated := &rbacv1.ClusterRoleBinding{}
	g.Expect(childCl.Get(t.Context(), client.ObjectKey{Name: "k0rdent-compute-admin"}, updated)).To(Succeed())
	g.Expect(updated.RoleRef.Name).To(Equal("other-admin"))
}

// TestSync_RoleRefChangeWhileOldBindingStillTerminating verifies that when the old
// ClusterRoleBinding is still present (e.g. blocked by a finalizer) after Delete is requested,
// applyClusterRoleBinding fails cleanly with AlreadyExists from Create rather than attempting an
// Update against the immutable roleRef field.
func TestSync_RoleRefChangeWhileOldBindingStillTerminating(t *testing.T) {
	g := NewWithT(t)

	stillTerminating := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "k0rdent-compute-admin",
			Finalizers: []string{"test.k0rdent.mirantis.com/block-deletion"},
			Labels:     map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue, ManagedByLabelKey: ManagedByLabelValue},
		},
		RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "old-role"},
	}
	childCl := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(stillTerminating).Build()

	policy := &kcmv1.RBACPolicy{
		Spec: kcmv1.RBACPolicySpec{
			Bindings: []kcmv1.RBACPolicyBinding{
				{
					Name:        "compute-admin",
					ClusterRole: "new-role",
					Subjects: []kcmv1.RBACPolicySubject{
						{Kind: rbacv1.UserKind, Name: "k0rdent:user:abc"},
					},
				},
			},
		},
	}

	desiredRoles, desiredBindings, changed, err := Sync(t.Context(), childCl, policy)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("creating ClusterRoleBinding after deleting the outdated one"))
	g.Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
	g.Expect(Retriable(err)).To(BeTrue())
	// a failed apply keeps its objects desired, so a retry never prunes what is still wanted
	g.Expect(desiredRoles).To(HaveKey("new-role"))
	g.Expect(desiredBindings).To(HaveKey("k0rdent-compute-admin"))
	g.Expect(changed).To(BeFalse())

	// the still-terminating object is untouched: Delete was requested (deletionTimestamp gets
	// set by the fake client because of the finalizer) but the roleRef was never updated in place
	unchanged := &rbacv1.ClusterRoleBinding{}
	g.Expect(childCl.Get(t.Context(), client.ObjectKey{Name: "k0rdent-compute-admin"}, unchanged)).To(Succeed())
	g.Expect(unchanged.RoleRef.Name).To(Equal("old-role"))
	g.Expect(unchanged.DeletionTimestamp).NotTo(BeNil())
}

// TestSync_RefusesToOverwriteUnmanagedClusterRole verifies that a binding can never clobber a
// pre-existing ClusterRole (e.g. a built-in "admin"/"edit"/"view") this package didn't create.
func TestSync_RefusesToOverwriteUnmanagedClusterRole(t *testing.T) {
	g := NewWithT(t)

	builtinAdmin := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "admin"},
		Rules:      []rbacv1.PolicyRule{{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"*"}}},
	}
	childCl := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(builtinAdmin).Build()

	policy := &kcmv1.RBACPolicy{
		Spec: kcmv1.RBACPolicySpec{
			Bindings: []kcmv1.RBACPolicyBinding{
				{
					Name:        "compute-admin",
					ClusterRole: "admin",
					Rules: []rbacv1.PolicyRule{
						{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
					},
					Subjects: []kcmv1.RBACPolicySubject{
						{Kind: rbacv1.UserKind, Name: "k0rdent:user:abc"},
					},
				},
			},
		},
	}

	desiredRoles, desiredBindings, changed, err := Sync(t.Context(), childCl, policy)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("admin"))
	g.Expect(err.Error()).To(ContainSubstring("refusing to overwrite"))
	g.Expect(errors.Is(err, ErrTerminal)).To(BeTrue())
	g.Expect(Retriable(err)).To(BeFalse())
	g.Expect(desiredRoles).To(HaveKey("admin"))
	g.Expect(desiredBindings).To(HaveKey("k0rdent-compute-admin"))
	g.Expect(changed).To(BeFalse())

	// the built-in role's rules must be untouched
	unchanged := &rbacv1.ClusterRole{}
	g.Expect(childCl.Get(t.Context(), client.ObjectKey{Name: "admin"}, unchanged)).To(Succeed())
	g.Expect(unchanged.Rules).To(Equal(builtinAdmin.Rules))
}

// TestSync_KeepsManagedRoleAfterRulesDropped verifies that dropping the rules from a binding
// leaves the managed ClusterRole in place, so the live ClusterRoleBinding keeps resolving.
func TestSync_KeepsManagedRoleAfterRulesDropped(t *testing.T) {
	g := NewWithT(t)

	childCl := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()
	policy := &kcmv1.RBACPolicy{
		Spec: kcmv1.RBACPolicySpec{
			Bindings: []kcmv1.RBACPolicyBinding{
				{
					Name:        "compute-admin",
					ClusterRole: "custom-admin",
					Rules: []rbacv1.PolicyRule{
						{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
					},
					Subjects: []kcmv1.RBACPolicySubject{{Kind: rbacv1.UserKind, Name: "k0rdent:user:abc"}},
				},
			},
		},
	}

	desiredRoles, desiredBindings, _, err := Sync(t.Context(), childCl, policy)
	g.Expect(err).To(Succeed())
	_, err = Prune(t.Context(), childCl, desiredRoles, desiredBindings)
	g.Expect(err).To(Succeed())

	policy.Spec.Bindings[0].Rules = nil
	desiredRoles, desiredBindings, _, err = Sync(t.Context(), childCl, policy)
	g.Expect(err).To(Succeed())
	_, err = Prune(t.Context(), childCl, desiredRoles, desiredBindings)
	g.Expect(err).To(Succeed())

	g.Expect(childCl.Get(t.Context(), client.ObjectKey{Name: "custom-admin"}, &rbacv1.ClusterRole{})).To(Succeed())
	g.Expect(childCl.Get(t.Context(), client.ObjectKey{Name: "k0rdent-compute-admin"}, &rbacv1.ClusterRoleBinding{})).To(Succeed())
}

// TestSync_OneBadBindingDoesNotBlockTheRest verifies that a binding that cannot be applied is
// reported without stopping the others.
func TestSync_OneBadBindingDoesNotBlockTheRest(t *testing.T) {
	g := NewWithT(t)

	builtinAdmin := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "admin"}}
	childCl := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(builtinAdmin).Build()

	policy := &kcmv1.RBACPolicy{
		Spec: kcmv1.RBACPolicySpec{
			Bindings: []kcmv1.RBACPolicyBinding{
				{
					Name:        "broken",
					ClusterRole: "admin", // pre-existing and unmanaged, so Rules can never be applied
					Rules: []rbacv1.PolicyRule{
						{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
					},
					Subjects: []kcmv1.RBACPolicySubject{{Kind: rbacv1.UserKind, Name: "k0rdent:user:abc"}},
				},
				{
					Name:        "healthy",
					ClusterRole: "view",
					Subjects:    []kcmv1.RBACPolicySubject{{Kind: rbacv1.UserKind, Name: "k0rdent:user:def"}},
				},
			},
		},
	}

	_, _, changed, err := Sync(t.Context(), childCl, policy)
	g.Expect(err).To(HaveOccurred())
	g.Expect(Retriable(err)).To(BeFalse())
	g.Expect(changed).To(BeTrue())
	g.Expect(childCl.Get(t.Context(), client.ObjectKey{Name: "k0rdent-healthy"}, &rbacv1.ClusterRoleBinding{})).To(Succeed())
}

// TestRetriable covers the forms callers actually produce, including a joined error rewrapped
// with fmt.Errorf: a mixed batch must stay retriable so the transient half is retried.
func TestRetriable(t *testing.T) {
	g := NewWithT(t)

	terminal := fmt.Errorf("%w: bad binding", ErrTerminal)
	transient := errors.New("apiserver unavailable")
	wrap := func(err error) error { return fmt.Errorf("failed to sync RBAC objects: %w", err) }

	g.Expect(Retriable(nil)).To(BeFalse())
	g.Expect(Retriable(terminal)).To(BeFalse())
	g.Expect(Retriable(transient)).To(BeTrue())
	g.Expect(Retriable(errors.Join(terminal, transient))).To(BeTrue())
	g.Expect(Retriable(wrap(errors.Join(terminal, transient)))).To(BeTrue())
	g.Expect(Retriable(wrap(errors.Join(terminal, terminal)))).To(BeFalse())
	g.Expect(Retriable(wrap(errors.Join(transient, transient)))).To(BeTrue())
	g.Expect(Retriable(wrap(terminal))).To(BeFalse())
	g.Expect(Retriable(wrap(transient))).To(BeTrue())
}

func managedLabels() map[string]string {
	return map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue, ManagedByLabelKey: ManagedByLabelValue}
}

func TestPrune(t *testing.T) {
	g := NewWithT(t)

	managedRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "managed-stale", Labels: managedLabels()},
	}
	managedKeptRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "managed-kept", Labels: managedLabels()},
	}
	unmanagedRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "unmanaged"},
	}
	managedBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "k0rdent-stale", Labels: managedLabels()},
	}

	childCl := fake.NewClientBuilder().
		WithScheme(testscheme.Scheme).
		WithObjects(managedRole, managedKeptRole, unmanagedRole, managedBinding).
		Build()

	changed, err := Prune(t.Context(), childCl, map[string]struct{}{"managed-kept": {}}, nil)
	g.Expect(err).To(Succeed())
	g.Expect(changed).To(BeTrue())

	g.Expect(childCl.Get(t.Context(), client.ObjectKey{Name: "managed-kept"}, &rbacv1.ClusterRole{})).To(Succeed())
	g.Expect(childCl.Get(t.Context(), client.ObjectKey{Name: "unmanaged"}, &rbacv1.ClusterRole{})).To(Succeed())

	err = childCl.Get(t.Context(), client.ObjectKey{Name: "managed-stale"}, &rbacv1.ClusterRole{})
	g.Expect(client.IgnoreNotFound(err)).To(Succeed())
	g.Expect(err).To(HaveOccurred())

	err = childCl.Get(t.Context(), client.ObjectKey{Name: "k0rdent-stale"}, &rbacv1.ClusterRoleBinding{})
	g.Expect(client.IgnoreNotFound(err)).To(Succeed())
	g.Expect(err).To(HaveOccurred())

	// nothing left to prune: reports no change
	changed, err = Prune(t.Context(), childCl, map[string]struct{}{"managed-kept": {}}, nil)
	g.Expect(err).To(Succeed())
	g.Expect(changed).To(BeFalse())
}

// TestPruneBindings verifies that the bindings half of Prune's work only ever touches
// ClusterRoleBindings.
func TestPruneBindings(t *testing.T) {
	g := NewWithT(t)

	managedRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "managed-role", Labels: managedLabels()},
	}
	managedBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "k0rdent-stale", Labels: managedLabels()},
	}

	childCl := fake.NewClientBuilder().
		WithScheme(testscheme.Scheme).
		WithObjects(managedRole, managedBinding).
		Build()

	changed, err := pruneManaged(t.Context(), childCl, rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBinding"), nil)
	g.Expect(err).To(Succeed())
	g.Expect(changed).To(BeTrue())

	// the ClusterRole survives a full binding revocation
	g.Expect(childCl.Get(t.Context(), client.ObjectKey{Name: "managed-role"}, &rbacv1.ClusterRole{})).To(Succeed())

	err = childCl.Get(t.Context(), client.ObjectKey{Name: "k0rdent-stale"}, &rbacv1.ClusterRoleBinding{})
	g.Expect(client.IgnoreNotFound(err)).To(Succeed())
	g.Expect(err).To(HaveOccurred())
}
