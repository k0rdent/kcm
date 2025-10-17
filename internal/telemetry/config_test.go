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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestConfig_validate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := &Config{
			MgmtClient:       fake.NewClientBuilder().Build(),
			Mode:             ModeLocal,
			SystemNamespace:  "kube-system",
			Interval:         10 * time.Minute,
			JitterPercentage: 5,
			Concurrency:      2,
			LocalBaseDir:     "some",
		}
		require.NoError(t, cfg.validate())
	})

	t.Run("missing client", func(t *testing.T) {
		cfg := &Config{
			Mode:             ModeLocal,
			Interval:         10 * time.Minute,
			JitterPercentage: 5,
			Concurrency:      1,
		}
		err := cfg.validate()
		require.ErrorContains(t, err, "management client")
	})

	t.Run("invalid concurrency", func(t *testing.T) {
		cfg := &Config{
			MgmtClient:       fake.NewClientBuilder().Build(),
			Concurrency:      -1,
			Interval:         10 * time.Minute,
			JitterPercentage: 5,
		}
		err := cfg.validate()
		require.ErrorContains(t, err, "concurrency")
	})

	t.Run("invalid interval", func(t *testing.T) {
		cfg := &Config{
			MgmtClient:       fake.NewClientBuilder().Build(),
			Concurrency:      1,
			Interval:         59 * time.Second,
			JitterPercentage: 5,
		}
		err := cfg.validate()
		require.ErrorContains(t, err, "interval")
	})

	t.Run("invalid jitter", func(t *testing.T) {
		cfg := &Config{
			MgmtClient:       fake.NewClientBuilder().Build(),
			Concurrency:      1,
			Interval:         10 * time.Minute,
			JitterPercentage: 100,
		}
		err := cfg.validate()
		require.ErrorContains(t, err, "jitter")
	})

	t.Run("empty directory in local mode", func(t *testing.T) {
		cfg := &Config{
			MgmtClient:       fake.NewClientBuilder().Build(),
			Concurrency:      1,
			Mode:             ModeLocal,
			Interval:         10 * time.Minute,
			JitterPercentage: 10,
		}
		err := cfg.validate()
		require.ErrorContains(t, err, "base directory")
	})

	t.Run("unknown mode", func(t *testing.T) {
		cfg := &Config{
			MgmtClient:       fake.NewClientBuilder().Build(),
			Concurrency:      1,
			Interval:         10 * time.Minute,
			JitterPercentage: 10,
			Mode:             Mode("invalid-mode"),
		}
		err := cfg.validate()
		require.ErrorContains(t, err, "unknown mode")
	})

	t.Run("empty mode", func(t *testing.T) {
		cfg := &Config{
			MgmtClient:       fake.NewClientBuilder().Build(),
			Concurrency:      1,
			Interval:         10 * time.Minute,
			JitterPercentage: 10,
		}
		err := cfg.validate()
		require.ErrorContains(t, err, "unknown mode")
	})
}

func TestConfig_normalize(t *testing.T) {
	cfg := &Config{}
	cfg.normalize()

	require.Equal(t, 5, cfg.Concurrency)
	require.Equal(t, 24*time.Hour, cfg.Interval)
	require.Equal(t, 10, int(cfg.JitterPercentage))
	require.NotEmpty(t, cfg.SystemNamespace)
}

func TestUseFlagOptions(t *testing.T) {
	base := &Config{
		Mode:             ModeOnline,
		Interval:         time.Hour,
		JitterPercentage: 15,
		Concurrency:      7,
		SystemNamespace:  "custom-ns",
	}

	newCfg := &Config{}
	opt := UseFlagOptions(base)
	opt(newCfg)

	require.Equal(t, *base, *newCfg)
}

func TestMode_Set(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Mode
		wantErr error
	}{
		{"valid online", "online", ModeOnline, nil},
		{"valid local", "local", ModeLocal, nil},
		{"valid disabled", "disabled", ModeDisabled, nil},
		{"empty is invalid", "", "", errors.New("unknown mode")},
		{"invalid mode", "invalid", "", errors.New("unknown mode")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m Mode
			err := m.Set(tt.input)
			if tt.wantErr != nil {
				require.ErrorContains(t, err, "unknown mode")
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, m)
			}
		})
	}
}

func TestMode_String(t *testing.T) {
	require.Equal(t, "online", ModeOnline.String())
	require.Equal(t, "local", ModeLocal.String())
	require.Equal(t, "disabled", ModeDisabled.String())
}

func TestConfig_BindFlags(t *testing.T) {
	var cfg Config
	fs := flag.NewFlagSet("test", flag.ContinueOnError)

	cfg.BindFlags(fs)

	err := fs.Parse([]string{
		"-telemetry-mode=online",
		"-telemetry-concurrency=3",
		"-telemetry-interval=5m",
		"-telemetry-jitter=7",
	})
	require.NoError(t, err)

	require.Equal(t, ModeOnline, cfg.Mode)
	require.Equal(t, 3, cfg.Concurrency)
	require.Equal(t, 5*time.Minute, cfg.Interval)
	require.Equal(t, uint(7), cfg.JitterPercentage)
}
