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

// Package poller provides a reusable controller-runtime [sigs.k8s.io/controller-runtime/pkg/manager.Runnable]
// that periodically invokes an enqueue function and emits the returned objects
// as [sigs.k8s.io/controller-runtime/pkg/event.TypedGenericEvent] on a typed
// channel.
package poller

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// DefaultInterval is the polling interval used when no [WithInterval]
	// option is provided.
	DefaultInterval = 1 * time.Minute
	// DefaultBufferSize is the capacity of the emitted event channel used
	// when no [WithBufferSize] option is provided.
	DefaultBufferSize = 64
	// DefaultName is the logger name used when no [WithName] option is
	// provided.
	DefaultName = "poller"
)

// EnqueueFunc returns the objects that should be emitted as
// [sigs.k8s.io/controller-runtime/pkg/event.TypedGenericEvent] on the
// [Runner]'s channel for a single tick.
//
// Returning (nil, nil) is the canonical way to express "nothing to enqueue,
// not an error". Returning a non-nil error logs it at the runner's logger
// and drops the tick; the next tick will retry.
//
// The returned slice must not contain nil entries: the runner forwards each
// element verbatim and controller-runtime panics on a nil event.Object.
type EnqueueFunc[T client.Object] func(ctx context.Context) ([]T, error)

// Runner is a controller-runtime
// [sigs.k8s.io/controller-runtime/pkg/manager.Runnable] that periodically
// invokes an [EnqueueFunc] and emits the returned objects as
// [sigs.k8s.io/controller-runtime/pkg/event.TypedGenericEvent] on a channel.
//
// T is the concrete client.Object kind handled by the runner; the emitted
// channel is typed [chan event.TypedGenericEvent[T]] so callers can wire it
// into a controller via [sigs.k8s.io/controller-runtime/pkg/source.TypedChannel]
// without an additional type assertion.
type Runner[T client.Object] struct {
	eventC   chan event.TypedGenericEvent[T]
	enqueue  EnqueueFunc[T]
	name     string
	interval time.Duration
	started  atomic.Bool
}

// config holds construction-time options for a [Runner]; kept private so
// [Option] stays non-generic and callers don't have to spell out T.
type config struct {
	name       string
	interval   time.Duration
	bufferSize int
}

// Option configures a [Runner] at construction time.
type Option func(*config)

// WithInterval sets the polling interval. The option is a no-op when d is
// non-positive, leaving [DefaultInterval] in effect.
func WithInterval(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.interval = d
		}
	}
}

// WithBufferSize sets the capacity of the emitted event channel. The option
// is a no-op when n is non-positive, leaving [DefaultBufferSize] in effect.
//
// The runner does not block on a full channel: events that cannot be sent
// immediately are dropped at V(1). The next tick will re-emit them if the
// [EnqueueFunc] still returns the same objects.
func WithBufferSize(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.bufferSize = n
		}
	}
}

// WithName sets the logger name attached to the runner's context. It also
// appears in dropped-event log lines, so it should be unique per runner.
func WithName(name string) Option {
	return func(c *config) {
		if name != "" {
			c.name = name
		}
	}
}

// NewRunner returns a [Runner] configured with the provided enqueue function
// and options. The returned runner must be registered with a manager via
// [sigs.k8s.io/controller-runtime/pkg/manager.Manager.Add] before its channel
// is wired into a controller.
//
// NewRunner panics when enqueue is nil; a nil enqueue is a programmer error.
func NewRunner[T client.Object](enqueue EnqueueFunc[T], opts ...Option) *Runner[T] {
	if enqueue == nil {
		panic("poller: NewRunner called with nil EnqueueFunc")
	}

	cfg := config{interval: DefaultInterval, bufferSize: DefaultBufferSize, name: DefaultName}
	for _, o := range opts {
		o(&cfg)
	}

	return &Runner[T]{
		eventC:   make(chan event.TypedGenericEvent[T], cfg.bufferSize),
		enqueue:  enqueue,
		name:     cfg.name,
		interval: cfg.interval,
	}
}

var _ manager.Runnable = (*Runner[client.Object])(nil)

// GetEventChannel returns the channel of emitted
// [sigs.k8s.io/controller-runtime/pkg/event.TypedGenericEvent].
//
// The returned channel is closed when [Runner.Start] returns.
func (r *Runner[T]) GetEventChannel() <-chan event.TypedGenericEvent[T] {
	return r.eventC
}

// Start implements [sigs.k8s.io/controller-runtime/pkg/manager.Runnable].
//
// It runs until ctx is canceled, invoking the [EnqueueFunc] every interval.
// Each returned object is emitted as a
// [sigs.k8s.io/controller-runtime/pkg/event.TypedGenericEvent]. A full channel
// causes the event to be dropped at V(1); the next tick will re-emit it if
// the enqueue function still returns the same object.
//
// Start refuses to run a second time and returns an error.
func (r *Runner[T]) Start(ctx context.Context) error {
	if !r.started.CompareAndSwap(false, true) {
		return errors.New("poller: runner cannot be started twice")
	}

	defer close(r.eventC)

	l := ctrl.LoggerFrom(ctx).WithName(r.name)
	ctx = ctrl.LoggerInto(ctx, l)

	l.Info("Starting poller", "interval", r.interval, "event_chan_cap", cap(r.eventC))

	wait.UntilWithContext(ctx, r.tick, r.interval)
	return nil
}

func (r *Runner[T]) tick(ctx context.Context) {
	l := ctrl.LoggerFrom(ctx)

	objs, err := r.enqueue(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}

		l.Error(err, "enqueue failed")
		return
	}

	for _, obj := range objs {
		select {
		case <-ctx.Done():
			return

		case r.eventC <- event.TypedGenericEvent[T]{Object: obj}:

		default:
			l.V(1).Info("event channel is full, dropping event",
				"object", client.ObjectKeyFromObject(obj),
				"gvk", obj.GetObjectKind().GroupVersionKind())
		}
	}
}
