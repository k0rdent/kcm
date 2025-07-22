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

// Package telemetry comment
package telemetry

import (
	"context"
	"fmt"
	"math/rand/v2"
	"runtime"
	"time"

	"github.com/segmentio/analytics-go/v3"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/K0rdent/kcm/internal/build"
	"github.com/K0rdent/kcm/internal/telemetry/collector"
)

// Collector is the generic interface for a telemetry collector.
type Collector interface {
	// Called periodically by the controller to trigger a collection round.
	Collect(context.Context) error
	// Called to persist any buffered data.
	Flush(context.Context) error
	// Called once during shutdown to release resources and flush final data.
	Close(context.Context) error
}

// Runner is a controller-runtime Runnable that periodically invokes the tracker.
type Runner struct {
	collector      Collector
	frequency      time.Duration
	jitterFraction float64
	isDisabled     bool
}

var _ manager.Runnable = (*Runner)(nil)

// NewRunner constructs a new [Runner] instance.
func NewRunner(cfg *Config) (*Runner, error) {
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("failed to validate cfg: %w", err)
	}
	cfg.normalize()

	var tr Collector
	if cfg.Mode == ModeOnline {
		tz := ""
		if loc := time.Now().Location(); loc != nil {
			tz = loc.String()
		}

		segmentClient, err := analytics.NewWithConfig(segmentToken, analytics.Config{
			BatchSize: 500,
			Interval:  time.Minute,
			DefaultContext: &analytics.Context{
				App: analytics.AppInfo{
					Build:     build.Commit,
					Name:      build.Name,
					Version:   build.Version,
					Namespace: cfg.SystemNamespace,
				},
				OS: analytics.OSInfo{
					Name:    runtime.GOOS,
					Version: runtime.GOARCH,
				},
				Timezone: tz,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to init segmentio client: %w", err)
		}

		segmentCollector, err := collector.NewSegmentIO(segmentClient, cfg.MgmtClient, cfg.Concurrency)
		if err != nil {
			return nil, fmt.Errorf("failed to init segment collector: %w", err)
		}

		SetSegmentIOClient(segmentClient)

		tr = segmentCollector
	}

	return &Runner{
		collector:      tr,
		frequency:      cfg.Interval,
		jitterFraction: float64(cfg.JitterPercentage) / 100,
		isDisabled:     cfg.Mode == ModeDisabled,
	}, nil
}

func (r *Runner) Start(ctx context.Context) error {
	l := ctrl.LoggerFrom(ctx).WithName("telemetry-runner")
	ctx = ctrl.LoggerInto(ctx, l)
	if r.isDisabled {
		l.Info("Telemetry is disabled")
		return nil
	}

	l.Info("Starting telemetry runner", "interval", r.frequency, "jitter", r.jitterFraction)

	jitterDur := func(base time.Duration) time.Duration {
		jitter := rand.Float64()*2*r.jitterFraction - r.jitterFraction // [-j, +j]
		return time.Duration(float64(base) * (1 + jitter))
	}

	for {
		select {
		case <-ctx.Done():
			l.Info("Shutting down telemetry runner")
			finishCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_ = r.collector.Flush(finishCtx)    //nolint:contextcheck // false-positive
			err := r.collector.Close(finishCtx) //nolint:contextcheck // false-positive
			cancel()
			return err
		default:
			l.Info("Running telemetry collection")

			if err := r.collector.Collect(ctx); err != nil {
				l.Error(err, "failed to collect telemetry, will try again")
			} else if err := r.collector.Flush(ctx); err != nil {
				l.Error(err, "failed to flush telemetry data")
			} else {
				l.Info("Telemetry collection round complete")
			}

			jdur := jitterDur(r.frequency)
			timer := time.NewTimer(jdur)
			l.V(1).Info("Waiting next tick with jitter", "duration", jdur)

			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
			}
		}
	}
}
