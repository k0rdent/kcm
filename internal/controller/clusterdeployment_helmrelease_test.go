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

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	conditionsutil "github.com/K0rdent/kcm/internal/util/conditions"
)

func Test_helmReleaseReadyCondition(t *testing.T) {
	t.Parallel()

	const cdGeneration = 7

	hrWith := func(generation, statusObserved int64, ready *metav1.Condition) *helmcontrollerv2.HelmRelease {
		hr := &helmcontrollerv2.HelmRelease{
			Name: "cd", Namespace: "ns", Generation: generation,
		}
		hr.Status.ObservedGeneration = statusObserved
		if ready != nil {
			hr.Status.Conditions = []metav1.Condition{*ready}
		}
		return hr
	}
	ready := func(status metav1.ConditionStatus, reason, msg string, observed int64) *metav1.Condition {
		return &metav1.Condition{
			Type:               fluxmeta.ReadyCondition,
			Status:             status,
			Reason:             reason,
			Message:            msg,
			ObservedGeneration: observed,
		}
	}

	tests := map[string]struct {
		hr           *helmcontrollerv2.HelmRelease
		wantNil      bool
		wantObserved bool
		wantStatus   metav1.ConditionStatus
		wantReason   string
		wantMessage  string
		wantCDReady  metav1.ConditionStatus
	}{
		"status not observed yet, stale Ready=True -> Unknown": {
			hr:          hrWith(3, 2, ready(metav1.ConditionTrue, "UpgradeSucceeded", "ok", 2)),
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  kcmv1.ProgressingReason,
			wantCDReady: metav1.ConditionUnknown,
		},
		"status observed, Ready condition lags -> Unknown": {
			hr:          hrWith(3, 3, ready(metav1.ConditionTrue, "UpgradeSucceeded", "ok", 2)),
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  kcmv1.ProgressingReason,
			wantCDReady: metav1.ConditionUnknown,
		},
		"stale Ready=False -> Unknown, not Failed": {
			hr:          hrWith(3, 2, ready(metav1.ConditionFalse, "UpgradeFailed", "boom", 2)),
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  kcmv1.ProgressingReason,
			wantCDReady: metav1.ConditionUnknown,
		},
		"just created, no status -> Unknown": {
			hr:          hrWith(1, 0, nil),
			wantStatus:  metav1.ConditionUnknown,
			wantReason:  kcmv1.ProgressingReason,
			wantCDReady: metav1.ConditionUnknown,
		},
		"caught up, Ready=True -> copied": {
			hr:           hrWith(3, 3, ready(metav1.ConditionTrue, "UpgradeSucceeded", "ok", 3)),
			wantObserved: true,
			wantStatus:   metav1.ConditionTrue,
			wantReason:   "UpgradeSucceeded",
			wantMessage:  "ok",
			wantCDReady:  metav1.ConditionTrue,
		},
		"caught up, UpgradeFailed -> copied": {
			hr:           hrWith(3, 3, ready(metav1.ConditionFalse, "UpgradeFailed", "boom", 3)),
			wantObserved: true,
			wantStatus:   metav1.ConditionFalse,
			wantReason:   "UpgradeFailed",
			wantMessage:  "boom",
			wantCDReady:  metav1.ConditionFalse,
		},
		"caught up, InstallFailed -> copied": {
			hr:           hrWith(1, 1, ready(metav1.ConditionFalse, "InstallFailed", "boom", 1)),
			wantObserved: true,
			wantStatus:   metav1.ConditionFalse,
			wantReason:   "InstallFailed",
			wantMessage:  "boom",
			wantCDReady:  metav1.ConditionFalse,
		},
		"caught up, no Ready condition -> nil": {
			hr:           hrWith(2, 2, nil),
			wantNil:      true,
			wantObserved: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, observed := helmReleaseReadyCondition(tc.hr, cdGeneration)
			if observed != tc.wantObserved {
				t.Errorf("observed = %v, want %v", observed, tc.wantObserved)
			}
			if tc.wantNil {
				if got != nil {
					t.Fatalf("condition = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("condition = nil, want non-nil")
			}
			if got.Type != kcmv1.HelmReleaseReadyCondition {
				t.Errorf("type = %q, want %q", got.Type, kcmv1.HelmReleaseReadyCondition)
			}
			if got.Status != tc.wantStatus || got.Reason != tc.wantReason {
				t.Errorf("status/reason = %s/%s, want %s/%s", got.Status, got.Reason, tc.wantStatus, tc.wantReason)
			}
			if tc.wantMessage != "" && got.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", got.Message, tc.wantMessage)
			}
			if got.ObservedGeneration != cdGeneration {
				t.Errorf("observedGeneration = %d, want %d", got.ObservedGeneration, cdGeneration)
			}

			// The ClusterDeployment Ready condition must follow: Unknown while the HelmRelease lags,
			// never True or Failed.
			conds := conditionsutil.UpdateReadyCondition([]metav1.Condition{*got}, cdGeneration, handleClusterDeploymentFailedConditions)
			var cdReady *metav1.Condition
			for i := range conds {
				if conds[i].Type == kcmv1.ReadyCondition {
					cdReady = &conds[i]
				}
			}
			if cdReady == nil {
				t.Fatal("ClusterDeployment Ready condition not set")
			}
			if cdReady.Status != tc.wantCDReady {
				t.Errorf("ClusterDeployment Ready = %s (%s), want %s", cdReady.Status, cdReady.Reason, tc.wantCDReady)
			}
		})
	}
}
