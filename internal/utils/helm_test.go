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

func TestDetermineDefaultRepositoryType(t *testing.T) {
	for _, tc := range []struct {
		url            string
		expectErr      bool
		expectedScheme string
	}{
		{url: "oci://kcm-local-registry:5000/charts", expectErr: false, expectedScheme: "oci"},
		{url: "https://registry.example.com", expectErr: false, expectedScheme: "default"},
		{url: "http://docker.io", expectErr: false, expectedScheme: "default"},
		{url: "ftp://ftp.example.com", expectErr: true},
		{url: "not-a-url", expectErr: true},
	} {
		t.Run(tc.url, func(t *testing.T) {
			actual, err := DetermineDefaultRepositoryType(tc.url)
			if tc.expectErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)

				if actual != tc.expectedScheme {
					t.Errorf("expected scheme %q, got %q", tc.expectedScheme, actual)
				}
			}
		})
	}
}
