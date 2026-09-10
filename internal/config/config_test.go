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
	"testing"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func TestResolveHelmReleaseName(t *testing.T) {
	tests := []struct {
		name  string
		value string
		found bool
		want  string
	}{
		{
			name:  "env var set to a non-empty value",
			value: "custom-release",
			found: true,
			want:  "custom-release",
		},
		{
			name:  "env var set but empty",
			found: true,
			want:  kcmv1.CoreKCMName,
		},
		{
			name: "env var not set",
			want: kcmv1.CoreKCMName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lookedUpKey string
			lookup := func(key string) (string, bool) {
				lookedUpKey = key
				return tt.value, tt.found
			}

			if got := resolveHelmReleaseName(lookup); got != tt.want {
				t.Errorf("resolveHelmReleaseName() = %q, want %q", got, tt.want)
			}
			if lookedUpKey != kcmHelmReleaseNameEnvVar {
				t.Errorf("resolveHelmReleaseName() looked up %q, want %q", lookedUpKey, kcmHelmReleaseNameEnvVar)
			}
		})
	}
}

func TestKCMHelmReleaseName(t *testing.T) {
	// KCMHelmReleaseName memoizes via sync.OnceValue, so this just checks that
	// it returns a stable, non-empty value derived from resolveHelmReleaseName.
	got := KCMHelmReleaseName()
	if got == "" {
		t.Fatal("KCMHelmReleaseName() = \"\", want non-empty")
	}
	if again := KCMHelmReleaseName(); again != got {
		t.Errorf("KCMHelmReleaseName() is not memoized: got %q then %q", got, again)
	}
}
