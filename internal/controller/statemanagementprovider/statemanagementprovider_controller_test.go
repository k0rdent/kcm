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

package statemanagementprovider

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	discoveryfake "k8s.io/client-go/discovery/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	pointerutil "github.com/K0rdent/kcm/internal/util/pointer"
)

const (
	systemNamespace = "kcm-system"

	stateManagementProviderName = "test"
	incorrectServiceAccountName = "incorrect"
)

func TestReconciler_evaluateReadiness(t *testing.T) {
	t.Parallel()

	type testCase struct {
		object         *unstructured.Unstructured
		rule           string
		expectedResult bool
		expectError    bool
	}

	f := func(t *testing.T, tc testCase) {
		t.Helper()
		res, err := evaluateReadiness(tc.object, tc.rule)
		if tc.expectError {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		require.Equal(t, tc.expectedResult, res)
	}

	cases := map[string]testCase{
		"deployment-ready-succeed": {
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"status": map[string]any{
						"availableReplicas": 1,
						"replicas":          1,
						"updatedReplicas":   1,
						"readyReplicas":     1,
					},
				},
			},
			rule: `self.status.availableReplicas == self.status.replicas && 
self.status.availableReplicas == self.status.updatedReplicas && 
self.status.availableReplicas == self.status.readyReplicas`,
			expectedResult: true,
		},
		"deployment-evaluation-failed": {
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"status": map[string]any{
						"availableReplicas": 1,
						"replicas":          2,
						"updatedReplicas":   1,
						"readyReplicas":     1,
					},
				},
			},
			rule: `self.status.availableReplicas == self.status.replicas && 
self.status.availableReplicas == self.status.updatedReplicas && 
self.status.availableReplicas == self.status.readyReplicas`,
			expectedResult: false,
		},
		"invalid-rule": {
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"status": map[string]any{
						"availableReplicas": 1,
						"replicas":          2,
						"updatedReplicas":   1,
						"readyReplicas":     1,
					},
				},
			},
			rule: `invalid: status.availableReplicas == self.status.replicas && 
self.status.availableReplicas == updatedReplicas && 
self.status.availableReplicas == self.status.readyReplicas`,
			expectedResult: false,
			expectError:    true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f(t, tc)
		})
	}
}

func TestReconciler_buildRBACRules(t *testing.T) {
	t.Parallel()

	type testCase struct {
		gvrList       []schema.GroupVersionResource
		expectedRules []rbacv1.PolicyRule
	}

	f := func(t *testing.T, tc testCase) {
		t.Helper()
		rules := buildRBACRules(tc.gvrList)
		require.Equal(t, tc.expectedRules, rules)
	}

	cases := map[string]testCase{
		"case-1": {
			gvrList: []schema.GroupVersionResource{
				{
					Group:    "apps",
					Version:  "v1",
					Resource: "deployments",
				},
				{
					Group:    "apps",
					Version:  "v1",
					Resource: "statefulsets",
				},
			},
			expectedRules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get", "list", "watch"},
					APIGroups: []string{"apps"},
					Resources: []string{"deployments", "statefulsets"},
				},
				{
					APIGroups: []string{apiExtensionsGroup},
					Resources: []string{apiExtensionsResource},
					Verbs:     []string{"get", "list", "watch"},
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f(t, tc)
		})
	}
}

func TestReconciler_ensureRBAC(t *testing.T) {
	t.Parallel()

	type testCase struct {
		stateManagementProvider    *kcmv1.StateManagementProvider
		discoveryResources         []*metav1.APIResourceList
		existingRBACObjects        []client.Object
		expectedServiceAccount     *corev1.ServiceAccount
		expectedClusterRole        *rbacv1.ClusterRole
		expectedClusterRoleBinding *rbacv1.ClusterRoleBinding
		expectError                bool
	}

	f := func(t *testing.T, tc testCase) {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// StateManagementProvider provider object is required
		if tc.stateManagementProvider == nil {
			t.Fatal("stateManagementProvider is nil")
		}

		kubeClient := fakeKubeClient(t, tc.stateManagementProvider, tc.existingRBACObjects...)
		reconciler := Reconciler{
			Client:   kubeClient,
			timeFunc: func() time.Time { return time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC) },
			discoveryClientFunc: func(_ *rest.Config) (discovery.DiscoveryInterface, error) {
				return fakeDiscoveryClient(t, tc.discoveryResources), nil
			},
			SystemNamespace: systemNamespace,
		}

		err := reconciler.ensureRBAC(ctx, tc.stateManagementProvider)
		if tc.expectError {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)

		sa := new(corev1.ServiceAccount)
		key := client.ObjectKey{Name: tc.stateManagementProvider.Name + serviceAccountSuffix, Namespace: systemNamespace}
		require.NoError(t, kubeClient.Get(ctx, key, sa))
		assertServiceAccount(t, tc.expectedServiceAccount, sa)

		cr := new(rbacv1.ClusterRole)
		key = client.ObjectKey{Name: tc.stateManagementProvider.Name + clusterRoleSuffix}
		require.NoError(t, kubeClient.Get(ctx, key, cr))
		assertClusterRole(t, tc.expectedClusterRole, cr)

		crb := new(rbacv1.ClusterRoleBinding)
		key = client.ObjectKey{Name: tc.stateManagementProvider.Name + clusterRoleBindingSuffix}
		require.NoError(t, kubeClient.Get(ctx, key, crb))
		assertClusterRoleBinding(t, tc.expectedClusterRoleBinding, crb)
	}

	cases := map[string]testCase{
		"create-rbac-objects": {
			stateManagementProvider: &kcmv1.StateManagementProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      stateManagementProviderName,
					Namespace: systemNamespace,
				},
				Spec: kcmv1.StateManagementProviderSpec{
					Adapter: kcmv1.ResourceReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test",
						Namespace:  metav1.NamespaceDefault,
					},
				},
			},
			discoveryResources: []*metav1.APIResourceList{
				{
					GroupVersion: "apps/v1",
					APIResources: []metav1.APIResource{
						{
							Name:       "deployments",
							Kind:       "Deployment",
							Namespaced: true,
						},
					},
				},
			},
			expectedServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      stateManagementProviderName + serviceAccountSuffix,
					Namespace: systemNamespace,
				},
			},
			expectedClusterRole: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: stateManagementProviderName + clusterRoleSuffix,
				},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{apiExtensionsGroup},
						Resources: []string{apiExtensionsResource},
						Verbs:     []string{"get", "list", "watch"},
					},
					{
						APIGroups: []string{"apps"},
						Verbs:     []string{"get", "list", "watch"},
						Resources: []string{"deployments"},
					},
				},
			},
			expectedClusterRoleBinding: &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: stateManagementProviderName + clusterRoleBindingSuffix,
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     stateManagementProviderName + clusterRoleSuffix,
				},
				Subjects: []rbacv1.Subject{
					{
						Kind:      "ServiceAccount",
						Name:      stateManagementProviderName + serviceAccountSuffix,
						Namespace: systemNamespace,
					},
				},
			},
		},
		"update-service-account": {
			stateManagementProvider: &kcmv1.StateManagementProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      stateManagementProviderName,
					Namespace: systemNamespace,
				},
				Spec: kcmv1.StateManagementProviderSpec{
					Adapter: kcmv1.ResourceReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test",
						Namespace:  metav1.NamespaceDefault,
					},
				},
			},
			discoveryResources: []*metav1.APIResourceList{
				{
					GroupVersion: "apps/v1",
					APIResources: []metav1.APIResource{
						{
							Name:       "deployments",
							Kind:       "Deployment",
							Namespaced: true,
						},
					},
				},
			},
			existingRBACObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      stateManagementProviderName + serviceAccountSuffix,
						Namespace: systemNamespace,
					},
					AutomountServiceAccountToken: pointerutil.To(true),
				},
			},
			expectedServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      stateManagementProviderName + serviceAccountSuffix,
					Namespace: systemNamespace,
				},
			},
			expectedClusterRole: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: stateManagementProviderName + clusterRoleSuffix,
				},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{apiExtensionsGroup},
						Resources: []string{apiExtensionsResource},
						Verbs:     []string{"get", "list", "watch"},
					},
					{
						APIGroups: []string{"apps"},
						Verbs:     []string{"get", "list", "watch"},
						Resources: []string{"deployments"},
					},
				},
			},
			expectedClusterRoleBinding: &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: stateManagementProviderName + clusterRoleBindingSuffix,
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     stateManagementProviderName + clusterRoleSuffix,
				},
				Subjects: []rbacv1.Subject{
					{
						Kind:      "ServiceAccount",
						Name:      stateManagementProviderName + serviceAccountSuffix,
						Namespace: systemNamespace,
					},
				},
			},
		},
		"update-cluster-role": {
			stateManagementProvider: &kcmv1.StateManagementProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      stateManagementProviderName,
					Namespace: systemNamespace,
				},
				Spec: kcmv1.StateManagementProviderSpec{
					Adapter: kcmv1.ResourceReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test",
						Namespace:  metav1.NamespaceDefault,
					},
				},
			},
			discoveryResources: []*metav1.APIResourceList{
				{
					GroupVersion: "apps/v1",
					APIResources: []metav1.APIResource{
						{
							Name:       "deployments",
							Kind:       "Deployment",
							Namespaced: true,
						},
					},
				},
			},
			existingRBACObjects: []client.Object{
				&rbacv1.ClusterRole{
					ObjectMeta: metav1.ObjectMeta{
						Name: stateManagementProviderName + clusterRoleSuffix,
					},
					Rules: []rbacv1.PolicyRule{
						{
							APIGroups: []string{apiExtensionsGroup},
							Resources: []string{apiExtensionsResource},
							Verbs:     []string{"get", "list", "watch"},
						},
						{
							APIGroups: []string{"v1"},
							Verbs:     []string{"get", "list", "watch"},
							Resources: []string{"pods"},
						},
					},
				},
			},
			expectedServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      stateManagementProviderName + serviceAccountSuffix,
					Namespace: systemNamespace,
				},
			},
			expectedClusterRole: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: stateManagementProviderName + clusterRoleSuffix,
				},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{apiExtensionsGroup},
						Resources: []string{apiExtensionsResource},
						Verbs:     []string{"get", "list", "watch"},
					},
					{
						APIGroups: []string{"apps"},
						Verbs:     []string{"get", "list", "watch"},
						Resources: []string{"deployments"},
					},
				},
			},
			expectedClusterRoleBinding: &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: stateManagementProviderName + clusterRoleBindingSuffix,
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     stateManagementProviderName + clusterRoleSuffix,
				},
				Subjects: []rbacv1.Subject{
					{
						Kind:      "ServiceAccount",
						Name:      stateManagementProviderName + serviceAccountSuffix,
						Namespace: systemNamespace,
					},
				},
			},
		},
		"update-cluster-role-binding": {
			stateManagementProvider: &kcmv1.StateManagementProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      stateManagementProviderName,
					Namespace: systemNamespace,
				},
				Spec: kcmv1.StateManagementProviderSpec{
					Adapter: kcmv1.ResourceReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test",
						Namespace:  metav1.NamespaceDefault,
					},
				},
			},
			discoveryResources: []*metav1.APIResourceList{
				{
					GroupVersion: "apps/v1",
					APIResources: []metav1.APIResource{
						{
							Name:       "deployments",
							Kind:       "Deployment",
							Namespaced: true,
						},
					},
				},
			},
			existingRBACObjects: []client.Object{
				&rbacv1.ClusterRoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: stateManagementProviderName + clusterRoleBindingSuffix,
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "ClusterRole",
						Name:     stateManagementProviderName + clusterRoleSuffix,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							Name:      incorrectServiceAccountName,
							Namespace: metav1.NamespaceDefault,
						},
					},
				},
			},
			expectedServiceAccount: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      stateManagementProviderName + serviceAccountSuffix,
					Namespace: systemNamespace,
				},
			},
			expectedClusterRole: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: stateManagementProviderName + clusterRoleSuffix,
				},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{apiExtensionsGroup},
						Resources: []string{apiExtensionsResource},
						Verbs:     []string{"get", "list", "watch"},
					},
					{
						APIGroups: []string{"apps"},
						Verbs:     []string{"get", "list", "watch"},
						Resources: []string{"deployments"},
					},
				},
			},
			expectedClusterRoleBinding: &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: stateManagementProviderName + clusterRoleBindingSuffix,
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     stateManagementProviderName + clusterRoleSuffix,
				},
				Subjects: []rbacv1.Subject{
					{
						Kind:      "ServiceAccount",
						Name:      stateManagementProviderName + serviceAccountSuffix,
						Namespace: systemNamespace,
					},
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f(t, tc)
		})
	}
}

func fakeKubeClient(t *testing.T, smp *kcmv1.StateManagementProvider, objects ...client.Object) client.Client {
	t.Helper()
	builder := clientfake.NewClientBuilder().WithScheme(scheme(t)).WithObjects(smp)
	for _, obj := range objects {
		builder.WithObjects(obj)
	}
	return builder.Build()
}

func fakeDiscoveryClient(t *testing.T, resources []*metav1.APIResourceList) discovery.DiscoveryInterface {
	t.Helper()
	fakeClientset := kubefake.NewClientset()
	fakeDiscovery, ok := fakeClientset.Discovery().(*discoveryfake.FakeDiscovery)
	if !ok {
		t.Fatal("failed to get clientfake discovery client")
	}
	fakeDiscovery.Resources = resources
	return fakeDiscovery
}

func scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(kcmv1.AddToScheme(s))
	return s
}

func assertServiceAccount(t *testing.T, expected, actual *corev1.ServiceAccount) {
	t.Helper()
	assert.Equal(t, expected.Name, actual.Name)
	assert.Equal(t, expected.Namespace, actual.Namespace)
	assert.Equal(t, expected.AutomountServiceAccountToken, actual.AutomountServiceAccountToken)
	assert.ElementsMatch(t, expected.ImagePullSecrets, actual.ImagePullSecrets)
	assert.ElementsMatch(t, expected.Secrets, actual.Secrets)
}

func assertClusterRole(t *testing.T, expected, actual *rbacv1.ClusterRole) {
	t.Helper()
	assert.Equal(t, expected.Name, actual.Name)
	assert.ElementsMatch(t, expected.Rules, actual.Rules)
}

func assertClusterRoleBinding(t *testing.T, expected, actual *rbacv1.ClusterRoleBinding) {
	t.Helper()
	assert.Equal(t, expected.Name, actual.Name)
	assert.Equal(t, expected.RoleRef, actual.RoleRef)
	assert.ElementsMatch(t, expected.Subjects, actual.Subjects)
}
