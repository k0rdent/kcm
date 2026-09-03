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

package status

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func newUnstructured(kind, name string, obj map[string]any) *unstructured.Unstructured {
	if obj == nil {
		obj = map[string]any{}
	}
	obj["kind"] = kind
	obj["metadata"] = map[string]any{"name": name}
	return &unstructured.Unstructured{Object: obj}
}

func TestObjKindName(t *testing.T) {
	u := newUnstructured("SomeKind", "some-name", nil)

	kind, name := ObjKindName(u)
	if kind != "SomeKind" {
		t.Errorf("first return = %q, want kind %q", kind, "SomeKind")
	}
	if name != "some-name" {
		t.Errorf("second return = %q, want name %q", name, "some-name")
	}
}

func TestConditionsFromUnstructured(t *testing.T) {
	t.Run("no status.conditions field returns error", func(t *testing.T) {
		u := newUnstructured("Foo", "bar", nil)

		_, err := ConditionsFromUnstructured(u)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "no status conditions found for") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("status.conditions of the wrong type returns error", func(t *testing.T) {
		u := newUnstructured("Foo", "bar", map[string]any{
			"status": map[string]any{
				"conditions": "not-a-slice",
			},
		})

		_, err := ConditionsFromUnstructured(u)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to get status conditions for") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("condition element not a map returns error", func(t *testing.T) {
		u := newUnstructured("Foo", "bar", map[string]any{
			"status": map[string]any{
				"conditions": []any{"not-a-map"},
			},
		})

		_, err := ConditionsFromUnstructured(u)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "expected") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("condition failing conversion returns error", func(t *testing.T) {
		u := newUnstructured("Foo", "bar", map[string]any{
			"status": map[string]any{
				"conditions": []any{
					map[string]any{
						"type":               "Ready",
						"status":             true, // metav1.Condition.Status is a string; this should fail conversion
						"lastTransitionTime": nil,
						"reason":             "SomeReason",
					},
				},
			},
		})

		_, err := ConditionsFromUnstructured(u)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to convert condition map to metav1.Condition") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid conditions are converted and message is prefixed with object name", func(t *testing.T) {
		u := newUnstructured("Foo", "bar", map[string]any{
			"status": map[string]any{
				"conditions": []any{
					map[string]any{
						"type":               "Ready",
						"status":             string(metav1.ConditionTrue),
						"lastTransitionTime": "2024-01-01T00:00:00Z",
						"reason":             "AllGood",
						"message":            "everything is fine",
					},
					map[string]any{
						"type":               "Available",
						"status":             string(metav1.ConditionFalse),
						"lastTransitionTime": "2024-01-01T00:00:00Z",
						"reason":             "NotYet",
					},
				},
			},
		})

		conditions, err := ConditionsFromUnstructured(u)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(conditions) != 2 {
			t.Fatalf("got %d conditions, want 2", len(conditions))
		}

		if conditions[0].Type != "Ready" || conditions[0].Status != metav1.ConditionTrue {
			t.Errorf("conditions[0] = %+v", conditions[0])
		}
		if want := "bar: everything is fine"; conditions[0].Message != want {
			t.Errorf("conditions[0].Message = %q, want %q", conditions[0].Message, want)
		}

		if conditions[1].Type != "Available" || conditions[1].Status != metav1.ConditionFalse {
			t.Errorf("conditions[1] = %+v", conditions[1])
		}
		if want := "bar"; conditions[1].Message != want {
			t.Errorf("conditions[1].Message (no original message) = %q, want %q", conditions[1].Message, want)
		}
	})
}
