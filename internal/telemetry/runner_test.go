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
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// mockCollector is a test double for the Collector interface.
type mockCollector struct {
	mu           sync.Mutex
	collectCalls int
	closeCalls   int
	collectErr   error
	closeErr     error
}

func (m *mockCollector) Collect(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.collectCalls++
	return m.collectErr
}

func (m *mockCollector) Close(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCalls++
	return m.closeErr
}

func TestNewRunner_DisabledMode(t *testing.T) {
	cfg := &Config{Mode: ModeDisabled, ParentClient: fake.NewClientBuilder().Build()}
	cfg.normalize()

	runner, err := NewRunner(cfg)
	require.NoError(t, err)
	require.True(t, runner.isDisabled)
}

func TestNewRunner_InvalidConfig(t *testing.T) {
	cfg := &Config{Mode: "invalid"}
	cfg.normalize()

	_, err := NewRunner(cfg)
	require.ErrorContains(t, err, "failed to validate cfg")
}

func TestNewRunner_ValidOnlineMode(t *testing.T) {
	cfg := &Config{Mode: ModeOnline, ParentClient: fake.NewClientBuilder().Build()}
	cfg.normalize()

	segmentToken = "not-empty"

	runn, err := NewRunner(cfg)
	require.NoError(t, err)
	require.True(t, runn.Enabled())
}

func TestNewRunner_ValidLocalMode(t *testing.T) {
	t.Cleanup(func() {
		_ = os.RemoveAll("foo")
	})

	cfg := &Config{Mode: ModeLocal, LocalBaseDir: "foo", ParentClient: fake.NewClientBuilder().Build()}
	cfg.normalize()

	runn, err := NewRunner(cfg)
	require.NoError(t, err)
	require.True(t, runn.Enabled())
}

func TestNewRunner_OnlineNoSegmentToken(t *testing.T) {
	cfg := &Config{Mode: ModeOnline, ParentClient: fake.NewClientBuilder().Build()}
	cfg.normalize()

	segmentToken = ""

	runn, err := NewRunner(cfg)
	require.NoError(t, err)
	require.False(t, runn.Enabled())
}

func TestRunner_Start_Disabled(t *testing.T) {
	r := &Runner{
		isDisabled: true,
	}

	err := r.Start(t.Context())
	require.NoError(t, err)
}

func TestRunner_Start_NormalFlow(t *testing.T) {
	mock := &mockCollector{}
	r := &Runner{
		collector:      mock,
		frequency:      10 * time.Millisecond,
		jitterFraction: 0.0,
		isDisabled:     false,
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()

	err := r.Start(ctx)
	require.NoError(t, err)

	// Should have at least one collection
	require.GreaterOrEqual(t, mock.collectCalls, 1)
	require.Equal(t, 1, mock.closeCalls)
}

func TestRunner_Start_WithErrors(t *testing.T) {
	mock := &mockCollector{
		collectErr: errors.New("collect failed"),
	}
	r := &Runner{
		collector:      mock,
		frequency:      10 * time.Millisecond,
		jitterFraction: 0.0,
		isDisabled:     false,
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()

	err := r.Start(ctx)
	require.NoError(t, err)

	require.GreaterOrEqual(t, mock.collectCalls, 1)
	require.Equal(t, 1, mock.closeCalls)
}
