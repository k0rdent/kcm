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

	"github.com/go-logr/logr"
	"github.com/segmentio/analytics-go/v3"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func Test_SegmentIO_collectChildProperties(t *testing.T) {
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
				"node.count":                    uint64(0),
				"node.cpu.total":                uint64(0),
				"node.memory.bytes":             uint64(0),
				"node.gpu.total":                uint64(0),
				"node.info":                     []map[string]string(nil),
				"pods.with_gpu_requests":        uint64(0),
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
							OperatingSystem: "linux",
							Architecture:    "amd64",
							KernelVersion:   "kerver",
							KubeletVersion:  "kubver+fl",
						},
					},
				},
			},
			expect: map[string]any{
				"node.count":        uint64(1),
				"node.cpu.total":    uint64(2),
				"node.memory.bytes": uint64(1 << 30),
				"node.gpu.total":    uint64(0),
				"node.info": []map[string]string{{
					"name":                      "node1",
					"arch":                      "amd64",
					"kernelVersion":             "kerver",
					"kubeVersion":               "kubver",
					"kubeFlavor":                "fl",
					"gpu.amd.bytes_capacity":    "0",
					"gpu.nvidia.bytes_capacity": "0",
					"os":                        "linux",
				}},
				"pods.with_gpu_requests":        uint64(0),
				"gpu.operator_installed.nvidia": false,
				"gpu.operator_installed.amd":    false,
				"kubevirt.vmis":                 0,
			},
		},
		{
			name: "node with GPU pods",
			objects: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1000m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
							amdGPUKey:             resource.MustParse("1Ki"),
							nvidiaGPUKey:          resource.MustParse("2Ki"),
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
					ObjectMeta: metav1.ObjectMeta{Name: "another-gpu-pod"},
					Spec: corev1.PodSpec{
						NodeName: "node1",
						Containers: []corev1.Container{{
							Name: "ctr-2",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									nvidiaGPUKey: resource.MustParse("1"),
									amdGPUKey:    resource.MustParse("2"),
								},
							},
						}},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "non-gpu-pod"},
					Spec: corev1.PodSpec{
						NodeName: "node1",
						Containers: []corev1.Container{{
							Name: "ctr-3",
						}},
					},
				},
			},
			expect: map[string]any{
				"node.count":        uint64(1),
				"node.cpu.total":    uint64(1),
				"node.memory.bytes": uint64(512 * 1 << 20),
				"node.gpu.total":    uint64(1),
				"node.info": []map[string]string{{
					"name":                      "node1",
					"arch":                      "",
					"kernelVersion":             "",
					"kubeVersion":               "",
					"kubeFlavor":                "",
					"gpu.amd.bytes_capacity":    "1024",
					"gpu.amd.bytes":             "2",
					"gpu.nvidia.bytes_capacity": "2048",
					"gpu.nvidia.bytes":          "2",
					"os":                        "",
				}},
				"pods.with_gpu_requests":        uint64(2),
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
							amdGPUKey:             resource.MustParse("1Ki"),
							nvidiaGPUKey:          resource.MustParse("2Ki"),
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
				"node.count":        uint64(1),
				"node.cpu.total":    uint64(1),
				"node.memory.bytes": uint64(512 * 1 << 20),
				"node.gpu.total":    uint64(1),
				"node.info": []map[string]string{{
					"name":                      "node1",
					"arch":                      "",
					"kernelVersion":             "",
					"kubeVersion":               "",
					"kubeFlavor":                "",
					"gpu.amd.bytes_capacity":    "1024",
					"gpu.nvidia.bytes_capacity": "2048",
					"os":                        "",
				}},
				"pods.with_gpu_requests":        uint64(0),
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
				"node.count":                    uint64(0),
				"node.cpu.total":                uint64(0),
				"node.memory.bytes":             uint64(0),
				"node.gpu.total":                uint64(0),
				"node.info":                     []map[string]string(nil),
				"pods.with_gpu_requests":        uint64(0),
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
				"node.count":                    uint64(0),
				"node.cpu.total":                uint64(0),
				"node.memory.bytes":             uint64(0),
				"node.gpu.total":                uint64(0),
				"node.info":                     []map[string]string(nil),
				"pods.with_gpu_requests":        uint64(0),
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
				"node.count":                    uint64(0),
				"node.cpu.total":                uint64(0),
				"node.memory.bytes":             uint64(0),
				"node.gpu.total":                uint64(0),
				"node.info":                     []map[string]string(nil),
				"pods.with_gpu_requests":        uint64(0),
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

func Test_SegmentIO_Collect(t *testing.T) {
	reqs := require.New(t)

	// prepare
	scheme := buildMgmtScheme(t)

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

	mgmtCRD := &metav1.PartialObjectMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Name: "managements.k0rdent.mirantis.com",
		},
	}
	mgmtCRD.SetGroupVersionKind(apiextv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"))

	tpl := &kcmv1.ClusterTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: clusterTplName, Namespace: ns},
		Status: kcmv1.ClusterTemplateStatus{
			Providers: []string{"prov1"},
			TemplateStatusCommon: kcmv1.TemplateStatusCommon{
				ChartVersion: "cv1",
			},
		},
	}

	objs := []client.Object{mgmtCRD, mgmt, tpl}
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
	ctx := logf.IntoContext(t.Context(), logr.Discard())
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

		reqs.NotEmpty(props["node.info"])
		cast, ok := props["node.info"].([]map[string]string)
		reqs.True(ok)
		reqs.Len(cast, 2)
		for _, nodeInfo := range cast {
			reqs.Contains(nodeInfo, "gpu.nvidia.bytes")
			reqs.Contains(nodeInfo, "gpu.amd.bytes")
			reqs.Contains(nodeInfo, "os")
			reqs.Contains(nodeInfo, "arch")
			reqs.Equal(strconv.Itoa(2*2*1<<20), nodeInfo["gpu.amd.bytes"])
			reqs.Equal(strconv.Itoa(2*1<<20), nodeInfo["gpu.nvidia.bytes"])
			reqs.Equal("linux", nodeInfo["os"])
			reqs.Equal("amd64", nodeInfo["arch"])
		}
		reqs.Equal(uint64(2*2), props["pods.with_gpu_requests"])
		reqs.Equal(uint64(2*2), props["node.cpu.total"])
		reqs.Equal(uint64(2), props["node.gpu.total"])
		reqs.Equal(uint64(2*1<<20), props["node.memory.bytes"])
		reqs.Equal(uint64(2), props["node.count"])

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
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("1Mi"),
						nvidiaGPUKey:          resource.MustParse("1"),
						amdGPUKey:             resource.MustParse("1"),
					},
					NodeInfo: corev1.NodeSystemInfo{
						OperatingSystem: "linux",
						Architecture:    "amd64",
						KernelVersion:   "5.10.0-test",
						KubeletVersion:  "v1.26.0+test-flavor",
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
								nvidiaGPUKey: resource.MustParse("1Mi"),
								amdGPUKey:    resource.MustParse("2Mi"),
							},
						},
					}},
				},
			},
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod" + itoa + "-0"},
				Spec: corev1.PodSpec{
					NodeName: nodeName,
					Containers: []corev1.Container{{
						Name: "container",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								nvidiaGPUKey: resource.MustParse("1Mi"),
								amdGPUKey:    resource.MustParse("2Mi"),
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

func Test_SegmentIO_Close(t *testing.T) {
	require.NoError(t, (&SegmentIO{segmentCl: &mockSegment{}}).Close(t.Context()))
}
