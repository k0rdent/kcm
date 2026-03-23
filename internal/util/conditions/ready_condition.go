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
	"slices"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func UpdateReadyCondition(conditions []metav1.Condition, handleFalseConditionFunc func(metav1.Condition) (string, string)) []metav1.Condition {
	// Check if the object is being deleted first
	deletingIdx := slices.IndexFunc(conditions, func(c metav1.Condition) bool {
		return c.Type == kcmv1.DeletingCondition
	})
	if deletingIdx >= 0 {
		apimeta.SetStatusCondition(&conditions, metav1.Condition{
			Type:    kcmv1.ReadyCondition,
			Status:  conditions[deletingIdx].Status,
			Reason:  conditions[deletingIdx].Reason,
			Message: conditions[deletingIdx].Message,
		})
		return conditions
	}

	// errs: critical failures that make the object not ready
	// warnings: transient issues where the object may still be provisioning
	// unknownMsgs: messages from conditions in Unknown status
	var errs, warnings, unknownMsgs []string
	for _, cond := range conditions {
		if cond.Type == kcmv1.ReadyCondition {
			continue
		}

		if cond.Type == kcmv1.PausedCondition {
			// If True and Paused, the cluster is paused and thus is not ready
			if cond.Status == metav1.ConditionTrue && cond.Reason == kcmv1.PausedReason {
				errs = append(errs, cond.Message)
			}
			// If False and NotPaused, that's normal operation - no need to include in status
			continue
		}
		switch cond.Status {
		case metav1.ConditionTrue:
			// Do nothing
		case metav1.ConditionUnknown:
			unknownMsgs = append(unknownMsgs, cond.Message)
		case metav1.ConditionFalse:
			errMsg, warningMsg := handleFalseConditionFunc(cond)
			if errMsg != "" {
				errs = append(errs, errMsg)
			}
			if warningMsg != "" {
				warnings = append(warnings, warningMsg)
			}
		}
	}

	ready := buildReadyCondition(errs, warnings, unknownMsgs, len(conditions))
	apimeta.SetStatusCondition(&conditions, ready)

	return conditions
}

// buildReadyCondition constructs the aggregate Ready condition from collected errors, unknown messages,
// and warnings. Priority: errors > warnings > unknown > all-ready
func buildReadyCondition(errs, warnings, unknownMsgs []string, totalConditions int) metav1.Condition {
	switch {
	case len(errs) > 0:
		return metav1.Condition{
			Type:    kcmv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  kcmv1.FailedReason,
			Message: strings.Join(errs, ". "),
		}
	case len(warnings) > 0:
		return metav1.Condition{
			Type:    kcmv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  kcmv1.ProgressingReason,
			Message: strings.Join(warnings, ". "),
		}
	case totalConditions == 0 || len(unknownMsgs) > 0:
		return metav1.Condition{
			Type:    kcmv1.ReadyCondition,
			Status:  metav1.ConditionUnknown,
			Reason:  kcmv1.ProgressingReason,
			Message: strings.Join(unknownMsgs, ". "),
		}
	default:
		return metav1.Condition{
			Type:    kcmv1.ReadyCondition,
			Status:  metav1.ConditionTrue,
			Reason:  kcmv1.SucceededReason,
			Message: "Object is ready",
		}
	}
}
