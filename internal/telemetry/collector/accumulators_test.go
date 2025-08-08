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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_onlineAccumulator_accumulateNode(t *testing.T) {
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
					OperatingSystem: "linux",
					Architecture:    "amd64",
					KernelVersion:   "5.10.0-test",
					KubeletVersion:  "v1.26.0+test-flavor",
				},
			},
		}
	}

	acc := &onlineAccumulator{}
	acc.accumulateNode(makeNode("node1", 2, 4, 2, 0))
	acc.accumulateNode(makeNode("node2", 2, 4, 0, 2))
	acc.accumulateNode(makeNode("node3", 2, 4, 0, 0))

	assert.Equal(t, uint64(6), acc.totalCPU)
	assert.Equal(t, uint64(12), acc.totalMemory)
	assert.Equal(t, uint64(2), acc.totalGPUNodes)
	assert.Equal(t, uint64(3), acc.count)

	expectedNode := makeNode("test", 0, 0, 0, 0)
	kubeFlavor, kubeVersion := "test-flavor", "v1.26.0"

	assert.Len(t, acc.nodeInfos, 3)
	for _, v := range acc.nodeInfos {
		assert.Equal(t, v["os"], expectedNode.Status.NodeInfo.OperatingSystem)
		assert.Equal(t, v["arch"], expectedNode.Status.NodeInfo.Architecture)
		assert.Equal(t, v["kernelVersion"], expectedNode.Status.NodeInfo.KernelVersion)
		assert.Equal(t, v["kubeVersion"], kubeVersion)
		assert.Equal(t, v["kubeFlavor"], kubeFlavor)
		// nodes gpu capacity implicitly has been already checked
	}
}

func Test_onlineAccumulator_accumulatePodGpu(t *testing.T) {
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
	acc := &onlineAccumulator{
		nodeInfos:        []map[string]string{nodeInfo},
		nodeName2InfoIdx: map[string]int{"node1": 0},
	}
	acc.accumulatePodGpu(pod)

	assert.Equal(t, uint64(1), acc.podsWithGPUReqs)
	assert.Equal(t, "1", nodeInfo["gpu.nvidia.bytes"])
	assert.Equal(t, "2", nodeInfo["gpu.amd.bytes"])
}
