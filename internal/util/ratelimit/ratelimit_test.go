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

package ratelimit

import (
	"testing"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

func TestTypedFastSlow(t *testing.T) {
	const (
		fastDelay       = 10 * time.Millisecond
		slowDelay       = 50 * time.Millisecond
		maxFastAttempts = 2
	)

	rl := TypedFastSlow[string](fastDelay, slowDelay, maxFastAttempts)

	item := "item-a"

	// Bucket limiter has a burst of 100, so it never dominates within these
	// few calls; the fast/slow item limiter's delay wins the max-of comparison.
	if got := rl.When(item); got != fastDelay {
		t.Errorf("When() call 1 = %v, want %v (fast)", got, fastDelay)
	}
	if got := rl.When(item); got != fastDelay {
		t.Errorf("When() call 2 = %v, want %v (fast)", got, fastDelay)
	}
	if got := rl.When(item); got != slowDelay {
		t.Errorf("When() call 3 = %v, want %v (slow, past maxFastAttempts)", got, slowDelay)
	}

	if got := rl.NumRequeues(item); got != 3 {
		t.Errorf("NumRequeues() = %d, want 3", got)
	}

	rl.Forget(item)

	if got := rl.NumRequeues(item); got != 0 {
		t.Errorf("NumRequeues() after Forget() = %d, want 0", got)
	}
	if got := rl.When(item); got != fastDelay {
		t.Errorf("When() after Forget() = %v, want %v (fast again)", got, fastDelay)
	}
}

func TestFastSlow(t *testing.T) {
	const (
		fastDelay       = 10 * time.Millisecond
		slowDelay       = 50 * time.Millisecond
		maxFastAttempts = 1
	)

	rl := FastSlow(fastDelay, slowDelay, maxFastAttempts)
	req := ctrl.Request{}

	if got := rl.When(req); got != fastDelay {
		t.Errorf("When() call 1 = %v, want %v (fast)", got, fastDelay)
	}
	if got := rl.When(req); got != slowDelay {
		t.Errorf("When() call 2 = %v, want %v (slow)", got, slowDelay)
	}
}

func TestDefaultFastSlow(t *testing.T) {
	rl := DefaultFastSlow()
	req := ctrl.Request{}

	// Only exercise a couple of calls: the token bucket's burst (100) would
	// otherwise dominate the max-of comparison well before
	// DefaultMaxFastAttempts (300) is reached, making the delay unpredictable.
	if got := rl.When(req); got != DefaultFastDelay {
		t.Errorf("When() call 1 = %v, want %v (fast)", got, DefaultFastDelay)
	}
	if got := rl.NumRequeues(req); got != 1 {
		t.Errorf("NumRequeues() = %d, want 1", got)
	}

	rl.Forget(req)
	if got := rl.NumRequeues(req); got != 0 {
		t.Errorf("NumRequeues() after Forget() = %d, want 0", got)
	}
}
