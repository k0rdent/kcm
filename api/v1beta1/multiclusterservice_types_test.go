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

package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestIsMCSNil guards the exact typed-nil trap this helper exists for: a MultiClusterServiceCommon
// interface value holding a nil *MultiClusterService or *NamespacedMultiClusterService compares
// unequal to a plain nil, yet must still be reported as "nil" by IsMCSNil so callers don't panic
// on the first method call.
func TestIsMCSNil(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mcs  MultiClusterServiceCommon
		want bool
	}{
		{name: "nil interface", mcs: nil, want: true},
		{name: "typed-nil MultiClusterService", mcs: (*MultiClusterService)(nil), want: true},
		{name: "typed-nil NamespacedMultiClusterService", mcs: (*NamespacedMultiClusterService)(nil), want: true},
		{name: "non-nil MultiClusterService", mcs: &MultiClusterService{}, want: false},
		{name: "non-nil NamespacedMultiClusterService", mcs: &NamespacedMultiClusterService{}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, IsMCSNil(tt.mcs))
		})
	}
}

func TestMCSKind(t *testing.T) {
	t.Parallel()

	require.Equal(t, MultiClusterServiceKind, MCSKind(&MultiClusterService{}))
	require.Equal(t, NamespacedMultiClusterServiceKind, MCSKind(&NamespacedMultiClusterService{}))
}

// TestNamespacedMultiClusterService_GetFullname guards the exact "namespace/name" format:
// it is not just a display string, it is the value written into a ServiceSet's
// .spec.namespacedMultiClusterService and the value matched by
// ServiceSetNamespacedMultiClusterServiceIndexKey, so writer and reader must agree on it.
func TestNamespacedMultiClusterService_GetFullname(t *testing.T) {
	t.Parallel()

	nmcs := &NamespacedMultiClusterService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "my-nmcs"},
	}
	require.Equal(t, "team-a/my-nmcs", nmcs.GetFullname())
}

// TestMultiClusterServiceCommon_pointerAliasing guards the interface contract documented on
// MultiClusterServiceCommon: GetMultiClusterServiceSpec/GetMultiClusterServiceStatus must return
// pointers into the receiver's own fields, not copies, since callers (e.g. updateStatus in the
// MultiClusterService controller) mutate through the returned pointer and then persist the
// object itself. A value receiver or a DeepCopy-returning implementation would still satisfy the
// interface and compile, silently dropping every such write.
func TestMultiClusterServiceCommon_pointerAliasing(t *testing.T) {
	t.Parallel()

	t.Run("MultiClusterService", func(t *testing.T) {
		t.Parallel()
		mcs := &MultiClusterService{}
		mcs.GetMultiClusterServiceSpec().DependsOn = []string{"dep"}
		require.Equal(t, []string{"dep"}, mcs.Spec.DependsOn)

		mcs.GetMultiClusterServiceStatus().ObservedGeneration = 7
		require.Equal(t, int64(7), mcs.Status.ObservedGeneration)
	})

	t.Run("NamespacedMultiClusterService", func(t *testing.T) {
		t.Parallel()
		nmcs := &NamespacedMultiClusterService{}
		nmcs.GetMultiClusterServiceSpec().DependsOn = []string{"dep"}
		require.Equal(t, []string{"dep"}, nmcs.Spec.DependsOn)

		nmcs.GetMultiClusterServiceStatus().ObservedGeneration = 7
		require.Equal(t, int64(7), nmcs.Status.ObservedGeneration)
	})
}
