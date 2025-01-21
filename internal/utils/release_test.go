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

package utils

import (
	"testing"
)

func TestReleaseNameFromVersion(t *testing.T) {
	for _, tc := range []struct {
		version      string
		expectedName string
	}{
		{version: "v0.0.1", expectedName: "kcm-0-0-1"},
		{version: "v0.0.1-rc", expectedName: "kcm-0-0-1-rc"},
		{version: "0.0.1", expectedName: "kcm-0-0-1"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			actual := ReleaseNameFromVersion(tc.version)
			if actual != tc.expectedName {
				t.Errorf("expected name %s, got %s", tc.expectedName, actual)
			}
		})
	}
}
