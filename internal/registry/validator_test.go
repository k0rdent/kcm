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

package registry

import (
	"testing"
)

func TestNormalizeRegistryURLForValidation(t *testing.T) {
	tests := []struct {
		name     string
		registry string
		want     string
	}{
		{
			name:     "simple host",
			registry: "registry.example.com",
			want:     "https://registry.example.com",
		},
		{
			name:     "with https prefix",
			registry: "https://registry.example.com",
			want:     "https://registry.example.com",
		},
		{
			name:     "with http prefix",
			registry: "http://registry.example.com",
			want:     "http://registry.example.com",
		},
		{
			name:     "with oci prefix",
			registry: "oci://registry.example.com",
			want:     "https://registry.example.com",
		},
		{
			name:     "with trailing slash",
			registry: "registry.example.com/",
			want:     "https://registry.example.com",
		},
		{
			name:     "with port",
			registry: "registry.example.com:5000",
			want:     "https://registry.example.com:5000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeRegistryURLForValidation(tt.registry)
			if got != tt.want {
				t.Errorf("normalizeRegistryURLForValidation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidator_ClearCache(t *testing.T) {
	v := NewValidator()

	// Add some entries to cache
	v.cache["key1"] = &ValidationResult{Valid: true}
	v.cache["key2"] = &ValidationResult{Valid: false}

	if len(v.cache) != 2 {
		t.Errorf("Expected 2 cache entries, got %d", len(v.cache))
	}

	v.ClearCache()

	if len(v.cache) != 0 {
		t.Errorf("Expected empty cache after clear, got %d entries", len(v.cache))
	}
}

