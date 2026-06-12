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

package poller

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func newObj(t *testing.T, name string) *corev1.ConfigMap {
	t.Helper()

	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}}
}

func TestNewRunner_NilEnqueuePanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil EnqueueFunc, got none")
		}
	}()
	_ = NewRunner[*corev1.ConfigMap](nil)
}

func TestNewRunner_Defaults(t *testing.T) {
	t.Parallel()

	noop := func(context.Context) ([]*corev1.ConfigMap, error) { return nil, nil }
	r := NewRunner(noop)

	if r.interval != DefaultInterval {
		t.Fatalf("interval = %s, want %s", r.interval, DefaultInterval)
	}
	if r.name != DefaultName {
		t.Fatalf("name = %q, want %q", r.name, DefaultName)
	}
	if cap(r.eventC) != DefaultBufferSize {
		t.Fatalf("cap(eventC) = %d, want %d", cap(r.eventC), DefaultBufferSize)
	}
}

func TestOptions_NoOpOnInvalidValues(t *testing.T) {
	t.Parallel()

	noop := func(context.Context) ([]*corev1.ConfigMap, error) { return nil, nil }
	r := NewRunner(noop,
		WithInterval(0),
		WithInterval(-1*time.Second),
		WithBufferSize(0),
		WithBufferSize(-3),
		WithName(""),
	)

	if r.interval != DefaultInterval {
		t.Fatalf("interval = %s, want %s (defaults preserved)", r.interval, DefaultInterval)
	}
	if r.name != DefaultName {
		t.Fatalf("name = %q, want %q (defaults preserved)", r.name, DefaultName)
	}
	if cap(r.eventC) != DefaultBufferSize {
		t.Fatalf("cap(eventC) = %d, want %d (defaults preserved)", cap(r.eventC), DefaultBufferSize)
	}
}

func TestOptions_ApplyValid(t *testing.T) {
	t.Parallel()

	noop := func(context.Context) ([]*corev1.ConfigMap, error) { return nil, nil }
	r := NewRunner(noop,
		WithInterval(2*time.Second),
		WithBufferSize(7),
		WithName("custom"),
	)

	if r.interval != 2*time.Second {
		t.Fatalf("interval = %s, want 2s", r.interval)
	}
	if r.name != "custom" {
		t.Fatalf("name = %q, want %q", r.name, "custom")
	}
	if cap(r.eventC) != 7 {
		t.Fatalf("cap(eventC) = %d, want 7", cap(r.eventC))
	}
}

func TestTick_EmitsObjects(t *testing.T) {
	t.Parallel()

	enq := func(context.Context) ([]*corev1.ConfigMap, error) {
		return []*corev1.ConfigMap{newObj(t, "a"), newObj(t, "b")}, nil
	}
	r := NewRunner(enq, WithBufferSize(4))

	r.tick(t.Context())

	got := drain(t, r.GetEventChannel())
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Object.GetName() != "a" || got[1].Object.GetName() != "b" {
		t.Fatalf("unexpected names: [%q, %q]", got[0].Object.GetName(), got[1].Object.GetName())
	}
}

func TestTick_DropsOnFullChannel(t *testing.T) {
	t.Parallel()

	enq := func(context.Context) ([]*corev1.ConfigMap, error) {
		return []*corev1.ConfigMap{newObj(t, "a"), newObj(t, "b"), newObj(t, "c"), newObj(t, "d")}, nil
	}
	r := NewRunner(enq, WithBufferSize(2))

	r.tick(t.Context())

	got := drain(t, r.GetEventChannel())
	// channel capacity is 2; the other 2 must be dropped instead of blocking
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (others dropped)", len(got))
	}
}

func TestTick_IgnoresEnqueueError(t *testing.T) {
	t.Parallel()

	calls := 0
	enq := func(context.Context) ([]*corev1.ConfigMap, error) {
		calls++
		return []*corev1.ConfigMap{newObj(t, "dropped")}, errors.New("boom")
	}
	r := NewRunner(enq, WithBufferSize(4))

	r.tick(t.Context())

	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if got := drain(t, r.GetEventChannel()); len(got) != 0 {
		t.Fatalf("got %d events, want 0 (error must short-circuit)", len(got))
	}
}

func TestTick_IgnoresContextCanceledError(t *testing.T) {
	t.Parallel()

	enq := func(context.Context) ([]*corev1.ConfigMap, error) {
		return nil, context.Canceled
	}
	r := NewRunner(enq, WithBufferSize(4), WithName("ctxcancel"))

	// must not panic, must not log a noisy error
	r.tick(t.Context())

	if got := drain(t, r.GetEventChannel()); len(got) != 0 {
		t.Fatalf("got %d events, want 0", len(got))
	}
}

func TestTick_AbortsOnContextCancelDuringSend(t *testing.T) {
	t.Parallel()

	enq := func(context.Context) ([]*corev1.ConfigMap, error) {
		return []*corev1.ConfigMap{newObj(t, "a"), newObj(t, "b"), newObj(t, "c"), newObj(t, "d")}, nil
	}
	// buffer = 0 forces every send to compete with the select default branch
	r := NewRunner(enq, WithName("ctxcancel-send"))
	r.eventC = make(chan event.TypedGenericEvent[*corev1.ConfigMap]) // unbuffered, no reader

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// even though sends would block on an unbuffered channel, the select's
	// default branch ensures tick returns without blocking; ctx.Done is also
	// observed, so this must not hang
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.tick(ctx)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tick did not return on canceled context within 2s")
	}
}

func TestStart_RefusesSecondStart(t *testing.T) {
	t.Parallel()

	enq := func(context.Context) ([]*corev1.ConfigMap, error) { return nil, nil }
	r := NewRunner(enq, WithInterval(time.Hour))

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // returns immediately
	if err := r.Start(ctx); err != nil {
		t.Fatalf("first Start = %v, want nil", err)
	}
	// second attempt must fail even with a fresh context
	if err := r.Start(t.Context()); err == nil {
		t.Fatal("second Start = nil, want error")
	}
}

func TestStart_ClosesChannelOnReturn(t *testing.T) {
	t.Parallel()

	enq := func(context.Context) ([]*corev1.ConfigMap, error) { return nil, nil }
	r := NewRunner(enq, WithInterval(time.Hour))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start = %v, want nil", err)
	}

	// channel must be closed
	select {
	case _, ok := <-r.GetEventChannel():
		if ok {
			t.Fatal("expected channel to be closed")
		}
	default:
		t.Fatal("expected channel to be closed, got blocked")
	}
}

func TestStart_TicksUntilContextCanceled(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		const interval = time.Second

		var calls atomic.Int32
		enq := func(context.Context) ([]*corev1.ConfigMap, error) {
			calls.Add(1)
			return []*corev1.ConfigMap{newObj(t, "tick")}, nil
		}
		r := NewRunner(enq, WithInterval(interval), WithBufferSize(8))

		ctx, cancel := context.WithCancel(t.Context())
		startErr := make(chan error, 1)
		go func() { startErr <- r.Start(ctx) }()

		// drain the channel so tick() never hits the full-channel branch
		var drained atomic.Int32
		done := make(chan struct{})
		go func() {
			defer close(done)
			for range r.GetEventChannel() {
				drained.Add(1)
			}
		}()

		// wait.UntilWithContext fires at t=0, then every interval; sleeping past
		// 3*interval guarantees exactly four ticks before cancel
		time.Sleep(interval*3 + interval/2)
		cancel()

		if err := <-startErr; err != nil {
			t.Fatalf("Start = %v, want nil", err)
		}
		<-done

		if got := calls.Load(); got != 4 {
			t.Fatalf("calls = %d, want 4", got)
		}
		if got := drained.Load(); got != 4 {
			t.Fatalf("drained events = %d, want 4", got)
		}
	})
}

func TestGetEventChannel_StableIdentity(t *testing.T) {
	t.Parallel()

	noop := func(context.Context) ([]*corev1.ConfigMap, error) { return nil, nil }
	r := NewRunner(noop)
	first, second := r.GetEventChannel(), r.GetEventChannel()
	if first != second {
		t.Fatal("GetEventChannel returned different channels across calls")
	}
}

// drain reads all currently buffered events without blocking.
func drain(t *testing.T, c <-chan event.TypedGenericEvent[*corev1.ConfigMap]) []event.TypedGenericEvent[*corev1.ConfigMap] {
	t.Helper()

	var out []event.TypedGenericEvent[*corev1.ConfigMap]
	for {
		select {
		case e, ok := <-c:
			if !ok {
				return out
			}
			out = append(out, e)
		default:
			return out
		}
	}
}
