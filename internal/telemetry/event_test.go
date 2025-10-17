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

func assertTrackEvent(t *testing.T, mock *mockClient, wantCalled bool, wantEvent *analytics.Track) {
	t.Helper()

	if mock == nil {
		return
	}

	require.Equal(t, wantCalled, mock.enqueueCalled)

	if wantEvent != nil {
		require.Len(t, mock.events, 1)
		got := mock.events[0]
		require.Equal(t, wantEvent.AnonymousId, got.AnonymousId)
		require.Equal(t, wantEvent.Event, got.Event)
		require.Equal(t, wantEvent.Properties, got.Properties)
	}
}

func TestTrackEvents(t *testing.T) {
	tests := []struct {
		name       string
		setup      func() *mockClient
		call       func() error
		wantErr    string
		wantCalled bool
		wantEvent  *analytics.Track
	}{
		{
			name:  "cluster deployment create - success",
			setup: func() *mockClient { return &mockClient{} },
			call: func() error {
				return TrackClusterDeploymentCreate("anon-id", "cd-id", "tmpl", true)
			},
			wantCalled: true,
			wantEvent: &analytics.Track{
				AnonymousId: "anon-id",
				Event:       "cluster-deployment-create",
				Properties: map[string]any{
					"clusterDeploymentID": "cd-id",
					"template":            "tmpl",
					"dryRun":              true,
				},
			},
		},
		{
			name:  "cluster deployment create - no client",
			setup: func() *mockClient { return nil },
			call: func() error {
				return TrackClusterDeploymentCreate("x", "y", "z", false)
			},
		},
		{
			name: "cluster deployment create - enqueue fails",
			setup: func() *mockClient {
				return &mockClient{err: errors.New("enqueue failed")}
			},
			call: func() error {
				return TrackClusterDeploymentCreate("x", "y", "z", false)
			},
			wantErr: "enqueue failed",
		},
		{
			name:  "cluster ipam create - success",
			setup: func() *mockClient { return &mockClient{} },
			call: func() error {
				return TrackClusterIPAMCreate("test", "cluster", "testprovider")
			},
			wantCalled: true,
			wantEvent: &analytics.Track{
				AnonymousId: "test",
				Event:       "cluster-ipam-create",
				Properties: map[string]any{
					"cluster":      "cluster",
					"ipamProvider": "testprovider",
				},
			},
		},
		{
			name:  "cluster ipam create - no client",
			setup: func() *mockClient { return nil },
			call: func() error {
				return TrackClusterIPAMCreate("x", "y", "z")
			},
		},
		{
			name: "cluster ipam create - enqueue fails",
			setup: func() *mockClient {
				return &mockClient{err: errors.New("enqueue failed")}
			},
			call: func() error {
				return TrackClusterIPAMCreate("x", "y", "z")
			},
			wantErr: "enqueue failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := tt.setup()

			if mock == nil {
				analyticsClient = nil
			} else {
				analyticsClient = mock
			}

			err := tt.call()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)

			assertTrackEvent(t, mock, tt.wantCalled, tt.wantEvent)
		})
	}
}
