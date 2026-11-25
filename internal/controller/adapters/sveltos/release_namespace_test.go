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

package sveltos

import (
	"testing"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func TestReleaseNamespace(t *testing.T) {
	for _, tc := range []struct {
		name string
		svc  kcmv1.ServiceWithValues
		want string
	}{
		{
			name: "explicit namespace",
			svc:  kcmv1.ServiceWithValues{Name: "podinfo", Namespace: "apps"},
			want: "apps",
		},
		{
			name: "empty namespace falls back to default, not the service name",
			svc:  kcmv1.ServiceWithValues{Name: "podinfo"},
			want: "default",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseNamespace(tc.svc); got != tc.want {
				t.Errorf("releaseNamespace() = %q, want %q", got, tc.want)
			}
		})
	}
}
