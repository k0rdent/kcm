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
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_onlineAccumulator_accumulateNode(t *testing.T) {
	t.Parallel()

	const (
		testOS         = "linux"
		testArch       = "amd64"
		testKernel     = "1.0.0-test"
		testKubeVer    = "v1.26.0"
		testKubeFlavor = "test-flavor"
	)

	makeNode := func(name, cpu, mem, nvidia, amd string) *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(cpu),
					corev1.ResourceMemory: resource.MustParse(mem),
					nvidiaGPUKey:          resource.MustParse(nvidia),
					amdGPUKey:             resource.MustParse(amd),
				},
				NodeInfo: corev1.NodeSystemInfo{
					OperatingSystem: testOS,
					Architecture:    testArch,
					KernelVersion:   testKernel,
					KubeletVersion:  testKubeVer + "+" + testKubeFlavor,
				},
			},
		}
	}

	acc := &onlineAccumulator{}
	acc.accumulateNode(makeNode("node1", "2", "4Gi", "2Mi", "0Mi"))
	acc.accumulateNode(makeNode("node2", "2", "4Gi", "0Mi", "2Mi"))
	acc.accumulateNode(makeNode("node3", "2", "4Gi", "0Mi", "0Mi"))

	assert.Equal(t, uint64(6), acc.nodesTotalCPU)
	assert.Equal(t, uint64(12*1<<30), acc.nodesTotalMemory)
	assert.Equal(t, uint64(2), acc.nodesTotalGPU)
	assert.Equal(t, uint64(3), acc.nodesCount)

	assert.Len(t, acc.nodeInfos, 3)
	for _, v := range acc.nodeInfos {
		assert.Equal(t, testOS, v["os"])
		assert.Equal(t, testArch, v["arch"])
		assert.Equal(t, testKernel, v["kernelVersion"])
		assert.Equal(t, testKubeVer, v["kubeVersion"])
		assert.Equal(t, testKubeFlavor, v["kubeFlavor"])

		switch v["name"] {
		case "node1":
			assert.Equal(t, strconv.Itoa(2*1<<20), v["gpu.nvidia.bytes_capacity"])
			assert.Equal(t, "0", v["gpu.amd.bytes_capacity"])
		case "node2":
			assert.Equal(t, "0", v["gpu.nvidia.bytes_capacity"])
			assert.Equal(t, strconv.Itoa(2*1<<20), v["gpu.amd.bytes_capacity"])
		case "node3":
			assert.Equal(t, "0", v["gpu.nvidia.bytes_capacity"])
			assert.Equal(t, "0", v["gpu.amd.bytes_capacity"])
		}
	}
}

func Test_onlineAccumulator_accumulatePodGpu(t *testing.T) {
	t.Parallel()

	pods := make([]*corev1.Pod, 0, 2)
	for range 2 {
		pods = append(pods, &corev1.Pod{
			Spec: corev1.PodSpec{
				NodeName: "node1",
				Containers: []corev1.Container{{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							nvidiaGPUKey: resource.MustParse("1Mi"),
							amdGPUKey:    resource.MustParse("2Mi"),
						},
					},
				}},
			},
		})
	}
	nodeInfo := map[string]string{"name": "node1"}
	acc := &onlineAccumulator{
		nodeInfos:        []map[string]string{nodeInfo},
		nodeName2InfoIdx: map[string]int{"node1": 0},
	}

	for _, pod := range pods {
		acc.accumulatePodGpu(pod)
	}

	assert.Equal(t, uint64(2), acc.podsWithGPUReqs)
	assert.Equal(t, strconv.Itoa(2*1<<20), nodeInfo["gpu.nvidia.bytes"])
	assert.Equal(t, strconv.Itoa(2*2*1<<20), nodeInfo["gpu.amd.bytes"])
}

func Test_localAccumulator_AccumulateNode(t *testing.T) {
	t.Parallel()

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
				nvidiaGPUKey:          resource.MustParse("2Gi"),
				amdGPUKey:             resource.MustParse("0"),
			},
			NodeInfo: corev1.NodeSystemInfo{
				Architecture:    "amd64",
				OperatingSystem: "linux",
				KubeletVersion:  "v1.30.4+flavor",
			},
		},
	}

	acc := newLocalAccumulator()
	acc.accumulateNode(node)

	asserts := assert.New(t)

	asserts.Equal(uint64(1), acc.nodesCount)
	asserts.Equal(uint64(4), acc.nodesTotalCPU)
	asserts.Equal(uint64(8<<30), acc.nodesTotalMemory)
	asserts.Equal(uint64(1), acc.nodesTotalGPU)
	asserts.Equal(uint64(2<<30), acc.nodesTotalNvidiaCapacity)
	asserts.Zero(acc.nodesTotalAMDCapacity)
	asserts.Len(acc.archCount, 1)
	asserts.Equal(uint64(1), acc.archCount["amd64"])
	asserts.Len(acc.osCount, 1)
	asserts.Equal(uint64(1), acc.osCount["linux"])
	asserts.Len(acc.kubeVerCount, 1)
	asserts.Equal(uint64(1), acc.kubeVerCount["1.30"])
}

func Test_localAccumulator_AccumulatePod(t *testing.T) {
	t.Parallel()

	acc := newLocalAccumulator()

	podWithGPU := &corev1.Pod{
		Spec: corev1.PodSpec{
			NodeName: "node1",
			Containers: []corev1.Container{{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						nvidiaGPUKey: resource.MustParse("2Mi"),
						amdGPUKey:    resource.MustParse("1Mi"),
					},
				},
			}},
		},
	}

	podWithoutGPU := &corev1.Pod{
		Spec: corev1.PodSpec{
			NodeName: "node1",
			Containers: []corev1.Container{{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("200m"),
					},
				},
			}},
		},
	}

	acc.accumulatePod(podWithGPU)
	acc.accumulatePod(podWithoutGPU)

	assert.Equal(t, uint64(1), acc.podsWithGPUReqs)
	assert.Equal(t, uint64(2<<20), acc.nodesTotalNvidiaUsed)
	assert.Equal(t, uint64(1<<20), acc.nodesTotalAMDUsed)
}
