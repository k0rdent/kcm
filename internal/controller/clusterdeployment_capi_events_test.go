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

package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func Test_capiClusterEvent(t *testing.T) {
	t.Parallel()

	summary := func(status metav1.ConditionStatus, reason, message string, generation int64) *metav1.Condition {
		return &metav1.Condition{
			Type:               kcmv1.CAPIClusterSummaryCondition,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: generation,
		}
	}

	tests := map[string]struct {
		previous   *metav1.Condition
		current    *metav1.Condition
		deleting   bool
		wantReason string
	}{
		"first condition, not ready -> provisioning": {
			current:    summary(metav1.ConditionFalse, "IssuesReported", "* InfrastructureReady: waiting", 1),
			wantReason: "CAPIClusterIsProvisioning",
		},
		"first condition, ready -> ready": {
			current:    summary(metav1.ConditionTrue, "InfoReported", "", 1),
			wantReason: "CAPIClusterIsReady",
		},
		"message-only change while provisioning -> no event": {
			previous: summary(metav1.ConditionFalse, "IssuesReported", "* InfrastructureReady: waiting", 1),
			current:  summary(metav1.ConditionFalse, "IssuesReported", "* ControlPlaneAvailable: waiting", 1),
		},
		"observed generation change on a ready cluster -> no event": {
			previous: summary(metav1.ConditionTrue, "InfoReported", "", 1),
			current:  summary(metav1.ConditionTrue, "InfoReported", "", 2),
		},
		"not ready -> ready": {
			previous:   summary(metav1.ConditionFalse, "IssuesReported", "* WorkersAvailable: waiting", 2),
			current:    summary(metav1.ConditionTrue, "InfoReported", "", 2),
			wantReason: "CAPIClusterIsReady",
		},
		"ready -> not ready": {
			previous:   summary(metav1.ConditionTrue, "InfoReported", "", 2),
			current:    summary(metav1.ConditionFalse, "IssuesReported", "* ControlPlaneMachinesReady: rolling out", 2),
			wantReason: "CAPIClusterIsProvisioning",
		},
		"reason change while not ready -> provisioning": {
			previous:   summary(metav1.ConditionFalse, "UnknownReported", "* InfrastructureReady: unknown", 1),
			current:    summary(metav1.ConditionFalse, "IssuesReported", "* InfrastructureReady: waiting", 1),
			wantReason: "CAPIClusterIsProvisioning",
		},
		"deletion starts on a not-ready cluster -> deleting": {
			previous:   summary(metav1.ConditionFalse, "IssuesReported", "* WorkersAvailable: waiting", 1),
			current:    summary(metav1.ConditionFalse, kcmv1.DeletingReason, "* Deleting: deleting machines", 1),
			deleting:   true,
			wantReason: "CAPIClusterIsDeleting",
		},
		"message-only change while deleting -> no event": {
			previous: summary(metav1.ConditionFalse, kcmv1.DeletingReason, "* Deleting: deleting machines", 1),
			current:  summary(metav1.ConditionFalse, kcmv1.DeletingReason, "* Deleting: deleting control plane", 1),
			deleting: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reason, message, ok := capiClusterEvent(tt.previous, tt.current, tt.deleting)
			if tt.wantReason == "" {
				if ok {
					t.Fatalf("got event %q (%q), want none", reason, message)
				}
				return
			}
			if !ok {
				t.Fatalf("got no event, want %q", tt.wantReason)
			}
			if reason != tt.wantReason {
				t.Fatalf("got event %q, want %q", reason, tt.wantReason)
			}
			if message == "" {
				t.Fatalf("event %q has an empty message", reason)
			}
		})
	}
}
