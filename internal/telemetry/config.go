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

	"github.com/K0rdent/kcm/internal/utils"
)

// Config configurates Telemetry Collector.
type Config struct {
	MgmtClient       client.Client
	Mode             Mode
	SystemNamespace  string
	Interval         time.Duration
	JitterPercentage uint // ie 10%
	Concurrency      int
}

// Opt is an optional function to change the [Config].
type Opt func(*Config)

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
	if flagValue == "" {
		*m = ModeLocal
		return nil
	}

	switch v := Mode(flagValue); v {
	case ModeDisabled, ModeOnline, ModeLocal:
		*m = v
		return nil
	default:
		return fmt.Errorf("unknown mode %q", flagValue)
	}
}

func (c *Config) BindFlags(fs *flag.FlagSet) {
	fs.Var(&c.Mode, "telemetry-mode", "Telemetry collection mode (one of 'online', 'local' or 'disabled'). Empty value default to 'local'")
	fs.IntVar(&c.Concurrency, "telemetry-concurrency", 5, "Number of clusters for which data is collected concurrently")
	fs.DurationVar(&c.Interval, "telemetry-interval", 24*time.Hour, "How frequently to collect data")
	fs.UintVar(&c.JitterPercentage, "telemetry-jitter", 10, "Jitter of telemetry collection interval given in percentage (0, 100)")
}

// UseFlagOptions configures the telemetry runner to use the [Config] set by parsing telemetry options flags from the CLI.
//
// Note: not all options can be set via flags, e.g. MgmtClient cannot be set in that manner.
//
//	cfg := &telemetry.Config{MgmtClient: cl}
//	cfg.BindFlags(flag.CommandLine)
//	flag.Parse()
//	runner, err := telemetry.NewRunner(telemetry.UseFlagOptions(cfg))
func UseFlagOptions(in *Config) Opt {
	return func(c *Config) { *c = *in }
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

	if c.MgmtClient == nil {
		return errors.New("management client must be set")
	}

	if c.Mode != "" {
		switch c.Mode {
		case ModeDisabled, ModeOnline, ModeLocal:
		default:
			return fmt.Errorf("unknown mode %q", c.Mode)
		}
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

	if c.Mode == "" {
		c.Mode = ModeLocal
	}

	if c.SystemNamespace == "" {
		c.SystemNamespace = utils.CurrentNamespace()
	}

	if c.JitterPercentage == 0 {
		c.JitterPercentage = 10
	}
}
