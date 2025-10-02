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
		{version: "1.2.3-alpha.4", expectedName: "kcm-1-2-3-alpha-4"},
		{version: "1.2.3+meta", expectedName: "kcm-1-2-3-meta"},
		{version: "1.2.3-rc.4+build.5", expectedName: "kcm-1-2-3-rc-4-build-5"},
		{version: "v00.01.02", expectedName: "kcm-00-01-02"},
		{version: "v1", expectedName: "kcm-1"},
		{version: "v2.3", expectedName: "kcm-2-3"},
		{version: "v4.5.6", expectedName: "kcm-4-5-6"},
		{version: "v1.2.3-rc.4", expectedName: "kcm-1-2-3-rc-4"},
		{version: "v1.2.3.4.5", expectedName: "kcm-1-2-3-4-5"},
		{version: "v123", expectedName: "kcm-123"},
		{version: "0.0.0", expectedName: "kcm-0-0-0"},
		{version: "9999.9999.9999", expectedName: "kcm-9999-9999-9999"},
		{version: "1.2.3-RC.4", expectedName: "kcm-1-2-3-rc-4"},
		{version: "v0.1.0-26-g6d786ca", expectedName: "kcm-0-1-0-26-g6d786ca"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			actual, err := ReleaseNameFromVersion(tc.version)
			if err != nil {
				t.Errorf("expected no error, got %v", err)
			}
			if actual != tc.expectedName {
				t.Errorf("expected name %s, got %s", tc.expectedName, actual)
			}
		})
	}
}
