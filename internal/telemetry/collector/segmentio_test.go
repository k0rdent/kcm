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
	"fmt"
	"strconv"
	"testing"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/segmentio/analytics-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	kubevirtv1 "kubevirt.io/api/core/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func Test_accumulateNode(t *testing.T) {
	makeNode := func(name string, cpu, mem, nvidia, amd int64) *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    *resourceQuantity(cpu),
					corev1.ResourceMemory: *resourceQuantity(mem),
					nvidiaGPUKey:          *resourceQuantity(nvidia),
					amdGPUKey:             *resourceQuantity(amd),
				},
				NodeInfo: corev1.NodeSystemInfo{
					OSImage:        "test-os",
					Architecture:   "amd64",
					KernelVersion:  "5.10.0-test",
					KubeletVersion: "v1.26.0+test-flavor",
				},
			},
		}
	}

	acc := &nodeDataAccumulator{}
	accumulateNode(acc, makeNode("node1", 2, 4, 2, 0))
	accumulateNode(acc, makeNode("node2", 2, 4, 0, 2))
	accumulateNode(acc, makeNode("node3", 2, 4, 0, 0))

	assert.Equal(t, uint64(6), acc.totalCPU)
	assert.Equal(t, uint64(12), acc.totalMemory)
	assert.Equal(t, uint(2), acc.totalGPUNodes)
	assert.Equal(t, uint(3), acc.count)

	expectedNode := makeNode("test", 0, 0, 0, 0)
	kubeFlavor, kubeVersion := "test-flavor", "v1.26.0"

	assert.Len(t, acc.nodeInfos, 3)
	for _, v := range acc.nodeInfos {
		assert.Equal(t, v["os"], expectedNode.Status.NodeInfo.OSImage)
		assert.Equal(t, v["arch"], expectedNode.Status.NodeInfo.Architecture)
		assert.Equal(t, v["kernelVersion"], expectedNode.Status.NodeInfo.KernelVersion)
		assert.Equal(t, v["kubeVersion"], kubeVersion)
		assert.Equal(t, v["kubeFlavor"], kubeFlavor)
		// nodes gpu capacity implicitly has been already checked
	}
}

func Test_accumulatePodGpu(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			NodeName: "node1",
			Containers: []corev1.Container{{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						nvidiaGPUKey: *resourceQuantity(1),
						amdGPUKey:    *resourceQuantity(2),
					},
				},
			}},
		},
	}
	nodeInfo := map[string]string{"name": "node1"}
	acc := &podDataAccumulator{
		nodeInfos:    &[]map[string]string{nodeInfo},
		nodeName2Idx: map[string]int{"node1": 0},
	}
	accumulatePodGpu(acc, pod)

	assert.Equal(t, uint(1), acc.podsWithGPUReqs)
	assert.Equal(t, "1", nodeInfo["gpu.nvidia.bytes"])
	assert.Equal(t, "2", nodeInfo["gpu.amd.bytes"])
}

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

func Test_collectChildProperties(t *testing.T) {
	ctx := t.Context()
	type testCase struct {
		name    string
		objects []client.Object
		expect  map[string]any
	}

	cases := []testCase{
		{
			name:    "no resources",
			objects: nil,
			expect: map[string]any{
				"node.count":                    uint(0),
				"node.cpu.total":                uint64(0),
				"node.memory.bytes":             uint64(0),
				"node.gpu.total":                uint(0),
				"node.info":                     []map[string]string(nil),
				"pods.with_gpu_requests":        uint(0),
				"gpu.operator_installed.nvidia": false,
				"gpu.operator_installed.amd":    false,
				"kubevirt.vmis":                 0,
			},
		},
		{
			name: "single node",
			objects: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2000m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						NodeInfo: corev1.NodeSystemInfo{
							OSImage:        "osimg",
							Architecture:   "amd64",
							KernelVersion:  "kerver",
							KubeletVersion: "kubver+fl",
						},
					},
				},
			},
			expect: map[string]any{
				"node.count":        uint(1),
				"node.cpu.total":    uint64(2),
				"node.memory.bytes": uint64(1 << 30),
				"node.gpu.total":    uint(0),
				"node.info": []map[string]string{{
					"name":                      "node1",
					"arch":                      "amd64",
					"kernelVersion":             "kerver",
					"kubeVersion":               "kubver",
					"kubeFlavor":                "fl",
					"gpu.amd.bytes_capacity":    "0",
					"gpu.nvidia.bytes_capacity": "0",
					"os":                        "osimg",
				}},
				"pods.with_gpu_requests":        uint(0),
				"gpu.operator_installed.nvidia": false,
				"gpu.operator_installed.amd":    false,
				"kubevirt.vmis":                 0,
			},
		},
		{
			name: "node with GPU pod",
			objects: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1000m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
						NodeInfo: corev1.NodeSystemInfo{},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "gpu-pod"},
					Spec: corev1.PodSpec{
						NodeName: "node1",
						Containers: []corev1.Container{{
							Name: "ctr-1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{nvidiaGPUKey: resource.MustParse("1")},
							},
						}},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "non-gpu-pod"},
					Spec: corev1.PodSpec{
						NodeName: "node1",
						Containers: []corev1.Container{{
							Name: "ctr-2",
						}},
					},
				},
			},
			expect: map[string]any{
				"node.count":        uint(1),
				"node.cpu.total":    uint64(1),
				"node.memory.bytes": uint64(512 * 1 << 20),
				"node.gpu.total":    uint(0),
				"node.info": []map[string]string{{
					"name":                      "node1",
					"arch":                      "",
					"kernelVersion":             "",
					"kubeVersion":               "",
					"kubeFlavor":                "",
					"gpu.amd.bytes_capacity":    "0",
					"gpu.amd.bytes":             "0",
					"gpu.nvidia.bytes_capacity": "0",
					"gpu.nvidia.bytes":          "1",
					"os":                        "",
				}},
				"pods.with_gpu_requests":        uint(1),
				"gpu.operator_installed.nvidia": false,
				"gpu.operator_installed.amd":    false,
				"kubevirt.vmis":                 0,
			},
		},
		{
			name: "node without GPU pods",
			objects: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1000m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
						NodeInfo: corev1.NodeSystemInfo{},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "non-gpu-pod-0"},
					Spec: corev1.PodSpec{
						NodeName: "node1",
						Containers: []corev1.Container{{
							Name: "ctr-1",
						}},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "non-gpu-pod-1"},
					Spec: corev1.PodSpec{
						NodeName: "node1",
						Containers: []corev1.Container{{
							Name: "ctr-2",
						}},
					},
				},
			},
			expect: map[string]any{
				"node.count":        uint(1),
				"node.cpu.total":    uint64(1),
				"node.memory.bytes": uint64(512 * 1 << 20),
				"node.gpu.total":    uint(0),
				"node.info": []map[string]string{{
					"name":                      "node1",
					"arch":                      "",
					"kernelVersion":             "",
					"kubeVersion":               "",
					"kubeFlavor":                "",
					"gpu.amd.bytes_capacity":    "0",
					"gpu.nvidia.bytes_capacity": "0",
					"os":                        "",
				}},
				"pods.with_gpu_requests":        uint(0),
				"gpu.operator_installed.nvidia": false,
				"gpu.operator_installed.amd":    false,
				"kubevirt.vmis":                 0,
			},
		},
		{
			name: "daemonsets without gpu-operators",
			objects: []client.Object{
				&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "ds1"}},
				&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "ds2"}},
			},
			expect: map[string]any{
				"node.count":                    uint(0),
				"node.cpu.total":                uint64(0),
				"node.memory.bytes":             uint64(0),
				"node.gpu.total":                uint(0),
				"node.info":                     []map[string]string(nil),
				"pods.with_gpu_requests":        uint(0),
				"gpu.operator_installed.nvidia": false,
				"gpu.operator_installed.amd":    false,
				"kubevirt.vmis":                 0,
			},
		},
		{
			name: "daemonsets with gpu-operators",
			objects: []client.Object{
				&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "gpu-operator-node-feature-discovery"}},
				&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "amd-gpu-operator-node-feature-discovery"}},
			},
			expect: map[string]any{
				"node.count":                    uint(0),
				"node.cpu.total":                uint64(0),
				"node.memory.bytes":             uint64(0),
				"node.gpu.total":                uint(0),
				"node.info":                     []map[string]string(nil),
				"pods.with_gpu_requests":        uint(0),
				"gpu.operator_installed.nvidia": true,
				"gpu.operator_installed.amd":    true,
				"kubevirt.vmis":                 0,
			},
		},
		{
			name: "kubevirt vmis present",
			objects: []client.Object{
				&kubevirtv1.VirtualMachineInstance{ObjectMeta: metav1.ObjectMeta{Name: "vmi1"}},
				&kubevirtv1.VirtualMachineInstance{ObjectMeta: metav1.ObjectMeta{Name: "vmi2"}},
			},
			expect: map[string]any{
				"node.count":                    uint(0),
				"node.cpu.total":                uint64(0),
				"node.memory.bytes":             uint64(0),
				"node.gpu.total":                uint(0),
				"node.info":                     []map[string]string(nil),
				"pods.with_gpu_requests":        uint(0),
				"gpu.operator_installed.nvidia": false,
				"gpu.operator_installed.amd":    false,
				"kubevirt.vmis":                 2,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqs := require.New(t)
			scheme := runtime.NewScheme()
			reqs.NoError(corev1.AddToScheme(scheme))
			reqs.NoError(metav1.AddMetaToScheme(scheme))
			reqs.NoError(appsv1.AddToScheme(scheme))
			reqs.NoError(kubevirtv1.AddToScheme(scheme))

			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.objects...).Build()
			sut := &SegmentIO{}
			props, err := sut.collectChildProperties(ctx, client)
			reqs.NoError(err)

			// check all expected keys and values
			reqs.Len(props, len(tc.expect), "unexpected number of keys in props")
			for key, exp := range tc.expect {
				val, exists := props[key]
				reqs.True(exists, "expected key %s", key)
				reqs.Equal(exp, val, "mismatch for key %s", key)
			}
		})
	}
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

func Test_getPartialClustersToCountServices(t *testing.T) {
	svC := &libsveltosv1beta1.SveltosCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "sveltos1"},
	}
	cl1, cl2 := &clusterapiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "capi1"},
	}, &clusterapiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "capi2"},
	}

	reqs := require.New(t)
	scheme := runtime.NewScheme()
	reqs.NoError(metav1.AddMetaToScheme(scheme))
	reqs.NoError(libsveltosv1beta1.AddToScheme(scheme))
	reqs.NoError(clusterapiv1.AddToScheme(scheme))

	sut := &SegmentIO{crCl: fake.NewClientBuilder().WithScheme(scheme).WithObjects(svC, cl1, cl2).Build()}
	capiClusters, sveltosClusters, err := sut.getPartialClustersToCountServices(t.Context())
	reqs.NoError(err)
	reqs.Len(capiClusters, 2)
	reqs.Len(sveltosClusters, 1)
	names := []string{capiClusters[0].Name, capiClusters[1].Name, sveltosClusters[0].Name}
	reqs.Equal([]string{"capi1", "capi2", "sveltos1"}, names)
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

type mockSegment struct {
	events []analytics.Track
}

func (m *mockSegment) Enqueue(ev analytics.Message) error {
	c, ok := ev.(analytics.Track)
	if !ok {
		return fmt.Errorf("unexpected type of the client mock %T", ev)
	}
	m.events = append(m.events, c)
	return nil
}

func (*mockSegment) Close() error { return nil }

var _ analytics.Client = (*mockSegment)(nil)

func TestCollect(t *testing.T) {
	reqs := require.New(t)

	// prepare
	scheme := runtime.NewScheme()
	reqs.NoError(clientgoscheme.AddToScheme(scheme))
	reqs.NoError(kcmv1.AddToScheme(scheme))
	reqs.NoError(clusterapiv1.AddToScheme(scheme))
	reqs.NoError(libsveltosv1beta1.AddToScheme(scheme))

	const (
		ns       = "test-ns"
		mgmtName = kcmv1.ManagementName
		mgmtUID  = "mgmt-uid"
		cldUID   = "cld-uid"

		clusterTplName = "test-cluster-template-name"
	)
	mgmt := &kcmv1.Management{
		ObjectMeta: metav1.ObjectMeta{Name: mgmtName, UID: types.UID(mgmtUID)},
	}

	tpl := &kcmv1.ClusterTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: clusterTplName, Namespace: ns},
		Status: kcmv1.ClusterTemplateStatus{
			Providers: []string{"prov1"},
			TemplateStatusCommon: kcmv1.TemplateStatusCommon{
				ChartVersion: "cv1",
			},
		},
	}

	objs := []client.Object{mgmt, tpl}
	for i := range 2 {
		cldName := "cld" + strconv.Itoa(i)
		objs = append(objs,
			&kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: cldName, Namespace: ns, UID: types.UID(cldUID)},
				Spec: kcmv1.ClusterDeploymentSpec{
					Template:    clusterTplName,
					ServiceSpec: kcmv1.ServiceSpec{SyncMode: "Continuous"},
				},
			},
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: cldName + "-kubeconfig", Namespace: ns},
				Data:       map[string][]byte{"value": nil}, // does no matter
			},
		)
	}

	mgmtCl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	fakeFactory := func(_ []byte, s *runtime.Scheme) (client.Client, error) {
		return fake.NewClientBuilder().WithScheme(s).WithObjects(testChildObjects(t)...).Build(), nil
	}

	segmentClient := &mockSegment{}
	// create collector
	collector, err := NewSegmentIO(segmentClient, mgmtCl, 1)
	reqs.NoError(err, "construct the collector")
	collector.childFactory = fakeFactory

	// run
	ctx := logf.IntoContext(t.Context(), zap.New(zap.UseDevMode(true)))
	reqs.NoError(collector.Collect(ctx))

	// verify
	reqs.Len(segmentClient.events, 2)
	clds := make([]*kcmv1.ClusterDeployment, 0, 2) // quick kludge
	for _, o := range objs {
		cld, ok := o.(*kcmv1.ClusterDeployment)
		if !ok {
			continue
		}
		clds = append(clds, cld)
	}
	reqs.Len(clds, 2)
	for i, event := range segmentClient.events {
		reqs.Equal(mgmtUID, event.AnonymousId)
		reqs.Equal("ChildDataHearbeat", event.Event)
		props := event.Properties

		reqs.Equal(ns+"/"+"cld"+strconv.Itoa(i), props["cluster"])
		reqs.Equal(cldUID, props["clusterDeploymentID"])
		reqs.Empty(props["clusterID"])
		reqs.Equal(tpl.Status.Providers, props["providers"])
		reqs.Equal(tpl.Status.ChartVersion, props["templateHelmChartVersion"])
		reqs.Equal(clds[i].Spec.Template, props["template"])
		reqs.Equal(clds[i].Spec.ServiceSpec.SyncMode, props["syncMode"])
		reqs.Equal(0, props["userServiceCount"])

		reqs.Equal(false, props["gpu.operator_installed.amd"])
		reqs.Equal(true, props["gpu.operator_installed.nvidia"])
		reqs.Equal(2, props["kubevirt.vmis"])
		reqs.Equal(uint(2), props["pods.with_gpu_requests"])
		reqs.Equal(uint64(2*2), props["node.cpu.total"])
		reqs.Equal(uint(2), props["node.gpu.total"])
		reqs.Equal(uint64(2*1<<10), props["node.memory.bytes"])
		reqs.Equal(uint(2), props["node.count"])

		// smoke
		infos, ok := props["node.info"].([]map[string]string)
		reqs.True(ok)
		reqs.Len(infos, 2)
	}
}

func testChildObjects(t *testing.T) []client.Object {
	t.Helper()

	objects := []client.Object{}
	for i := range 2 {
		itoa := strconv.Itoa(i)
		nodeName := "node" + itoa
		objects = append(objects,
			&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resourceQuantity(2),
						corev1.ResourceMemory: *resourceQuantity(1 << 10),
						nvidiaGPUKey:          *resourceQuantity(1),
						amdGPUKey:             *resourceQuantity(1),
					},
					NodeInfo: corev1.NodeSystemInfo{
						OSImage:        "test-os",
						Architecture:   "amd64",
						KernelVersion:  "5.10.0-test",
						KubeletVersion: "v1.26.0+test-flavor",
					},
				},
			},
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod" + itoa},
				Spec: corev1.PodSpec{
					NodeName: nodeName,
					Containers: []corev1.Container{{
						Name: "container",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								nvidiaGPUKey: *resourceQuantity(1),
								amdGPUKey:    *resourceQuantity(2),
							},
						},
					}},
				},
			},
			&appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Name: "gpu-operator-" + itoa},
			},
			&kubevirtv1.VirtualMachineInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "vmi" + itoa},
			},
		)
	}

	return objects
}

func TestClose(t *testing.T) {
	require.NoError(t, (&SegmentIO{segmentCl: &mockSegment{}}).Close(t.Context()))
}

func TestFlush(t *testing.T) {
	require.NoError(t, (&SegmentIO{segmentCl: &mockSegment{}}).Flush(t.Context()))
}

func resourceQuantity(v int64) *resource.Quantity {
	return resource.NewQuantity(v, resource.DecimalSI)
}
