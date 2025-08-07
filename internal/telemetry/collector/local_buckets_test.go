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
	"testing"

	"github.com/stretchr/testify/require"
)

func Example_bucketize() {
	fmt.Printf("bucketize(\"test:\", []int{2, 5, 10, 100}, boundLabel)(30): %v\n", bucketize("test:", []int{2, 5, 10, 100}, boundLabel)(30))
	fmt.Printf("bucketize(\"test:\", []int{2, 5, 10, 100}, boundLabel)(200): %v\n", bucketize("test:", []int{2, 5, 10, 100}, boundLabel)(200))
	fmt.Printf("bucketize(\"test:\", []int{2, 5, 10, 100}, boundLabel)(1): %v\n", bucketize("test:", []int{2, 5, 10, 100}, boundLabel)(1))
	fmt.Printf("bucketize(\"test:\", []int{2, 5, 10, 100}, boundLabel)(0): %v\n", bucketize("test:", []int{2, 5, 10, 100}, boundLabel)(0))

	// Output:
	// bucketize("test:", []int{2, 5, 10, 100}, boundLabel)(30): test:11-100
	// bucketize("test:", []int{2, 5, 10, 100}, boundLabel)(200): test:101+
	// bucketize("test:", []int{2, 5, 10, 100}, boundLabel)(1): test:1-2
	// bucketize("test:", []int{2, 5, 10, 100}, boundLabel)(0):
}

func Example_generateBounds() {
	fmt.Printf("generateBounds(2, 2, 200): %v\n", generateBounds(2, 2, 200))
	fmt.Printf("generateBounds(2, 2, 256): %v\n", generateBounds(2, 2, 256))
	fmt.Printf("generateBounds(2, 1, 200): %v\n", generateBounds(2, 1, 200))
	fmt.Printf("generateBounds(0, 1, 4): %v\n", generateBounds(0, 1, 4))
	fmt.Printf("generateBounds(1, 5, 1e3): %v\n", generateBounds(1, 5, 1e3))

	gibs := generateBounds(16<<30, 2, 1024<<30)
	gibss := make([]string, len(gibs))
	for i, v := range gibs {
		gibss[i] = fmt.Sprintf("%dGiB", v>>30)
	}
	fmt.Printf("generateBounds(16<<30, 2, 1024<<30) pretty GiB: %v\n", gibss)

	// Output:
	// generateBounds(2, 2, 200): [2 4 8 16 32 64 128]
	// generateBounds(2, 2, 256): [2 4 8 16 32 64 128 256]
	// generateBounds(2, 1, 200): [2 4 8 16 32 64 128]
	// generateBounds(0, 1, 4): [1 2 4]
	// generateBounds(1, 5, 1e3): [1 5 25 125 625]
	// generateBounds(16<<30, 2, 1024<<30) pretty GiB: [16GiB 32GiB 64GiB 128GiB 256GiB 512GiB 1024GiB]
}

func Test_generateBounds(t *testing.T) {
	tests := []struct {
		name             string
		first, fact, cut uint64
	}{
		{"normal", 2, 2, 16},
		{"factor coerced", 2, 1, 8},
		{"first elem coerced", 0, 2, 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateBounds(tt.first, tt.fact, tt.cut)
			require.NotEmpty(t, got)
			require.IsIncreasing(t, got)
		})
	}
}

func Test_boundLabels(t *testing.T) {
	t.Parallel()

	var (
		two  uint64 = 2
		four uint64 = 4
	)
	reqs := require.New(t)
	reqs.Equal("1-2", boundLabel(nil, &two))
	reqs.Equal("3-4", boundLabel(&two, &four))
	reqs.Equal("5+", boundLabel(&four, nil))

	ul := unitBoundLabel[uint64](1 << 30)
	sixteen := uint64(16 << 30)
	thirtytwo := uint64(32 << 30)
	reqs.Equal("1-16", ul(nil, &sixteen))
	reqs.Equal("17-32", ul(&sixteen, &thirtytwo))
	reqs.Equal("33+", ul(&thirtytwo, nil))
}

func Test_bucketizePanics(t *testing.T) {
	t.Parallel()

	reqs := require.New(t)
	reqs.PanicsWithValue("bounds must be non-empty", func() { bucketize("x:", []uint64{}, boundLabel[uint64]) }, "should panic if empty slice")
	reqs.PanicsWithValue("bounds must not consist of only zero-valued elem", func() { bucketize("x:", []uint64{0}, boundLabel[uint64]) })
	reqs.Panics(func() { bucketize("x:", []uint64{4, 2, 8}, boundLabel[uint64]) }, "should panic on unsorted slice")
	reqs.NotPanics(func() { bucketize("x:", []uint64{0, 1}, boundLabel[uint64]) })
}

func Test_buckets(t *testing.T) {
	t.Parallel()

	reqs := require.New(t)
	reqs.Empty(bucketNodeCount(0))
	reqs.Empty(bucketPodsWithGPURequests(0))
	reqs.Empty(bucketGPUTotal(0))
	reqs.Empty(bucketGPUCapacityGi("nvidia", 0))
	reqs.Empty(bucketGPUUsedMi("amd", 0))

	reqs.Equal("node.count:1-2", bucketNodeCount(1))
	reqs.Equal("node.cpu.total:5-8", bucketCPUTotal(8))
	reqs.Equal("userServiceCount:6-10", bucketUserServiceCount(8))
	reqs.Equal("node.memory.gibibytes:17-32", bucketMemoryGi(32<<30))
	reqs.Equal("node.gpu.total:3-4", bucketGPUTotal(3))
	reqs.Equal("pods.with_gpu_requests:5-8", bucketPodsWithGPURequests(5))
	reqs.Equal("kubevirt.vmis:6-10", bucketKubevirtVMIs(7))
	reqs.Equal("gpu.nvidia.gibibytes_capacity:33-64", bucketGPUCapacityGi("nvidia", 64<<30))
	reqs.Equal("gpu.amd.mebibytes:257-512", bucketGPUUsedMi("amd", 512<<20))

	reqs.Equal("node.info.kubeVersion:1.30", bucketKubeVersion("1.30"))
	reqs.Equal("node.info.kubeVersion:other", bucketKubeVersion("1.30.5")) // patch should not happen
	reqs.Equal("node.info.os:linux", bucketOS("Linux"))
	reqs.Equal("node.info.os:other", bucketOS("macos"))
	reqs.Equal("node.info.arch:arm64", bucketArch("arm64"))
	reqs.Equal("node.info.arch:other", bucketArch("loongarch64"))
	reqs.Equal("gpu.operator_installed.nvidia:true", bucketOperatorInstalled("nvidia", true))
	reqs.Equal("syncMode:continuous", bucketSyncMode("CoNtInUoUs"))
	reqs.Equal("template:aws-foo", bucketTemplate("aws-foo"))
}
