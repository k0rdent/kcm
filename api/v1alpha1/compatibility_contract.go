// Copyright 2024
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

package v1alpha1

import (
	"strconv"
	"strings"
)

// isCAPIContractVersion determines whether a given string
// represents a version in the CAPI contract version format (e.g. v1_v1beta1_v1alpha1, etc.).
func isCAPIContractVersion(version string) bool {
	for _, v := range strings.Split(version, "_") {
		if !isCAPIContractSingleVersion(v) {
			return false
		}
	}

	return true
}

// isCAPIContractSingleVersion determines whether a given string
// represents a single version in the CAPI contract version format (e.g. v1, v1beta1, v1alpha1, etc.).
func isCAPIContractSingleVersion(version string) bool {
	if !strings.HasPrefix(version, "v") {
		return false
	}

	parts := strings.Split(version, "v")
	if len(parts) != 2 || parts[0] != "" || strings.IndexByte(version, '_') != -1 { // skip v1_v1beta1 list of versions
		return false
	}

	const (
		alphaPrefix, betaPrefix = "alpha", "beta"
	)

	versionNumber := parts[1]
	alphaIndex := strings.Index(versionNumber, alphaPrefix)
	betaIndex := strings.Index(versionNumber, betaPrefix)

	if alphaIndex != -1 {
		return isNonMajor(versionNumber, alphaPrefix, alphaIndex)
	} else if betaIndex != -1 {
		return isNonMajor(versionNumber, betaPrefix, betaIndex)
	}

	_, err := strconv.Atoi(strings.TrimSpace(versionNumber))
	return err == nil
}

// isNonMajor checks is a given version with "alpha" or "beta" version prefix
// is a CAPI non-major version. Expects only ASCII chars, otherwise will return false (expected).
func isNonMajor(version, prefix string, prefixIdx int) bool {
	majorVer := version[:prefixIdx]
	prefixedVer := version[prefixIdx+len(prefix):]

	if _, err := strconv.Atoi(majorVer); err != nil {
		return false
	}

	_, err := strconv.Atoi(prefixedVer)
	return err == nil
}
