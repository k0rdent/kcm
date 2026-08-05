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

package conditions

import (
	"fmt"
	"strings"
	"testing"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// the failure message sveltoscluster-manager reports for a Secret whose kubeconfig is not
// parseable at all, e.g. one holding the literal "hello world"
const unparseableKubeconfigFailure = `BuildConfigFromFlags: error loading config file "/tmp/kubeconfig1453503567": ` +
	`couldn't get version/kind; json parse error: json: cannot unmarshal string into Go value of type struct ` +
	`{ APIVersion string "json:\"apiVersion,omitempty\""; Kind string "json:\"kind,omitempty\"" }`

func TestGetSveltosClusterReadyCondition(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status         libsveltosv1beta1.SveltosClusterStatus
		wantStatus     metav1.ConditionStatus
		wantReason     string
		wantMsgSubstrs []string
	}{
		// A malformed kubeconfig makes sveltoscluster-manager fail before it can build a client, so
		// it returns without ever resetting status.ready: the stale ready=true here is exactly what
		// used to let the ClusterDeployment report Ready.
		"unparseable kubeconfig, stale ready=true": {
			status: libsveltosv1beta1.SveltosClusterStatus{
				Ready:              true,
				Version:            "v1.36.1+k0s",
				ConnectionStatus:   libsveltosv1beta1.ConnectionDown,
				ConnectionFailures: 115,
				FailureMessage:     new(unparseableKubeconfigFailure),
			},
			wantStatus:     metav1.ConditionFalse,
			wantReason:     kcmv1.ConnectionDownReason,
			wantMsgSubstrs: []string{"Connection to the cluster is down.", "couldn't get version/kind"},
		},

		// Valid kubeconfig, unreachable API server. The config builds fine, so the probe gets as far
		// as GET /version and fails there.
		"valid kubeconfig, connection refused past threshold": {
			status: libsveltosv1beta1.SveltosClusterStatus{
				ConnectionStatus:   libsveltosv1beta1.ConnectionDown,
				ConnectionFailures: 3,
				FailureMessage:     new(`Get "https://127.0.0.1:1/version?timeout=32s": dial tcp 127.0.0.1:1: connect: connection refused`),
			},
			wantStatus:     metav1.ConditionFalse,
			wantReason:     kcmv1.ConnectionDownReason,
			wantMsgSubstrs: []string{"Connection to the cluster is down.", "connection refused"},
		},
		"valid kubeconfig, network timeout past threshold": {
			status: libsveltosv1beta1.SveltosClusterStatus{
				ConnectionStatus:   libsveltosv1beta1.ConnectionDown,
				ConnectionFailures: 3,
				FailureMessage:     new(`Get "https://192.0.2.1:6443/version?timeout=32s": dial tcp 192.0.2.1:6443: i/o timeout`),
			},
			wantStatus:     metav1.ConditionFalse,
			wantReason:     kcmv1.ConnectionDownReason,
			wantMsgSubstrs: []string{"Connection to the cluster is down.", "i/o timeout"},
		},

		// Sveltos populates failureMessage on every failed probe but only flips connectionStatus to
		// Down once spec.consecutiveFailureThreshold is crossed, so a failure without Down means the
		// cluster is still within its allowance and must not hard-fail the ClusterDeployment.
		"failing but below the consecutive failure threshold": {
			status: libsveltosv1beta1.SveltosClusterStatus{
				ConnectionFailures: 2,
				FailureMessage:     new(`Get "https://192.0.2.1:6443/version?timeout=32s": dial tcp 192.0.2.1:6443: i/o timeout`),
			},
			wantStatus:     metav1.ConditionFalse,
			wantReason:     kcmv1.ProgressingReason,
			wantMsgSubstrs: []string{"failed 2 time(s) in a row", "i/o timeout"},
		},

		"healthy": {
			status: libsveltosv1beta1.SveltosClusterStatus{
				Ready:              true,
				Version:            "v1.35.0",
				ConnectionStatus:   libsveltosv1beta1.ConnectionHealthy,
				ConnectionFailures: 0,
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: kcmv1.SucceededReason,
		},

		// Freshly created SveltosCluster: the health of the cluster is genuinely not known yet, so
		// it must not be reported as ready.
		"not probed yet": {
			status:     libsveltosv1beta1.SveltosClusterStatus{},
			wantStatus: metav1.ConditionUnknown,
			wantReason: kcmv1.ConnectionProbePendingReason,
		},
		// ready=true with nothing else is still an unprobed cluster as far as we are concerned.
		"ready=true is not trusted on its own": {
			status: libsveltosv1beta1.SveltosClusterStatus{
				Ready:   true,
				Version: "v1.36.1+k0s",
			},
			wantStatus: metav1.ConditionUnknown,
			wantReason: kcmv1.ConnectionProbePendingReason,
		},
		// an empty failure message must not be mistaken for a failure
		"blank failure message is ignored": {
			status: libsveltosv1beta1.SveltosClusterStatus{
				ConnectionStatus: libsveltosv1beta1.ConnectionHealthy,
				FailureMessage:   new("   "),
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: kcmv1.SucceededReason,
		},
	}

	const generation = 7

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cd := &kcmv1.ClusterDeployment{}
			cd.Generation = generation
			sc := &libsveltosv1beta1.SveltosCluster{Status: tc.status}

			got := GetSveltosClusterReadyCondition(cd, sc)

			if got.Type != kcmv1.SveltosClusterReadyCondition {
				t.Errorf("Type = %q, want %q", got.Type, kcmv1.SveltosClusterReadyCondition)
			}
			if got.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.ObservedGeneration != generation {
				t.Errorf("ObservedGeneration = %d, want %d", got.ObservedGeneration, generation)
			}
			for _, substr := range tc.wantMsgSubstrs {
				if !strings.Contains(got.Message, substr) {
					t.Errorf("Message = %q, want substring %q", got.Message, substr)
				}
			}
		})
	}
}

// Sveltos loads the kubeconfig through a randomly-named temp file and puts that name in the failure
// message, so two probes of the same broken cluster report different strings. The condition message
// has to come out identical anyway, otherwise every probe looks like a state change and drives an
// endless loop of status updates, reconciles and duplicate events.
func TestGetSveltosClusterReadyCondition_stableAcrossProbes(t *testing.T) {
	t.Parallel()

	const failure = `BuildConfigFromFlags: error loading config file %q: couldn't get version/kind; json parse error`

	cd := &kcmv1.ClusterDeployment{}

	first := GetSveltosClusterReadyCondition(cd, &libsveltosv1beta1.SveltosCluster{
		Status: libsveltosv1beta1.SveltosClusterStatus{
			ConnectionStatus:   libsveltosv1beta1.ConnectionDown,
			ConnectionFailures: 3,
			FailureMessage:     new(fmt.Sprintf(failure, "/tmp/kubeconfig2139820100")),
		},
	})
	second := GetSveltosClusterReadyCondition(cd, &libsveltosv1beta1.SveltosCluster{
		Status: libsveltosv1beta1.SveltosClusterStatus{
			ConnectionStatus:   libsveltosv1beta1.ConnectionDown,
			ConnectionFailures: 4, // the retry counter moves on; the reported problem does not
			FailureMessage:     new(fmt.Sprintf(failure, "/tmp/kubeconfig1188603431")),
		},
	})

	if first.Message != second.Message {
		t.Errorf("message differs between probes of the same failure:\n first  = %q\n second = %q", first.Message, second.Message)
	}
	// the diagnostic itself must survive the scrubbing
	if !strings.Contains(first.Message, "couldn't get version/kind") {
		t.Errorf("Message = %q, want it to retain the underlying error", first.Message)
	}
}

func TestStableFailureMessage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ in, want string }{
		"temp kubeconfig suffix stripped": {
			in:   `error loading config file "/tmp/kubeconfig2139820100": bad`,
			want: `error loading config file "/tmp/kubeconfig": bad`,
		},
		"surrounding whitespace trimmed": {
			in:   "  dial tcp 192.0.2.1:6443: i/o timeout\n",
			want: "dial tcp 192.0.2.1:6443: i/o timeout",
		},
		"unrelated numbers preserved": {
			in:   `Get "https://192.0.2.1:6443/version?timeout=32s": i/o timeout`,
			want: `Get "https://192.0.2.1:6443/version?timeout=32s": i/o timeout`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := stableFailureMessage(tc.in); got != tc.want {
				t.Errorf("stableFailureMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
