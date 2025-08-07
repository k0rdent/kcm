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

package collector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func Test_gpuOperatorPresence(t *testing.T) {
	cases := []struct {
		name     string
		names    []string
		expected [2]bool
	}{
		{"none", []string{"some-ds"}, [2]bool{false, false}},
		{"nvidia only", []string{"gpu-operator-node-feature-discovery"}, [2]bool{true, false}},
		{"amd only", []string{"amd-gpu-operator-node-feature-discovery"}, [2]bool{false, true}},
		{"both", []string{"gpu-operator-node-feature-discovery", "amd-gpu-operator-node-feature-discovery", "third-ds"}, [2]bool{true, true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dses := make([]metav1.PartialObjectMetadata, len(tc.names))
			for i, n := range tc.names {
				dses[i].Name = n
			}
			nvidia, amd := getGpuOperatorPresence(dses)
			assert.Equal(t, tc.expected[0], nvidia)
			assert.Equal(t, tc.expected[1], amd)
		})
	}
}

func Test_streamPaginatedNodes(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))

	node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node1"}}
	node2 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node2"}}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(node1, node2).Build()
	var collected []string

	err := streamPaginatedNodes(t.Context(), cl, 1, func(batch []*corev1.Node) {
		for _, n := range batch {
			collected = append(collected, n.Name)
		}
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"node1", "node2"}, collected)
}

func Test_streamPaginatedPods(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))

	pod1 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod1"}}
	pod2 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod2"}}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(pod1, pod2).Build()
	var collected []string

	err := streamPaginatedPods(t.Context(), cl, 1, func(batch []*corev1.Pod) {
		for _, n := range batch {
			collected = append(collected, n.Name)
		}
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"pod1", "pod2"}, collected)
}

func Test_countUserServices(t *testing.T) {
	labelsToMatch := map[string]string{"foo": "bar"}

	var (
		partialClusters = []metav1.PartialObjectMetadata{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "foo",
					Labels: labelsToMatch,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "bar",
					Labels: nil, // nothing
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "baz",
					Labels: labelsToMatch,
				},
			},
		}

		mcs = &kcmv1.MultiClusterServiceList{
			Items: []kcmv1.MultiClusterService{
				{
					Spec: kcmv1.MultiClusterServiceSpec{
						ClusterSelector: metav1.LabelSelector{MatchLabels: labelsToMatch},
						ServiceSpec: kcmv1.ServiceSpec{
							Services: []kcmv1.Service{{}, {}},
						},
					},
				},
				{
					Spec: kcmv1.MultiClusterServiceSpec{
						ClusterSelector: metav1.LabelSelector{MatchLabels: map[string]string{"nothing": ""}},
						ServiceSpec: kcmv1.ServiceSpec{
							Services: []kcmv1.Service{{}, {}},
						},
					},
				},
			},
		}

		cld = &kcmv1.ClusterDeployment{
			Spec: kcmv1.ClusterDeploymentSpec{
				ServiceSpec: kcmv1.ServiceSpec{Services: []kcmv1.Service{{}}},
			},
		}
	)

	n := countUserServices(cld, mcs, partialClusters)
	assert.Equal(t, 5, n) // 1 (from cld) + 4 (from mcs)
}

func Test_getK0sClusterID(t *testing.T) {
	const fakeClusterID = "cluster-id"

	clusters := []metav1.PartialObjectMetadata{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cld1", Annotations: map[string]string{k0sClusterIDAnnotation: fakeClusterID}},
			TypeMeta:   metav1.TypeMeta{Kind: clusterapiv1.ClusterKind, APIVersion: clusterapiv1.GroupVersion.String()},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cld2"},
			TypeMeta:   metav1.TypeMeta{Kind: clusterapiv1.ClusterKind, APIVersion: clusterapiv1.GroupVersion.String()},
		},
	}

	require.Equal(t, fakeClusterID, getK0sClusterID(clusters, client.ObjectKey{Name: "cld1"}))
}

func Test_listAsPartial(t *testing.T) {
	objs := []client.Object{
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "a"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "b"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "c"}},
	}

	reqs := require.New(t)
	scheme := runtime.NewScheme()
	reqs.NoError(metav1.AddMetaToScheme(scheme))
	reqs.NoError(corev1.AddToScheme(scheme))
	fakeCl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Node"}
	items, err := listAsPartial(t.Context(), fakeCl, gvk)
	reqs.NoError(err)
	reqs.Len(items, 3, "expected all items across pages")
	names := []string{items[0].Name, items[1].Name, items[2].Name}
	reqs.Equal([]string{"a", "b", "c"}, names)
}
