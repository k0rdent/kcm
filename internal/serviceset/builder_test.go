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

package serviceset

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// Test_Builder_Build_namespacedMultiClusterService covers the branches Build takes for a
// NamespacedMultiClusterService owner - previously untested, since this package had no
// builder_test.go at all.
func Test_Builder_Build_namespacedMultiClusterService(t *testing.T) {
	t.Parallel()

	selector := &metav1.LabelSelector{}

	t.Run("NamespacedMultiClusterService alone: owner, spec field, and provider config all come from it", func(t *testing.T) {
		t.Parallel()

		nmcs := &kcmv1.NamespacedMultiClusterService{
			Namespace: "team-a", Name: "my-nmcs",
			Spec: kcmv1.MultiClusterServiceSpec{
				ServiceSpec: kcmv1.ServiceSpec{
					// SelfManagement on a NamespacedMultiClusterService must be ignored -
					// Build forces .Spec.Provider.SelfManagement to false regardless.
					Provider: kcmv1.StateManagementProviderConfig{SelfManagement: true},
				},
			},
		}

		sset := &kcmv1.ServiceSet{}
		got, err := NewBuilder(nil, sset, selector).
			WithMultiClusterServiceCommon(nmcs).
			Build()
		require.NoError(t, err)

		require.Len(t, got.OwnerReferences, 1)
		owner := got.OwnerReferences[0]
		require.Equal(t, kcmv1.NamespacedMultiClusterServiceKind, owner.Kind)
		require.Equal(t, "my-nmcs", owner.Name)

		require.Equal(t, "team-a/my-nmcs", got.Spec.NamespacedMultiClusterService)
		require.Empty(t, got.Spec.MultiClusterService)
		require.Empty(t, got.Spec.Cluster)
		require.False(t, got.Spec.Provider.SelfManagement)
	})

	t.Run("ClusterDeployment and NamespacedMultiClusterService both set: CD owns, but the provider config comes from the NamespacedMultiClusterService", func(t *testing.T) {
		t.Parallel()

		cd := &kcmv1.ClusterDeployment{
			Namespace: "team-a", Name: "my-cd",
			Spec: kcmv1.ClusterDeploymentSpec{
				ServiceSpec: kcmv1.ServiceSpec{
					Provider: kcmv1.StateManagementProviderConfig{Name: "cd-provider"},
				},
			},
		}
		nmcs := &kcmv1.NamespacedMultiClusterService{
			Namespace: "team-a", Name: "my-nmcs",
			Spec: kcmv1.MultiClusterServiceSpec{
				ServiceSpec: kcmv1.ServiceSpec{
					Provider: kcmv1.StateManagementProviderConfig{Name: "nmcs-provider"},
				},
			},
		}

		sset := &kcmv1.ServiceSet{}
		got, err := NewBuilder(cd, sset, selector).
			WithMultiClusterServiceCommon(nmcs).
			Build()
		require.NoError(t, err)

		require.Len(t, got.OwnerReferences, 1)
		require.Equal(t, kcmv1.ClusterDeploymentKind, got.OwnerReferences[0].Kind)
		require.Equal(t, "my-cd", got.OwnerReferences[0].Name)

		// Ownership and provider config are selected independently: the ClusterDeployment owns the
		// ServiceSet, but the NamespacedMultiClusterService that asked for these services supplies the
		// provider config. Coupling the two would drop its settings and rewrite the immutable
		// .spec.provider.name of an already existing ServiceSet.
		require.Equal(t, "nmcs-provider", got.Spec.Provider.Name)

		require.Equal(t, "my-cd", got.Spec.Cluster)
		require.Equal(t, "team-a/my-nmcs", got.Spec.NamespacedMultiClusterService)
		require.False(t, got.Spec.Provider.SelfManagement)
	})

	t.Run("MultiClusterService (cluster-scoped) alone: owner Kind and spec field differ from NamespacedMultiClusterService", func(t *testing.T) {
		t.Parallel()

		mcs := &kcmv1.MultiClusterService{
			Name: "my-mcs",
		}

		sset := &kcmv1.ServiceSet{}
		got, err := NewBuilder(nil, sset, selector).
			WithMultiClusterServiceCommon(mcs).
			Build()
		require.NoError(t, err)

		require.Len(t, got.OwnerReferences, 1)
		require.Equal(t, kcmv1.MultiClusterServiceKind, got.OwnerReferences[0].Kind)
		require.Equal(t, "my-mcs", got.Spec.MultiClusterService)
		require.Empty(t, got.Spec.NamespacedMultiClusterService)
		// SelfManagement is meaningful (not forced false) for a cluster-scoped MultiClusterService
		// with no ClusterDeployment - this is the self-management ServiceSet.
		require.True(t, got.Spec.Provider.SelfManagement)
	})
}

// Test_Builder_Build_providerConfigPrecedence covers the combination that reaches Build on every
// reconcile of a MultiClusterService matching a ClusterDeployment: both objects are set, and the
// provider config has to come from the MCS rather than from the cluster it happens to target.
func Test_Builder_Build_providerConfigPrecedence(t *testing.T) {
	t.Parallel()

	selector := &metav1.LabelSelector{}

	t.Run("cluster-scoped MultiClusterService matching a ClusterDeployment: the provider name comes from the MCS", func(t *testing.T) {
		t.Parallel()

		cd := &kcmv1.ClusterDeployment{
			Namespace: "kcm-system", Name: "my-cd",
			Spec: kcmv1.ClusterDeploymentSpec{
				ServiceSpec: kcmv1.ServiceSpec{
					Provider: kcmv1.StateManagementProviderConfig{Name: "cd-provider"},
				},
			},
		}
		mcs := &kcmv1.MultiClusterService{
			Name: "my-mcs",
			Spec: kcmv1.MultiClusterServiceSpec{
				ServiceSpec: kcmv1.ServiceSpec{
					Provider: kcmv1.StateManagementProviderConfig{Name: "mcs-provider"},
				},
			},
		}

		sset := &kcmv1.ServiceSet{}
		got, err := NewBuilder(cd, sset, selector).
			WithMultiClusterServiceCommon(mcs).
			Build()
		require.NoError(t, err)

		// The ClusterDeployment still owns the ServiceSet and is still recorded as its cluster.
		require.Len(t, got.OwnerReferences, 1)
		require.Equal(t, kcmv1.ClusterDeploymentKind, got.OwnerReferences[0].Kind)
		require.Equal(t, "my-cd", got.Spec.Cluster)
		require.Equal(t, "my-mcs", got.Spec.MultiClusterService)

		// GetServiceSetWithOperation resolves the StateManagementProvider from the MCS spec and passes
		// that provider's selector in as the ServiceSet labels, so the name written here has to match.
		require.Equal(t, "mcs-provider", got.Spec.Provider.Name)

		// Not the self-management ServiceSet: this one targets a ClusterDeployment.
		require.False(t, got.Spec.Provider.SelfManagement)
	})

	t.Run("default provider: the deprecated service spec settings come from the MCS, not the ClusterDeployment", func(t *testing.T) {
		t.Parallel()

		// Neither side names a provider, so both resolve to the default one and only the marshalled
		// config distinguishes them. This is the case that affects every user of the default provider,
		// not just those running a custom StateManagementProvider.
		cd := &kcmv1.ClusterDeployment{
			Namespace: "kcm-system", Name: "my-cd",
			Spec: kcmv1.ClusterDeploymentSpec{
				ServiceSpec: kcmv1.ServiceSpec{SyncMode: "OneTime", Priority: 10}, //nolint:staticcheck // SA1019: exercising the deprecated fields on purpose
			},
		}
		mcs := &kcmv1.MultiClusterService{
			Name: "my-mcs",
			Spec: kcmv1.MultiClusterServiceSpec{
				ServiceSpec: kcmv1.ServiceSpec{SyncMode: "ContinuousWithDriftDetection", Priority: 999}, //nolint:staticcheck // SA1019: exercising the deprecated fields on purpose
			},
		}

		sset := &kcmv1.ServiceSet{}
		got, err := NewBuilder(cd, sset, selector).
			WithMultiClusterServiceCommon(mcs).
			Build()
		require.NoError(t, err)

		require.NotNil(t, got.Spec.Provider.Config)
		require.JSONEq(t,
			`{"syncMode":"ContinuousWithDriftDetection","priority":999}`,
			string(got.Spec.Provider.Config.Raw))
	})
}
