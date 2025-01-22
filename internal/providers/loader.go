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

package providers

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/K0rdent/kcm/internal/projectroot"
)

const EnvProvidersPathGlob = "PROVIDERS_PATH_GLOB"

func init() {
	var baseDir string

	if testing.Testing() {
		baseDir = projectroot.Path
	}

	providersGlob := cmp.Or(
		os.Getenv(EnvProvidersPathGlob),
		filepath.Join(baseDir, "providers", "*.yml"),
	)

	if err := RegisterProvidersFromGlob(providersGlob); err != nil {
		panic(fmt.Sprintf("failed to register providers: %v", err))
	}
}
