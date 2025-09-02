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

package utils_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/utils"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("failed to build scheme: %v", err)
	}
	return s
}

func TestAddKCMComponentLabel(t *testing.T) {
	t.Parallel()

	scheme := newScheme(t)
	ctx := t.Context()

	type verifyFn func(t *testing.T, labels map[string]string)

	tests := []struct {
		name          string
		initialLabels map[string]string
		seedObject    bool
		expectUpdated bool
		expectErr     bool
		verify        verifyFn
	}{
		{
			name:          "adds label when missing",
			initialLabels: map[string]string{"keep": "me"},
			seedObject:    true,
			expectUpdated: true,
			verify: func(t *testing.T, l map[string]string) {
				t.Helper()
				if l[kcmv1.GenericComponentNameLabel] != kcmv1.GenericComponentLabelValueKCM {
					t.Fatalf("label not set, got %q", l[kcmv1.GenericComponentNameLabel])
				}
				if l["keep"] != "me" {
					t.Fatalf("existing labels not preserved: %+v", l)
				}
			},
		},
		{
			name: "no-op when already correct",
			initialLabels: map[string]string{
				kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM,
				"keep":                          "me",
			},
			seedObject:    true,
			expectUpdated: false,
			verify: func(t *testing.T, l map[string]string) {
				t.Helper()
				if l["keep"] != "me" {
					t.Fatalf("existing labels modified: %+v", l)
				}
				if l[kcmv1.GenericComponentNameLabel] != kcmv1.GenericComponentLabelValueKCM {
					t.Fatalf("desired label value changed: %+v", l)
				}
			},
		},
		{
			name: "updates when different value",
			initialLabels: map[string]string{
				kcmv1.GenericComponentNameLabel: "wrong",
				"keep":                          "me",
			},
			seedObject:    true,
			expectUpdated: true,
			verify: func(t *testing.T, l map[string]string) {
				t.Helper()
				if l[kcmv1.GenericComponentNameLabel] != kcmv1.GenericComponentLabelValueKCM {
					t.Fatalf("label not corrected, got %q", l[kcmv1.GenericComponentNameLabel])
				}
				if l["keep"] != "me" {
					t.Fatalf("existing labels not preserved: %+v", l)
				}
			},
		},
		{
			name:          "returns error on patch failure",
			initialLabels: map[string]string{},
			seedObject:    false,
			expectUpdated: false,
			expectErr:     true,
			verify:        nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-cm",
					Labels:    tc.initialLabels,
				},
				Data: map[string]string{"foo": "bar"},
			}

			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tc.seedObject {
				builder = builder.WithObjects(cm.DeepCopy())
			}
			cl := builder.Build()

			updated, err := utils.AddKCMComponentLabel(ctx, cl, cm)

			if tc.expectErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.expectErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if updated != tc.expectUpdated {
				t.Fatalf("labelsUpdated mismatch: expected %v, got %v", tc.expectUpdated, updated)
			}

			if !tc.expectErr && tc.verify != nil {
				var got corev1.ConfigMap
				if err := cl.Get(ctx, client.ObjectKeyFromObject(cm), &got); err != nil {
					t.Fatalf("get failed: %v", err)
				}
				tc.verify(t, got.GetLabels())
			}
		})
	}
}
