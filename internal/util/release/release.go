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

package release

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// ReleaseNameFromVersion generates a DNS-compliant release name from a version string.
// It expects a version string (e.g., "v1.2.3") and returns a formatted release name or an error if invalid.
func ReleaseNameFromVersion(version string) (string, error) {
	n := "kcm-" +
		strings.ToLower(
			strings.ReplaceAll(
				strings.ReplaceAll(
					strings.TrimPrefix(version, "v"),
					".", "-"),
				"+", "-"),
		)

	if validationErrors := validation.IsDNS1123Subdomain(n); len(validationErrors) > 0 {
		return "", fmt.Errorf("invalid name: %v", validationErrors)
	}

	return n, nil
}

// TemplatesChartFromReleaseName returns the chart name for templates based on the given release name.
func TemplatesChartFromReleaseName(releaseName string) string {
	return releaseName + "-tpl"
}
