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

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/record"
)

// GetStatusConditions returns a list of conditions from provided ClusterSummary.
func GetStatusConditions(summary *addoncontrollerv1beta1.ClusterSummary) (conditions []metav1.Condition, err error) {
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
		// The way ClusterSummary status works is that if there is an error encountered while
		// deploying any helm chart, that error is reported in .status.featureSummaries, e.g:
		//
		// status:
		// 	dependencies: no dependencies
		// 	featureSummaries:
		// 	- consecutiveFailures: 6
		// 		failureMessage: 'no cached repo found. (try ''helm repo update''): open /home/nonroot/.cache/helm/repository/ingress-nginx-index.yaml:
		// 			no such file or directory'
		// 		featureID: Helm
		// 		hash: 8UsfB9vZpjIceiTHIKYgrr+emLow23ABa5RxloaKZkQ=
		// 		lastAppliedTime: "2025-07-21T13:03:42Z"
		// 		status: Provisioning
		// 	helmReleaseSummaries:
		// 	- releaseName: kyverno
		// 		releaseNamespace: kyverno
		// 		status: Managing
		// 		valuesHash: Eq4yyx7ALQHto1gbEnwf7jsNxTVy7WuvI5choD2C4SY=
		// 	- releaseName: ingress-nginx
		// 		releaseNamespace: ingress-nginx
		// 		status: Managing
		//
		// If there are more than 1 helm charts being deployed, this presents a problem where it is not possible
		// to determine which chart the error is associated with. Sveltos deploys each chart chronologically in
		// the sequence it occurs in the spec. It will stop at the first failure (unless .spec.continueOnError is True)
		// and update .status.featureSummaries.failureMessage without identifying which chart the error is associated with.
		//
		// Due to this we can't determine which chart failed, but fortunately we can determine which chart was successfully
		// deployed by looking at the valuesHash field in status. Currently, the addon-controller will only set this field
		// if the chart was successfully deployed. For more detail, see:
		// https://github.com/k0rdent/kcm/issues/1693#issuecomment-3096989267
		//
		// TODO: We may change this logic if we submit a feature request to sveltos to make it possible
		// to associate error with chart and the feature gets implemented.
		if len(x.ValuesHash) == 0 {
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
			record.Eventf(object, generation, kcmv1.SveltosHelmReleaseReadyCondition, condition.Message)
		} else {
			record.Warnf(object, generation, kcmv1.SveltosHelmReleaseNotReadyReason, condition.Message)
		}
		return
	}

	// If here then the condition is related to one of the Sveltos Features (Resources, Helm, Kustomize).

	// This msg will render as:
	// 1. Sveltos feature Resources Provisioned on target cluster cluster-namespace/cluster-name
	// 2. Sveltos feature Helm FailedNonRetriable on target cluster cluster-namespace/cluster-name
	msg := fmt.Sprintf("Sveltos feature %s %s on target cluster %s", condition.Type, condition.Reason, targetCluster.String())
	if condition.Status == metav1.ConditionTrue {
		record.Eventf(object, generation, kcmv1.SveltosFeatureReadyReason, msg)
	} else {
		record.Eventf(object, generation, kcmv1.SveltosFeatureNotReadyReason, msg)
	}
}

// helmReleaseReadyConditionType returns a SveltosHelmReleaseReady
// type per service to be used in status conditions.
func helmReleaseReadyConditionType(releaseNamespace, releaseName string) string {
	return fmt.Sprintf(
		"%s.%s/%s",
		releaseNamespace,
		releaseName,
		kcmv1.SveltosHelmReleaseReadyCondition,
	)
}

func isHelmReleaseReadyConditionType(condition *metav1.Condition) bool {
	return strings.HasSuffix(condition.Type, kcmv1.SveltosHelmReleaseReadyCondition)
}

func helmReleaseConditionMessage(releaseNamespace, releaseName, conflictMsg string) string {
	msg := "Release " + releaseNamespace + "/" + releaseName
	if conflictMsg != "" {
		msg += ": " + conflictMsg
	}

	return msg
}
