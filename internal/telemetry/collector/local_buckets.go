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
	"strings"
)

var (
	bucketNodeCount = bucketize(
		"node.count:",
		[]uint64{2, 4, 8, 16, 24, 35, 50, 100, 200},
		boundLabel[uint64])

	bucketUserServiceCount = bucketize(
		"userServiceCount:",
		[]int{2, 5, 10, 25, 50},
		boundLabel[int],
	)

	bucketCPUTotal = bucketize(
		"node.cpu.total:",
		generateBounds(2, 2, 1024), // [2,1024]
		boundLabel[uint64],
	)

	bucketMemoryGi = bucketize(
		"node.memory.gibibytes:",
		generateBounds(2<<30, 2, 1024<<30), // [2GiB,1024GiB]
		unitBoundLabel[uint64](1<<30),
	)

	bucketGPUTotal = bucketize(
		"node.gpu.total:",
		generateBounds(2, 2, 256), // [2,256]
		boundLabel[uint64],
	)

	bucketPodsWithGPURequests = bucketize(
		"pods.with_gpu_requests:",
		generateBounds(2, 2, 256), // [2,256]
		boundLabel[uint64],
	)

	bucketKubevirtVMIs = bucketize(
		"kubevirt.vmis:",
		[]int{2, 5, 10, 20, 50},
		boundLabel[int],
	)

	bucketGPUCapacityUnprefixedGi = bucketize( // must be prefixed
		"",
		generateBounds(2<<30, 2, 1024<<30), // [2GiB,1024GiB]
		unitBoundLabel[uint64](1<<30),
	)

	bucketGPUUsedUnprefixedMi = bucketize( // must be prefixed
		"",
		generateBounds(2<<20, 2, 16<<30), // [2MiB, 16GiB]
		unitBoundLabel[uint64](1<<20),
	)
)

func bucketGPUCapacityGi(typ string, bytes uint64) string {
	if bytes == 0 {
		return ""
	}
	return "gpu." + typ + ".gibibytes_capacity:" + bucketGPUCapacityUnprefixedGi(bytes)
}

func bucketGPUUsedMi(typ string, bytes uint64) string {
	if bytes == 0 {
		return ""
	}
	return "gpu." + typ + ".mebibytes:" + bucketGPUUsedUnprefixedMi(bytes)
}

var kubeVersionBuckets = map[string]struct{}{
	"1.29": {},
	"1.30": {},
	"1.31": {},
	"1.32": {},
	"1.33": {},
	"1.34": {},
	"1.35": {},
	"1.36": {},
	"1.37": {},
}

func bucketKubeVersion(v string) string {
	if _, ok := kubeVersionBuckets[v]; ok {
		return "node.info.kubeVersion:" + v
	}
	return "node.info.kubeVersion:other"
}

func bucketOS(osname string) string {
	prefix := "node.info.os:"
	switch v := strings.ToLower(osname); v {
	case "linux", "windows":
		return prefix + v
	default:
		return prefix + "other"
	}
}

func bucketArch(arch string) string {
	prefix := "node.info.arch:"
	switch v := strings.ToLower(arch); v {
	case "amd64", "arm64":
		return prefix + v
	default:
		return prefix + "other"
	}
}

func bucketOperatorInstalled(typ string, v bool) string {
	return "gpu.operator_installed." + typ + ":" + strconv.FormatBool(v)
}

func bucketTemplate(name string) string {
	return "template:" + name
}

// ---

type integerConstraint interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// labeler renders the label for [lo..hi], where either side can be nil.
// If hi == nil, it means "greater than lo".
type labeler[T integerConstraint] func(lo, hi *T) string

// boundLabel produces, for example, "1-2", then "3-4", ..., "X+" labels.
func boundLabel[T integerConstraint](lo, hi *T) string {
	switch {
	case lo == nil && hi != nil:
		return fmt.Sprintf("1-%d", *hi)
	case hi == nil && lo != nil:
		return fmt.Sprintf("%d+", *lo+1)
	default:
		return fmt.Sprintf("%d-%d", *lo+1, *hi)
	}
}

// unitBoundLabel is similar to [boundLabel] but divides ranges by the given unit.
func unitBoundLabel[T integerConstraint](unit T) labeler[T] {
	return func(lo, hi *T) string {
		toUnit := func(x T) T { return x / unit }
		switch {
		case lo == nil && hi != nil:
			return fmt.Sprintf("1-%d", toUnit(*hi))
		case hi == nil && lo != nil:
			return fmt.Sprintf("%d+", toUnit(*lo)+1)
		default:
			return fmt.Sprintf("%d-%d", toUnit(*lo)+1, toUnit(*hi))
		}
	}
}

// bucketize returns a function(prefix+label) for a given set of ascending upper bounds.
// Example: bounds {2,4,8} => labels "1-2","3-4","5-8", final "9+"
func bucketize[T integerConstraint](prefix string, bounds []T, label labeler[T]) func(T) string {
	if len(bounds) == 0 {
		panic("bounds must be non-empty")
	}
	if len(bounds) == 1 && bounds[0] == 0 {
		panic("bounds must not consist of only zero-valued elem")
	}
	for i := 1; i < len(bounds); i++ {
		if bounds[i-1] >= bounds[i] {
			panic(fmt.Sprintf("bounds must be in ascending order, got %v", bounds))
		}
	}

	if bounds[0] == 0 {
		bounds = bounds[1:]
	}

	return func(x T) string {
		if x == 0 { // skip zero-values
			return ""
		}

		var lo *T
		for i := range bounds {
			hi := bounds[i]
			if x <= hi {
				return prefix + label(lo, &hi)
			}
			lo = &hi
		}
		return prefix + label(lo, nil)
	}
}

// generateBounds makes bounds start at 'first', then grow by 'factor' until >= cut.
func generateBounds(first, factor, cut uint64) []uint64 {
	if factor < 2 {
		factor = 2
	}
	if first < 1 {
		first = 1
	}
	var b []uint64
	for x := first; x <= cut; x *= factor {
		b = append(b, x)
		if x > cut/factor { // overflow
			break
		}
	}
	return b
}
