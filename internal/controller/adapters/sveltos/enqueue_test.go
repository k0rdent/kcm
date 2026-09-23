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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestEnqueueState_FirstSightEnqueuesAndSeedsRV asserts that a fresh
// enqueueState (empty lastSeenSummaryRV) treats any observed CS RV as a
// change, enqueues, and remembers the RV. This is the correct behavior
// on process restart or newly-created ServiceSet — we have not verified
// anything yet, so an initial verify is warranted.
func TestEnqueueState_FirstSightEnqueuesAndSeedsRV(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	s := &enqueueState{}

	got := s.evaluate(now, "rv-1", false)

	assert.True(t, got, "first sight of any RV must enqueue")
	assert.Equal(t, "rv-1", s.lastSeenSummaryRV, "RV must be recorded")
	assert.Equal(t, enqueueBaseBackoff, s.currentBackoff, "back-off starts at base")
	assert.Equal(t, now.Add(enqueueBaseBackoff), s.nextEligibleTime,
		"next-eligible must be now+base")
}

// TestEnqueueState_CSChangedResetsBackoff asserts that any RV change
// resets an accumulated back-off. Long-running in-flight installs may
// have back-off at max; when sveltos next moves, we want to react
// immediately, not wait out the accumulated interval.
func TestEnqueueState_CSChangedResetsBackoff(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	s := &enqueueState{
		lastSeenSummaryRV: "rv-1",
		currentBackoff:    enqueueMaxBackoff,
		nextEligibleTime:  now.Add(enqueueMaxBackoff),
	}

	got := s.evaluate(now, "rv-2", false)

	assert.True(t, got, "RV change must enqueue")
	assert.Equal(t, "rv-2", s.lastSeenSummaryRV, "new RV must be recorded")
	assert.Equal(t, enqueueBaseBackoff, s.currentBackoff,
		"back-off must reset to base on RV change")
}

// TestEnqueueState_QuiescentSkipsWhenDeployed asserts that a Deployed
// ServiceSet with unchanged CS RV skips entirely, regardless of any
// back-off state.
func TestEnqueueState_QuiescentSkipsWhenDeployed(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	// Back-off long expired — a fixed-interval poller would enqueue here,
	// but Status.Deployed==true and RV unchanged means there is nothing
	// to verify.
	s := &enqueueState{
		lastSeenSummaryRV: "rv-1",
		currentBackoff:    enqueueBaseBackoff,
		nextEligibleTime:  now.Add(-time.Hour),
	}
	priorNext := s.nextEligibleTime
	priorBackoff := s.currentBackoff

	got := s.evaluate(now, "rv-1", true)

	assert.False(t, got, "quiescent SS must skip")
	assert.Equal(t, priorNext, s.nextEligibleTime, "state must not change on skip")
	assert.Equal(t, priorBackoff, s.currentBackoff, "back-off must not change on skip")
}

// TestEnqueueState_InflightBackoffElapsedEnqueuesAndDoubles asserts that
// an in-flight ServiceSet whose back-off window has passed enqueues and
// doubles its back-off — the whole point of the exponential schedule.
func TestEnqueueState_InflightBackoffElapsedEnqueuesAndDoubles(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	s := &enqueueState{
		lastSeenSummaryRV: "rv-1",
		currentBackoff:    30 * time.Second,
		nextEligibleTime:  now.Add(-time.Second), // just elapsed
	}

	got := s.evaluate(now, "rv-1", false)

	assert.True(t, got, "elapsed back-off must enqueue")
	assert.Equal(t, 60*time.Second, s.currentBackoff, "back-off must double")
	assert.Equal(t, now.Add(60*time.Second), s.nextEligibleTime,
		"next-eligible must advance by the new back-off")
}

// TestEnqueueState_InflightWithinWindowSkips asserts that an in-flight
// SS whose back-off window has NOT elapsed is skipped and its state is
// preserved — no needless work on ticks between polls.
func TestEnqueueState_InflightWithinWindowSkips(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	s := &enqueueState{
		lastSeenSummaryRV: "rv-1",
		currentBackoff:    30 * time.Second,
		nextEligibleTime:  now.Add(time.Second), // still in the window
	}
	priorNext := s.nextEligibleTime
	priorBackoff := s.currentBackoff

	got := s.evaluate(now, "rv-1", false)

	assert.False(t, got, "SS within back-off window must skip")
	assert.Equal(t, priorNext, s.nextEligibleTime, "state must not change on skip")
	assert.Equal(t, priorBackoff, s.currentBackoff, "back-off must not change on skip")
}

// TestEnqueueState_BackoffCapsAtMax asserts that repeated in-flight
// doubling eventually hits enqueueMaxBackoff and stops growing.
func TestEnqueueState_BackoffCapsAtMax(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	s := &enqueueState{
		lastSeenSummaryRV: "rv-1",
		currentBackoff:    enqueueMaxBackoff, // already at cap
		nextEligibleTime:  now.Add(-time.Second),
	}

	got := s.evaluate(now, "rv-1", false)

	assert.True(t, got, "elapsed back-off enqueues even at cap")
	assert.Equal(t, enqueueMaxBackoff, s.currentBackoff,
		"back-off must not exceed cap after doubling")
}

// TestEnqueueState_BackoffDoublePastCapClamps asserts that when back-off
// is close to (but not at) the cap, doubling clamps to the cap rather
// than overshooting.
func TestEnqueueState_BackoffDoublePastCapClamps(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	// Deliberately choose a value where 2× exceeds the cap.
	preClamp := enqueueMaxBackoff/2 + time.Second
	require.Greater(t, preClamp*2, enqueueMaxBackoff, "test fixture invariant")

	s := &enqueueState{
		lastSeenSummaryRV: "rv-1",
		currentBackoff:    preClamp,
		nextEligibleTime:  now.Add(-time.Second),
	}

	_ = s.evaluate(now, "rv-1", false)

	assert.Equal(t, enqueueMaxBackoff, s.currentBackoff,
		"doubling past cap must clamp exactly to cap")
}

// TestPruneEnqueueStates_DropsUnseen asserts that pruneEnqueueStates
// removes entries for ServiceSets that were not observed this tick,
// covering the "ServiceSet deleted from cluster" garbage path.
func TestPruneEnqueueStates_DropsUnseen(t *testing.T) {
	// Isolate from any state a parallel test might have populated.
	enqueueStates.Lock()
	original := enqueueStates.entries
	enqueueStates.entries = make(map[client.ObjectKey]*enqueueState)
	enqueueStates.Unlock()
	t.Cleanup(func() {
		enqueueStates.Lock()
		enqueueStates.entries = original
		enqueueStates.Unlock()
	})

	kept := client.ObjectKey{Namespace: "ns-a", Name: "kept"}
	dropped := client.ObjectKey{Namespace: "ns-b", Name: "dropped"}

	enqueueStates.Lock()
	enqueueStates.entries[kept] = &enqueueState{lastSeenSummaryRV: "rv-a"}
	enqueueStates.entries[dropped] = &enqueueState{lastSeenSummaryRV: "rv-b"}
	enqueueStates.Unlock()

	pruneEnqueueStates(map[client.ObjectKey]struct{}{kept: {}})

	enqueueStates.Lock()
	defer enqueueStates.Unlock()
	_, keptOK := enqueueStates.entries[kept]
	_, droppedOK := enqueueStates.entries[dropped]
	assert.True(t, keptOK, "seen entry must survive prune")
	assert.False(t, droppedOK, "unseen entry must be dropped")
}
