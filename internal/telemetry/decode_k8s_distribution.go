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

package telemetry

import (
	"errors"
	"strings"
)

// DecodeDistribution takes a Kubernetes version string and returns the distribution name (if matched) or "unknown"
// See https://github.com/lensapp/lens/blob/release/v4.1/src/main/cluster-detectors/distribution-detector.ts for the original implementation
func DecodeDistribution(k8sVersion string) (string, error) {
	if k8sVersion == "" {
		return "", errors.New("kubernetes version cannot be empty")
	}

	switch {
	case strings.Contains(strings.ToLower(k8sVersion), "aliyun"):
		return "alibaba", nil
	case strings.Contains(strings.ToLower(k8sVersion), "eks"):
		return "eks", nil
	case strings.Contains(k8sVersion, "gke"):
		return "gke", nil
	case strings.Contains(strings.ToLower(k8sVersion), "iks"):
		return "iks", nil
	case strings.Contains(k8sVersion, "+k3s"):
		return "k3s", nil
	case strings.Contains(k8sVersion, "rancher"):
		return "rke", nil
	case strings.Contains(k8sVersion, "k0s"):
		return "k0s", nil
	case strings.Contains(k8sVersion, "vmware"):
		return "vmware", nil
	case strings.Contains(k8sVersion, "-CCE"):
		return "huawei", nil
	default:
		return "unknown", nil
	}
}
