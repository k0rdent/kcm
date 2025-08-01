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
	"sync"
	"testing"

	"github.com/segmentio/analytics-go/v3"
	"github.com/stretchr/testify/require"
)

// mockClient implements [github.com/segmentio/analytics-go/v3.Client] for testing
type mockClient struct {
	mu            sync.Mutex
	events        []analytics.Track
	err           error
	enqueueCalled bool
}

func (m *mockClient) Enqueue(msg analytics.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	track, ok := msg.(analytics.Track)
	if ok {
		m.events = append(m.events, track)
	}
	m.enqueueCalled = true
	return m.err
}

func (*mockClient) Close() error { return nil }

func TestSetSegmentIOClient_Idempotent(t *testing.T) {
	mock1 := &mockClient{}
	mock2 := &mockClient{}

	analyticsClient = nil
	clientOnce = sync.Once{}

	SetSegmentIOClient(mock1)
	SetSegmentIOClient(mock2)

	require.Same(t, mock1, analyticsClient)
	require.NotSame(t, mock2, analyticsClient)
}

func TestTrackClusterDeploymentCreate(t *testing.T) {
	mock := &mockClient{}
	analyticsClient = mock

	err := TrackClusterDeploymentCreate("anon-id", "cd-id", "tmpl", true)
	require.NoError(t, err)

	require.True(t, mock.enqueueCalled)
	require.Len(t, mock.events, 1)
	e := mock.events[0]

	require.Equal(t, "anon-id", e.AnonymousId)
	require.Equal(t, "cluster-deployment-create", e.Event)
	require.Equal(t, "cd-id", e.Properties["clusterDeploymentID"])
	require.Equal(t, "tmpl", e.Properties["template"])
	require.Equal(t, true, e.Properties["dryRun"])
}

func TestTrackClusterDeploymentCreate_NoClient(t *testing.T) {
	analyticsClient = nil
	err := TrackClusterDeploymentCreate("x", "y", "z", false)
	require.NoError(t, err)
}

func TestTrackClusterDeploymentCreate_EnqueueFails(t *testing.T) {
	mock := &mockClient{err: errors.New("enqueue failed")}
	analyticsClient = mock

	err := TrackClusterDeploymentCreate("x", "y", "z", false)
	require.ErrorContains(t, err, "enqueue failed")
}
