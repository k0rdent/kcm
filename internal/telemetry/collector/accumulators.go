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

	"github.com/Masterminds/semver/v3"
	corev1 "k8s.io/api/core/v1"

	"github.com/K0rdent/kcm/internal/utils/pointer"
)

const (
	amdGPUKey    corev1.ResourceName = "amd.com/gpu"
	nvidiaGPUKey corev1.ResourceName = "nvidia.com/gpu"
)

type localAccumulator struct {
	kubeVerCount map[string]uint64
	archCount    map[string]uint64
	osCount      map[string]uint64

	nodesCount uint64

	nodesTotalCPU, nodesTotalMemory uint64

	nodesTotalGPUNodes                                   uint64
	nodesTotalAMDGiBCapacity, nodesTotalAMDGiBUsed       uint64
	nodesTotalNvidiaGiBCapacity, nodesTotalNvidiaGiBUsed uint64

	podsWithGPUReqs uint64
}

func newLocalAccumulator() *localAccumulator {
	return &localAccumulator{
		kubeVerCount: make(map[string]uint64),
		archCount:    make(map[string]uint64),
		osCount:      make(map[string]uint64),
	}
}

func (acc *localAccumulator) accumulateNode(node *corev1.Node) {
	acc.nodesCount++
	acc.nodesTotalCPU += uint64(node.Status.Capacity.Cpu().MilliValue() / 1000)
	acc.nodesTotalMemory += uint64(node.Status.Capacity.Memory().Value())

	gpusNC := pointer.To(node.Status.Capacity[nvidiaGPUKey]).Value()
	gpusAC := pointer.To(node.Status.Capacity[amdGPUKey]).Value()
	if gpusNC > 0 || gpusAC > 0 {
		acc.nodesTotalGPUNodes++
	}

	acc.nodesTotalAMDGiBCapacity += uint64(gpusAC)
	acc.nodesTotalNvidiaGiBCapacity += uint64(gpusNC)

	info := node.Status.NodeInfo
	acc.archCount[info.Architecture]++
	acc.osCount[info.OperatingSystem]++

	ver, err := semver.NewVersion(info.KubeletVersion)
	if err == nil {
		acc.kubeVerCount[fmt.Sprintf("v%d.%d", ver.Major(), ver.Minor())]++
	}
}

func (acc *localAccumulator) accumulatePod(pod *corev1.Pod) {
	gpuRequested := false
	var nvidia, amd int64

	for _, c := range pod.Spec.Containers {
		nReq := pointer.To(c.Resources.Requests[nvidiaGPUKey]).Value()
		aReq := pointer.To(c.Resources.Requests[amdGPUKey]).Value()
		if nReq > 0 || aReq > 0 {
			gpuRequested = true
			nvidia += nReq
			amd += aReq
		}
	}

	if !gpuRequested {
		return
	}

	acc.podsWithGPUReqs++
	acc.nodesTotalNvidiaGiBUsed += uint64(nvidia)
	acc.nodesTotalAMDGiBUsed += uint64(amd)
}

type onlineAccumulator struct {
	nodeName2InfoIdx map[string]int
	nodeInfos        []map[string]string
	totalCPU         uint64
	totalMemory      uint64
	totalGPUNodes    uint64
	count            uint64
	podsWithGPUReqs  uint64
}

func newOnlineAccumulator() *onlineAccumulator {
	return &onlineAccumulator{nodeName2InfoIdx: make(map[string]int)}
}

func (acc *onlineAccumulator) accumulateNode(node *corev1.Node) {
	acc.count++
	acc.totalCPU += uint64(node.Status.Capacity.Cpu().MilliValue() / 1000)
	acc.totalMemory += uint64(node.Status.Capacity.Memory().Value())

	gpusNC := pointer.To(node.Status.Capacity[nvidiaGPUKey]).Value()
	gpusAC := pointer.To(node.Status.Capacity[amdGPUKey]).Value()
	if gpusNC > 0 || gpusAC > 0 {
		acc.totalGPUNodes++
	}

	info := node.Status.NodeInfo

	nodeInfo := map[string]string{
		"name":                      node.Name,
		"os":                        info.OperatingSystem,
		"arch":                      info.Architecture,
		"kernelVersion":             info.KernelVersion,
		"gpu.amd.bytes_capacity":    strconv.FormatInt(gpusAC, 10),
		"gpu.nvidia.bytes_capacity": strconv.FormatInt(gpusNC, 10),
	}

	nodeInfo["kubeVersion"], nodeInfo["kubeFlavor"] = getKubeVersionAndFlavor(info)

	acc.nodeInfos = append(acc.nodeInfos, nodeInfo)
}

func (acc *onlineAccumulator) accumulatePodGpu(pod *corev1.Pod) {
	gpuRequested := false
	var nvidia, amd int64

	for _, c := range pod.Spec.Containers {
		nReq := pointer.To(c.Resources.Requests[nvidiaGPUKey]).Value()
		aReq := pointer.To(c.Resources.Requests[amdGPUKey]).Value()
		if nReq > 0 || aReq > 0 {
			gpuRequested = true
			nvidia += nReq
			amd += aReq
		}
	}

	if !gpuRequested {
		return
	}

	acc.podsWithGPUReqs++
	if idx, ok := acc.nodeName2InfoIdx[pod.Spec.NodeName]; ok {
		(acc.nodeInfos)[idx]["gpu.amd.bytes"] = strconv.FormatInt(amd, 10)
		(acc.nodeInfos)[idx]["gpu.nvidia.bytes"] = strconv.FormatInt(nvidia, 10)
	}
}
