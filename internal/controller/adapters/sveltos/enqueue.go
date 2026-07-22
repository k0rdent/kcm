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
	"context"
	"fmt"
	"sync"
	"time"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	pollerutil "github.com/K0rdent/kcm/internal/util/poller"
)

const (
	// enqueueBaseBackoff is the initial polling interval for a ServiceSet
	// that is in flight (Status.Deployed == false). Also the reset target
	// on any observed ClusterSummary ResourceVersion change.
	enqueueBaseBackoff = 10 * time.Second

	// enqueueMaxBackoff caps the in-flight back-off. Beyond this, we stop
	// slowing further — a genuinely stuck install still gets polled every
	// enqueueMaxBackoff so the verifier's diagnostic conditions stay fresh.
	enqueueMaxBackoff = 5 * time.Minute
)

// enqueueState tracks per-ServiceSet polling metadata across ticks. Its
// dual purpose: (1) detect ClusterSummary-side changes cheaply via
// ResourceVersion comparison, and (2) implement exponential back-off on
// ServiceSets that are still in flight, without hammering the workload
// cluster on every tick.
//
// Field ordering: time.Time first (its embedded *Location has an internal
// pointer offset that fieldalignment sees), then string, then int64.
type enqueueState struct {
	nextEligibleTime  time.Time
	lastSeenSummaryRV string
	currentBackoff    time.Duration
}

// enqueueStates holds live enqueueState values keyed by ServiceSet
// (namespace, name). Entries are created on first observation and pruned
// once a ServiceSet disappears from the poller's List.
var enqueueStates = struct {
	entries map[client.ObjectKey]*enqueueState
	sync.Mutex
}{entries: make(map[client.ObjectKey]*enqueueState)}

// evaluate is the per-tick state-machine decision for a single ServiceSet.
// It updates the receiver in place and returns whether the ServiceSet
// should be enqueued this tick.
//
// Extracted from enqueueClusterSummary so unit tests can exercise the
// state transitions directly with an injectable clock, without spinning
// up envtest. `deployed` is a state-machine axis (the "we are done"
// arm), not a control-coupling flag.
//
//nolint:revive // deployed is an input axis of the state machine, not a caller-driven switch
func (s *enqueueState) evaluate(now time.Time, summaryRV string, deployed bool) bool {
	if s.lastSeenSummaryRV != summaryRV {
		// A real sveltos-side change since we last looked. Enqueue,
		// reset back-off, remember the new RV.
		s.lastSeenSummaryRV = summaryRV
		s.currentBackoff = enqueueBaseBackoff
		s.nextEligibleTime = now.Add(s.currentBackoff)
		return true
	}
	if deployed {
		// Quiescent — sveltos unchanged AND every service confirmed
		// Deployed by the verifier. No health controller exists in this
		// codebase, so ongoing pod-death detection is out of scope; we
		// unquiesce automatically when sveltos next bumps CS RV.
		return false
	}
	if now.Before(s.nextEligibleTime) {
		// In-flight but still within the back-off window from the last
		// fruitless enqueue.
		return false
	}
	// In-flight and the back-off window has elapsed — enqueue and
	// double the back-off up to the cap.
	s.currentBackoff *= 2
	if s.currentBackoff > enqueueMaxBackoff {
		s.currentBackoff = enqueueMaxBackoff
	}
	s.nextEligibleTime = now.Add(s.currentBackoff)
	return true
}

// loadOrCreateEnqueueState returns the state for key, creating a fresh
// one on first sight. Fresh state has an empty lastSeenSummaryRV so the
// first observed CS RV triggers the "change detected" arm and enqueues
// once — the correct behavior on process restart or newly created
// ServiceSet.
func loadOrCreateEnqueueState(key client.ObjectKey) *enqueueState {
	enqueueStates.Lock()
	defer enqueueStates.Unlock()
	s, ok := enqueueStates.entries[key]
	if !ok {
		s = &enqueueState{}
		enqueueStates.entries[key] = s
	}
	return s
}

// pruneEnqueueStates drops state entries for ServiceSets that were not
// observed this tick — deleted, garbage-collected, or moved out of the
// poller's List scope. Runs under the state map's lock; O(N) in current
// map size.
func pruneEnqueueStates(seen map[client.ObjectKey]struct{}) {
	enqueueStates.Lock()
	defer enqueueStates.Unlock()
	for key := range enqueueStates.entries {
		if _, ok := seen[key]; !ok {
			delete(enqueueStates.entries, key)
		}
	}
}

// enqueueClusterSummary returns a [pollerutil.EnqueueFunc] that emits a
// ServiceSet only when it needs work, rather than every tick. Three
// gates decide, in order:
//
//  1. ClusterSummary.ResourceVersion advanced since last enqueue — a
//     real sveltos-side change (apply, upgrade, patch). Enqueue and
//     reset back-off to enqueueBaseBackoff.
//  2. ServiceSet.Status.Deployed == true AND CS RV unchanged — the
//     verifier confirmed every service is Deployed AND sveltos has not
//     moved since then. Skip. There is no ongoing health controller in
//     this codebase — the verifier's role in this PR is apply-time
//     correctness, not continuous pod-death detection.
//  3. Otherwise (in flight) — enqueue with exponentially-increasing
//     back-off from enqueueBaseBackoff up to enqueueMaxBackoff. Any
//     CS RV change resets to the base.
//
// Per-ServiceSet state is retained across ticks in enqueueStates;
// entries are pruned when a ServiceSet disappears from the poller's List.
//
// Regional clients are cached per tick.
func enqueueClusterSummary(cl client.Client, systemNamespace string) pollerutil.EnqueueFunc[*kcmv1.ServiceSet] {
	return func(ctx context.Context) ([]*kcmv1.ServiceSet, error) {
		logger := ctrl.LoggerFrom(ctx)

		serviceSetList := new(kcmv1.ServiceSetList)
		if err := cl.List(ctx, serviceSetList); err != nil {
			return nil, fmt.Errorf("failed to list ServiceSet objects: %w", err)
		}

		logger.V(1).Info("Listing ServiceSet objects", "service_set_count", len(serviceSetList.Items))

		// cluster object key -> regional client, reused within a single tick
		rgnClients := make(map[client.ObjectKey]client.Client)
		seenThisTick := make(map[client.ObjectKey]struct{}, len(serviceSetList.Items))
		now := time.Now()

		out := make([]*kcmv1.ServiceSet, 0, len(serviceSetList.Items))
		for i := range serviceSetList.Items {
			serviceSet := &serviceSetList.Items[i]
			key := client.ObjectKeyFromObject(serviceSet)

			if !serviceSet.GetDeletionTimestamp().IsZero() {
				logger.V(1).Info("ServiceSet is being deleted, skipping polling", "service_set", key)
				continue
			}
			seenThisTick[key] = struct{}{}

			rgnClient, err := resolveRegionalClient(ctx, cl, serviceSet, systemNamespace, rgnClients)
			if err != nil {
				logger.V(1).Error(err, "failed to get regional client", "service_set", key)
				continue
			}

			var profile client.Object
			if serviceSet.Spec.Provider.SelfManagement {
				profile = new(addoncontrollerv1beta1.ClusterProfile)
			} else {
				profile = new(addoncontrollerv1beta1.Profile)
			}
			if err := rgnClient.Get(ctx, key, profile); err != nil {
				logger.V(1).Error(err, "failed to get Profile or ClusterProfile", "object_name", key)
				continue
			}

			summary, err := getClusterSummaryForServiceSet(ctx, rgnClient, serviceSet, profile)
			if err != nil {
				logger.V(1).Error(err, "failed to get ClusterSummary", "service_set", key)
				continue
			}

			state := loadOrCreateEnqueueState(key)
			if state.evaluate(now, summary.ResourceVersion, serviceSet.Status.Deployed) {
				logger.V(1).Info("Scheduling reconcile",
					"service_set", key,
					"cluster_summary", client.ObjectKeyFromObject(summary),
					"next_backoff", state.currentBackoff,
				)
				out = append(out, serviceSet)
			}
		}

		pruneEnqueueStates(seenThisTick)

		return out, nil
	}
}
