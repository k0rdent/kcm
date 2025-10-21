// Copyright 2025
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

package collector

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_isOnline(t *testing.T) {
	tests := []struct {
		name     string
		s        scope
		expected bool
	}{
		{"scopeManagement (mgmt only)", scopeManagement, true},
		{"scopeRegional (regional only)", scopeRegional, true},
		{"scopeOnline (online only)", scopeOnline, true},
		{"scopeLocal (local only)", scopeLocal, false},
		{"fetcherMgmtOnline", fetcherMgmtOnline, true},
		{"fetcherMgmtLocal", fetcherMgmtLocal, false},
		{"fetcherRegionalOnline", fetcherRegionalOnline, true},
		{"fetcherRegionalLocal", fetcherRegionalLocal, false},
		{"zero scope", scope(0), true},
		{"unknown scope (all bits set)", scope(0xF), false},
		{"unknown scope (random bits)", scope(5), true},   // 0101: mgmt+online
		{"unknown scope (random bits)", scope(10), false}, // 1010: regional+local
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.s.isOnline()
			if got != tt.expected {
				t.Errorf("scope(%d).isOnline() = %v, want %v", tt.s, got, tt.expected)
			}
		})
	}
}

func Test_isMgmt(t *testing.T) {
	tests := []struct {
		name     string
		s        scope
		expected bool
	}{
		{"scopeManagement (mgmt only)", scopeManagement, true},
		{"scopeRegional (regional only)", scopeRegional, false},
		{"scopeOnline (online only)", scopeOnline, true},
		{"scopeLocal (local only)", scopeLocal, true},
		{"fetcherMgmtOnline", fetcherMgmtOnline, true},          // mgmt + online
		{"fetcherMgmtLocal", fetcherMgmtLocal, true},            // mgmt + local
		{"fetcherRegionalOnline", fetcherRegionalOnline, false}, // regional + online
		{"fetcherRegionalLocal", fetcherRegionalLocal, false},   // regional + local
		{"zero scope", scope(0), true},
		{"unknown scope (all bits set)", scope(0xF), false},
		{"unknown scope (random bits 5)", scope(5), true},    // 0101: mgmt+online
		{"unknown scope (random bits 10)", scope(10), false}, // 1010: regional+local
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.s.isMgmt()
			if got != tt.expected {
				t.Errorf("scope(%d).isMgmt() = %v, want %v", tt.s, got, tt.expected)
			}
		})
	}
}

func Test_isMgmtCluster(t *testing.T) {
	tests := []struct {
		name        string
		setupClient func() *fake.ClientBuilder
		expected    bool
	}{
		{
			name: "management CRD exists",
			setupClient: func() *fake.ClientBuilder {
				mgmtCRD := &metav1.PartialObjectMetadata{
					ObjectMeta: metav1.ObjectMeta{
						Name: "managements.k0rdent.mirantis.com",
					},
				}
				mgmtCRD.SetGroupVersionKind(apiextv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"))
				return fake.NewClientBuilder().WithObjects(mgmtCRD)
			},
			expected: true,
		},
		{
			name:        "management CRD does not exist",
			setupClient: fake.NewClientBuilder,
			expected:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, metav1.AddMetaToScheme(scheme))
			require.NoError(t, apiextv1.AddToScheme(scheme))

			cl := tc.setupClient().WithScheme(scheme).Build()

			got, err := isMgmtCluster(context.Background(), cl)
			require.NoError(t, err)
			require.Equal(t, tc.expected, got)
		})
	}
}
