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

package backup

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	kcmv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
)

var _ manager.Runnable = (*Runner)(nil)

// Runner is a periodic runner which enqueues [github.com/K0rdent/kcm/api/v1alpha1.ManagementBackup] for reconciliation on a schedule.
type Runner struct {
	eventC   chan event.GenericEvent
	cl       client.Client
	started  *atomic.Bool
	interval time.Duration
}

// RunnerOpt is a function which configures the [Runner].
type RunnerOpt func(c *Runner)

// NewRunner creates a new periodic [Runner] and configures it using the provided [RunnerOpt].
func NewRunner(opts ...RunnerOpt) *Runner {
	r := &Runner{
		eventC:   make(chan event.GenericEvent),
		started:  new(atomic.Bool),
		interval: 1 * time.Minute,
	}

	for _, o := range opts {
		o(r)
	}

	return r
}

// WithClient configures the [Runner] with the given client.
func WithClient(cl client.Client) RunnerOpt {
	return func(c *Runner) {
		c.cl = cl
	}
}

// WithInterval configures the [Runner] with the given interval.
func WithInterval(interval time.Duration) RunnerOpt {
	return func(c *Runner) {
		c.interval = interval
	}
}

// GetEventChannel returns the channel [sigs.k8s.io/controller-runtime/pkg/event.GenericEvent] typed.
func (r *Runner) GetEventChannel() <-chan event.GenericEvent {
	return r.eventC
}

// Start implements the [sigs.k8s.io/controller-runtime/pkg/manager.Runnable] interface.
func (r *Runner) Start(ctx context.Context) error {
	if r.started.Load() {
		return errors.New("the runner cannot be started twice")
	}
	if r.cl == nil {
		return errors.New("cannot start runnter without the client")
	}

	r.started.Store(true)

	defer close(r.eventC)

	l := ctrl.LoggerFrom(ctx).WithName("mgmtbackup_schedule_runner")
	ctx = ctrl.LoggerInto(ctx, l)

	l.Info("Starting schedule runner")

	wait.Until(func() {
		if err := r.enqueueScheduledBackups(ctx); err != nil {
			if errors.Is(err, errEmptyList) {
				l.V(1).Info("No management backups with schedule to enqueue")
				return
			}

			l.Error(err, "failed to enqueue management backups")
		}
	}, r.interval, ctx.Done())

	return nil
}

var errEmptyList = errors.New("no items available to enqueue")

// enqueueScheduledBackups enqueues the [github.com/K0rdent/kcm/api/v1alpha1.ManagementBackup] objects which are properly annotated.
func (r *Runner) enqueueScheduledBackups(ctx context.Context) error {
	schedules := new(kcmv1alpha1.ManagementBackupList)
	if err := r.cl.List(ctx, schedules, client.MatchingFields{kcmv1alpha1.ManagementBackupScheduledIndexKey: "true"}); err != nil {
		return fmt.Errorf("failed to list ManagementBackups in periodic runner: %w", err)
	}

	if len(schedules.Items) == 0 {
		return errEmptyList
	}

	for _, item := range schedules.Items {
		if item.Status.Paused { // no sense to enqueue paused schedules
			continue
		}
		r.eventC <- event.GenericEvent{
			Object: &item,
		}
	}

	return nil
}
