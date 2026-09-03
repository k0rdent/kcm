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

package record

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/events"
)

func TestTitle(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"reconcile started", "Reconcile Started"},
		{"ALREADY UPPER", "ALREADY UPPER"},
		{"", ""},
		{"single", "Single"},
	}

	for _, tt := range tests {
		if got := title(tt.in); got != tt.want {
			t.Errorf("title(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestInitFromRecorderAndEventf covers InitFromRecorder, Eventf and Warnf
// together: initOnce is process-global, so InitFromRecorder can only be
// meaningfully set once per test binary, and its no-op-on-second-call
// behavior needs to be observed within that same call.
func TestInitFromRecorderAndEventf(t *testing.T) {
	first := events.NewFakeRecorder(10)
	InitFromRecorder(first)

	second := events.NewFakeRecorder(10)
	InitFromRecorder(second) // should be a no-op, first stays active

	Eventf(nil, nil, "already done", "created thing", "some note %s", "arg1")

	select {
	case msg := <-first.Events:
		want := corev1.EventTypeNormal + " Already Done some note arg1"
		if msg != want {
			t.Errorf("Eventf() message = %q, want %q", msg, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event on first recorder")
	}

	select {
	case msg := <-second.Events:
		t.Fatalf("second recorder received an event, want none (InitFromRecorder should no-op): %q", msg)
	default:
	}

	Warnf(nil, nil, "reconcile failed", "retry scheduled", "warn note %d", 7)

	select {
	case msg := <-first.Events:
		want := corev1.EventTypeWarning + " Reconcile Failed warn note 7"
		if msg != want {
			t.Errorf("Warnf() message = %q, want %q", msg, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for warning event on first recorder")
	}
}
