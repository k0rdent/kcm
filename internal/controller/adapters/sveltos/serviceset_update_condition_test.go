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

package sveltos

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func TestUpdateCondition(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	laterTime := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	existingCondition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "AllGood",
		Message:            "everything is fine",
		ObservedGeneration: 1,
		LastTransitionTime: metav1.NewTime(baseTime),
	}

	tests := []struct {
		name string
		// initial conditions on the ServiceSet (nil means no existing condition)
		initialConditions []metav1.Condition
		generation        int64
		// inputs to updateCondition
		inputCondition metav1.Condition
		newStatus      metav1.ConditionStatus
		newReason      string
		newMessage     string
		transitionTime time.Time
		// expectations
		wantChanged            bool
		wantStatus             metav1.ConditionStatus
		wantReason             string
		wantMessage            string
		wantObservedGeneration int64
		// zero means: don't check LastTransitionTime
		wantLastTransitionTime time.Time
	}{
		{
			name:                   "new condition is added and returns true",
			initialConditions:      nil,
			generation:             1,
			inputCondition:         metav1.Condition{Type: "Ready"},
			newStatus:              metav1.ConditionTrue,
			newReason:              "AllGood",
			newMessage:             "everything is fine",
			transitionTime:         baseTime,
			wantChanged:            true,
			wantStatus:             metav1.ConditionTrue,
			wantReason:             "AllGood",
			wantMessage:            "everything is fine",
			wantObservedGeneration: 1,
		},
		{
			// This is the regression test for the memory leak fix.
			// Previously updateCondition returned the result of
			// apimeta.SetStatusCondition, which returns true whenever
			// ObservedGeneration changes — even if status/reason/message
			// are identical. That caused record.Eventf to fire on every
			// reconcile, flooding the event broadcaster's goroutine pool
			// and eventCache, resulting in an unbounded heap growth.
			name:                   "returns false when only ObservedGeneration changes (regression: memory leak)",
			initialConditions:      []metav1.Condition{existingCondition},
			generation:             2, // bumped generation simulates a spec update
			inputCondition:         existingCondition,
			newStatus:              metav1.ConditionTrue,
			newReason:              "AllGood",
			newMessage:             "everything is fine",
			transitionTime:         baseTime,
			wantChanged:            false,
			wantStatus:             metav1.ConditionTrue,
			wantReason:             "AllGood",
			wantMessage:            "everything is fine",
			wantObservedGeneration: 2, // must still be updated in the stored condition
		},
		{
			name:                   "returns true when status changes",
			initialConditions:      []metav1.Condition{existingCondition},
			generation:             1,
			inputCondition:         existingCondition,
			newStatus:              metav1.ConditionFalse,
			newReason:              "AllGood",
			newMessage:             "everything is fine",
			transitionTime:         laterTime,
			wantChanged:            true,
			wantStatus:             metav1.ConditionFalse,
			wantReason:             "AllGood",
			wantMessage:            "everything is fine",
			wantObservedGeneration: 1,
			wantLastTransitionTime: laterTime,
		},
		{
			name:                   "returns true when reason changes",
			initialConditions:      []metav1.Condition{existingCondition},
			generation:             1,
			inputCondition:         existingCondition,
			newStatus:              metav1.ConditionTrue,
			newReason:              "NewReason",
			newMessage:             "everything is fine",
			transitionTime:         laterTime,
			wantChanged:            true,
			wantStatus:             metav1.ConditionTrue,
			wantReason:             "NewReason",
			wantMessage:            "everything is fine",
			wantObservedGeneration: 1,
			// apimeta.SetStatusCondition only updates LastTransitionTime when
			// Status changes; reason/message changes do not move the timestamp.
			wantLastTransitionTime: baseTime,
		},
		{
			name:                   "returns true when message changes",
			initialConditions:      []metav1.Condition{existingCondition},
			generation:             1,
			inputCondition:         existingCondition,
			newStatus:              metav1.ConditionTrue,
			newReason:              "AllGood",
			newMessage:             "updated message",
			transitionTime:         laterTime,
			wantChanged:            true,
			wantStatus:             metav1.ConditionTrue,
			wantReason:             "AllGood",
			wantMessage:            "updated message",
			wantObservedGeneration: 1,
			// apimeta.SetStatusCondition only updates LastTransitionTime when
			// Status changes; reason/message changes do not move the timestamp.
			wantLastTransitionTime: baseTime,
		},
		{
			name:                   "LastTransitionTime is not updated when nothing meaningful changes",
			initialConditions:      []metav1.Condition{existingCondition},
			generation:             3,
			inputCondition:         existingCondition,
			newStatus:              metav1.ConditionTrue,
			newReason:              "AllGood",
			newMessage:             "everything is fine",
			transitionTime:         laterTime, // different time, but should be ignored
			wantChanged:            false,
			wantStatus:             metav1.ConditionTrue,
			wantReason:             "AllGood",
			wantMessage:            "everything is fine",
			wantObservedGeneration: 3,
			wantLastTransitionTime: baseTime, // must remain unchanged
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ss := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-serviceset",
					Generation: tc.generation,
				},
			}
			if tc.initialConditions != nil {
				ss.Status.Conditions = append([]metav1.Condition(nil), tc.initialConditions...)
			}

			got := updateCondition(ss, tc.inputCondition, tc.newStatus, tc.newReason, tc.newMessage, tc.transitionTime)

			if got != tc.wantChanged {
				t.Errorf("updateCondition() changed = %v, want %v", got, tc.wantChanged)
			}

			if len(ss.Status.Conditions) == 0 {
				t.Fatal("expected at least one condition in ServiceSet status after updateCondition")
			}
			cond := ss.Status.Conditions[0]

			if cond.Status != tc.wantStatus {
				t.Errorf("condition.Status = %q, want %q", cond.Status, tc.wantStatus)
			}
			if cond.Reason != tc.wantReason {
				t.Errorf("condition.Reason = %q, want %q", cond.Reason, tc.wantReason)
			}
			if cond.Message != tc.wantMessage {
				t.Errorf("condition.Message = %q, want %q", cond.Message, tc.wantMessage)
			}
			if cond.ObservedGeneration != tc.wantObservedGeneration {
				t.Errorf("condition.ObservedGeneration = %d, want %d", cond.ObservedGeneration, tc.wantObservedGeneration)
			}
			if !tc.wantLastTransitionTime.IsZero() && !cond.LastTransitionTime.Time.Equal(tc.wantLastTransitionTime) {
				t.Errorf("condition.LastTransitionTime = %v, want %v", cond.LastTransitionTime.Time, tc.wantLastTransitionTime)
			}
		})
	}
}
