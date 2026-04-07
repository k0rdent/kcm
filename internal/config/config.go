// Copyright 2026
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

package config

import (
	"os"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const kcmHelmReleaseNameEnvVar = "HELM_RELEASE_NAME"

// KCMHelmReleaseName returns the name of the helm release with core KCM components. The name is expected
// to be provided via the HELM_RELEASE_NAME environment variable.
// If HELM_RELEASE_NAME is not set or is empty, it falls back to the default core KCM release name.
func KCMHelmReleaseName() string {
	releaseName, ok := os.LookupEnv(kcmHelmReleaseNameEnvVar)
	if !ok || releaseName == "" {
		return kcmv1.CoreKCMName
	}
	return releaseName
}
