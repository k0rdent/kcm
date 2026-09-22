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

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
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

// TestEnqueueState_QuiescentSkipsWhenSettled asserts that a settled
// ServiceSet (healthy AND stamp converged) with unchanged CS RV skips
// entirely, regardless of any back-off state.
func TestEnqueueState_QuiescentSkipsWhenSettled(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	// Back-off long expired — a fixed-interval poller would enqueue here,
	// but settled on both axes with RV unchanged means there is nothing
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

func TestEnqueueState_StampBehindKeepsPolling(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	s := &enqueueState{
		lastSeenSummaryRV: "rv-1",
		currentBackoff:    30 * time.Second,
		nextEligibleTime:  now.Add(-time.Second),
	}

	got := s.evaluate(now, "rv-1", false)

	assert.True(t, got, "healthy-but-unstamped SS must keep polling, not quiesce")
	assert.Equal(t, 60*time.Second, s.currentBackoff,
		"an unstamped SS follows the in-flight back-off schedule")
	assert.Equal(t, now.Add(60*time.Second), s.nextEligibleTime,
		"next-eligible must advance by the new back-off")
}

func TestEnqueueState_StampBehindThenSettledQuiesces(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	s := &enqueueState{
		lastSeenSummaryRV: "rv-1",
		currentBackoff:    30 * time.Second,
		nextEligibleTime:  now.Add(-time.Second),
	}

	require.True(t, s.evaluate(now, "rv-1", false), "precondition: still polling while unstamped")

	got := s.evaluate(now, "rv-1", true)

	assert.False(t, got, "SS must quiesce once the stamp has converged")
}

// Regression test for the permanent-freeze bug: the poller's "we are done" input
// was Status.Deployed alone, and a ServiceSet can be Deployed while its verifier
// version stamp is still behind. The fixture is the observed lab state.
func TestServiceSetSettled_HealthyButUnstampedIsNotSettled(t *testing.T) {
	const ns = "k0rdent-apis"

	ss := &kcmv1.ServiceSet{
		Spec: kcmv1.ServiceSetSpec{Services: []kcmv1.ServiceWithValues{
			{Name: "kacs-org-api", Namespace: ns, Version: new("1.1.0-rc.1.1")},
		}},
		Status: kcmv1.ServiceSetStatus{
			Deployed: true,
			Services: []kcmv1.ServiceState{
				{Name: "kacs-org-api", Namespace: ns, Version: new("1.0.0"), State: kcmv1.ServiceStateDeployed},
			},
		},
	}

	require.True(t, ss.Status.Deployed, "fixture invariant: health axis reports done")
	assert.False(t, serviceSetSettled(ss),
		"a Deployed SS whose stamp is behind must not be treated as settled")
}

func TestServiceSetSettled_RequiresBothAxes(t *testing.T) {
	const ns = "k0rdent-apis"

	converged := []kcmv1.ServiceState{
		{Name: "svc-a", Namespace: ns, Version: new("1.1.0"), State: kcmv1.ServiceStateDeployed},
	}
	spec := []kcmv1.ServiceWithValues{{Name: "svc-a", Namespace: ns, Version: new("1.1.0")}}

	for _, tc := range []struct {
		name     string
		deployed bool
		obs      []kcmv1.ServiceState
		want     bool
	}{
		{name: "healthy and stamped", deployed: true, obs: converged, want: true},
		{name: "stamped but not healthy", deployed: false, obs: converged, want: false},
		{
			name: "healthy but not stamped", deployed: true,
			obs:  []kcmv1.ServiceState{{Name: "svc-a", Namespace: ns, Version: new("1.0.0")}},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ss := &kcmv1.ServiceSet{
				Spec:   kcmv1.ServiceSetSpec{Services: spec},
				Status: kcmv1.ServiceSetStatus{Deployed: tc.deployed, Services: tc.obs},
			}
			assert.Equal(t, tc.want, serviceSetSettled(ss))
		})
	}
}

func TestStampConverged(t *testing.T) {
	const ns = "k0rdent-apis"

	for _, tc := range []struct {
		name string
		spec []kcmv1.ServiceWithValues
		obs  []kcmv1.ServiceState
		want bool
	}{
		{
			name: "converged",
			spec: []kcmv1.ServiceWithValues{{Name: "svc-a", Namespace: ns, Version: new("1.1.0")}},
			obs:  []kcmv1.ServiceState{{Name: "svc-a", Namespace: ns, Version: new("1.1.0"), State: kcmv1.ServiceStateDeployed}},
			want: true,
		},
		{
			name: "status version behind spec",
			spec: []kcmv1.ServiceWithValues{{Name: "svc-a", Namespace: ns, Version: new("1.1.0")}},
			obs:  []kcmv1.ServiceState{{Name: "svc-a", Namespace: ns, Version: new("1.0.0"), State: kcmv1.ServiceStateDeployed}},
			want: false,
		},
		{
			name: "helm status version never stamped",
			spec: []kcmv1.ServiceWithValues{{Name: "svc-a", Namespace: ns, Version: new("1.1.0")}},
			obs:  []kcmv1.ServiceState{{Name: "svc-a", Namespace: ns, State: kcmv1.ServiceStateDeployed}},
			want: false,
		},
		{
			name: "desired service absent from status",
			spec: []kcmv1.ServiceWithValues{
				{Name: "svc-a", Namespace: ns, Version: new("1.1.0")},
				{Name: "svc-b", Namespace: ns, Version: new("2.0.0")},
			},
			obs:  []kcmv1.ServiceState{{Name: "svc-a", Namespace: ns, Version: new("1.1.0")}},
			want: false,
		},
		{
			name: "stale status entry with no spec counterpart",
			spec: []kcmv1.ServiceWithValues{{Name: "svc-a", Namespace: ns, Version: new("1.1.0")}},
			obs: []kcmv1.ServiceState{
				{Name: "svc-a", Namespace: ns, Version: new("1.1.0")},
				{Name: "svc-gone", Namespace: ns, Version: new("0.9.0")},
			},
			want: true,
		},
		{
			name: "namespace defaulting must still match",
			spec: []kcmv1.ServiceWithValues{{Name: "svc-a", Version: new("1.1.0")}},
			obs:  []kcmv1.ServiceState{{Name: "svc-a", Namespace: "default", Version: new("1.1.0")}},
			want: true,
		},
		{
			name: "no services desired",
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ss := &kcmv1.ServiceSet{
				Spec:   kcmv1.ServiceSetSpec{Services: tc.spec},
				Status: kcmv1.ServiceSetStatus{Services: tc.obs},
			}
			assert.Equal(t, tc.want, stampConverged(ss))
		})
	}
}
