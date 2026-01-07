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

package telemetry

import (
	"errors"
	"flag"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

var segmentToken = "" // segmentToken variable is set via the linker flags

// Config configurates Telemetry Collector.
type Config struct {
	// ParentClient is the client from the current (local) cluster, that is either
	// a regional or the management cluster.
	ParentClient     client.Client
	DirectReader     client.Reader // WARN: need once during the init; used to by-pass cache before the Start()
	Mode             Mode
	SystemNamespace  string
	LocalBaseDir     string
	Interval         time.Duration
	JitterPercentage uint // ie 10%
	Concurrency      int
}

// Mode defines the way telemetry is collected and stored.
type Mode string //nolint:recvcheck // invalid in this case

const (
	ModeDisabled Mode = "disabled"
	ModeLocal    Mode = "local"
	ModeOnline   Mode = "online"
)

var _ flag.Value = (*Mode)(nil)

func (m Mode) String() string {
	return string(m)
}

func (m *Mode) Set(flagValue string) error {
	switch v := Mode(flagValue); v {
	case ModeDisabled, ModeOnline, ModeLocal:
		*m = v
		return nil
	default:
		return fmt.Errorf("unknown mode %q, must be one of 'online', 'local' or 'disabled'", flagValue)
	}
}

func (c *Config) BindFlags(fs *flag.FlagSet) {
	fs.Var(&c.Mode, "telemetry-mode", "Telemetry collection mode (one of 'online', 'local' or 'disabled')")
	fs.IntVar(&c.Concurrency, "telemetry-concurrency", 5, "Number of clusters for which data is collected concurrently")
	fs.DurationVar(&c.Interval, "telemetry-interval", 24*time.Hour, "How frequently to collect data")
	fs.UintVar(&c.JitterPercentage, "telemetry-jitter", 10, "Jitter of telemetry collection interval given in percentage (0, 100)")
	fs.StringVar(&c.LocalBaseDir, "telemetry-base-dir", "/var/lib/telemetry", "Base directory where to put local telemetry data")
}

func (c *Config) validate() error {
	if c.Concurrency < 0 {
		return errors.New("concurrency must be positive")
	}

	if c.JitterPercentage >= 100 {
		return errors.New("jitter percentage must be in [1,100)")
	}

	if c.Interval < 1*time.Minute {
		return errors.New("interval must be >= 1 minute")
	}

	if c.ParentClient == nil {
		return errors.New("management client must be set")
	}

	if c.Mode == ModeLocal && c.LocalBaseDir == "" {
		return errors.New("base directory must be set for the local telemetry mode")
	}

	switch c.Mode {
	case ModeDisabled, ModeOnline, ModeLocal:
	default:
		return fmt.Errorf("unknown mode %q", c.Mode)
	}

	return nil
}

func (c *Config) normalize() {
	if c.Concurrency <= 0 {
		c.Concurrency = 5
	}

	if c.Interval < 1*time.Minute {
		c.Interval = 24 * time.Hour
	}

	if c.SystemNamespace == "" {
		c.SystemNamespace = kubeutil.CurrentNamespace()
	}

	if c.JitterPercentage == 0 {
		c.JitterPercentage = 10
	}
}
