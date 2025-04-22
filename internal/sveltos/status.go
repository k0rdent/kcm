// Copyright 2024
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

package sveltos

import (
	"errors"
	"fmt"
	"strings"

	sveltosv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/record"
)

// GetStatusConditions returns a list of conditions from provided ClusterSummary.
func GetStatusConditions(summary *sveltosv1beta1.ClusterSummary) (conditions []metav1.Condition, err error) {
	if summary == nil {
		return nil, errors.New("error getting status from ClusterSummary: nil summary provided")
	}

	for _, x := range summary.Status.FeatureSummaries {
		msg := ""
		status := metav1.ConditionTrue
		if x.FailureMessage != nil && *x.FailureMessage != "" {
			msg = *x.FailureMessage
			status = metav1.ConditionFalse
		}

		apimeta.SetStatusCondition(&conditions, metav1.Condition{
			Message: msg,
			Reason:  string(x.Status),
			Status:  status,
			Type:    string(x.FeatureID),
		})
	}

	for _, x := range summary.Status.HelmReleaseSummaries {
		status := metav1.ConditionTrue
		if x.ConflictMessage != "" {
			status = metav1.ConditionFalse
		}

		apimeta.SetStatusCondition(&conditions, metav1.Condition{
			Message: helmReleaseConditionMessage(x.ReleaseNamespace, x.ReleaseName, x.ConflictMessage),
			Reason:  string(x.Status),
			Status:  status,
			Type:    helmReleaseReadyConditionType(x.ReleaseNamespace, x.ReleaseName),
		})
	}

	return conditions, nil
}

// CreateEventFromCondition creates sveltos related kubernetes event from the provided condition.
func CreateEventFromCondition(object runtime.Object, generation int64, targetCluster client.ObjectKey, condition *metav1.Condition) {
	if isHelmReleaseReadyConditionType(condition) {
		if condition.Status == metav1.ConditionTrue {
			record.Eventf(object, generation, kcm.SveltosHelmReleaseReadyCondition, condition.Message)
		} else {
			record.Warnf(object, generation, kcm.SveltosHelmReleaseNotReadyReason, condition.Message)
		}
		return
	}

	// If here then the condition is related to one of the Sveltos Features (Resources, Helm, Kustomize).

	// This msg will render as:
	// 1. Sveltos feature Resources Provisioned on target cluster cluster-namespace/cluster-name
	// 2. Sveltos feature Helm FailedNonRetriable on target cluster cluster-namespace/cluster-name
	msg := fmt.Sprintf("Sveltos feature %s %s on target cluster %s", condition.Type, condition.Reason, targetCluster.String())
	if condition.Status == metav1.ConditionTrue {
		record.Eventf(object, generation, kcm.SveltosFeatureReadyReason, msg)
	} else {
		record.Eventf(object, generation, kcm.SveltosFeatureNotReadyReason, msg)
	}
}

// helmReleaseReadyConditionType returns a SveltosHelmReleaseReady
// type per service to be used in status conditions.
func helmReleaseReadyConditionType(releaseNamespace, releaseName string) string {
	return fmt.Sprintf(
		"%s.%s/%s",
		releaseNamespace,
		releaseName,
		kcm.SveltosHelmReleaseReadyCondition,
	)
}

func isHelmReleaseReadyConditionType(condition *metav1.Condition) bool {
	return strings.HasSuffix(condition.Type, kcm.SveltosHelmReleaseReadyCondition)
}

func helmReleaseConditionMessage(releaseNamespace, releaseName, conflictMsg string) string {
	msg := "Release " + releaseNamespace + "/" + releaseName
	if conflictMsg != "" {
		msg += ": " + conflictMsg
	}

	return msg
}
