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
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	conditionsutil "github.com/K0rdent/kcm/internal/util/conditions"
)

func Test_updateClusterDeploymentConditions(t *testing.T) {
	const generation = 2
	type testInput struct {
		name                   string
		currentConditions      []metav1.Condition
		expectedReadyCondition metav1.Condition
	}
	tests := []testInput{
		{
			name: "No conditions exist; should add Ready condition with Unknown status.",
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionUnknown,
				Reason:             kcmv1.ProgressingReason,
				ObservedGeneration: generation,
			},
		},
		{
			name: "Some conditions exist with Unknown status; should add Ready condition with Unknown status.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.CredentialReadyCondition,
					Status:  metav1.ConditionUnknown,
					Reason:  "CredentialStatusUnknown",
					Message: "Credential status is unknown",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionUnknown,
				Reason:             kcmv1.ProgressingReason,
				Message:            "Credential status is unknown",
				ObservedGeneration: generation,
			},
		},
		{
			name: "Credential and Template are not Ready; should reflect both errors in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.CredentialReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  "SomeCredentialError",
					Message: "Some error with credentials",
				},
				{
					Type:    kcmv1.TemplateReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  "SomeTemplateError",
					Message: "Some error with cluster template",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.FailedReason,
				Message:            "Some error with credentials. Some error with cluster template",
				ObservedGeneration: generation,
			},
		},
		{
			name: "ClusterDataSource and ClusterIdentity are not Ready; should reflect both errors in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.CredentialReadyCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.SucceededReason,
					Message: "Credential is ready",
				},
				{
					Type:    kcmv1.TemplateReadyCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.SucceededReason,
					Message: "ClusterTemplate is ready",
				},
				{
					Type:    kcmv1.ClusterDataSourceReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  kcmv1.FailedReason,
					Message: "Some error with cluster data source",
				},
				{
					Type:    kcmv1.ClusterAuthenticationReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  kcmv1.FailedReason,
					Message: "Some error with cluster authentication",
				},
				{
					Type:               kcmv1.CAPIClusterSummaryCondition,
					Status:             metav1.ConditionFalse,
					Reason:             "IssuesReported",
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-5 * time.Minute)},
					Message:            "InfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.FailedReason,
				Message:            "Some error with cluster data source. Some error with cluster authentication",
				ObservedGeneration: generation,
			},
		},
		{
			name: "CAPI Cluster is provisioning for 5 minutes; should reflect Progressing reason in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.ClusterAuthenticationReadyCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.SucceededReason,
					Message: "ClusterAuthentication is ready",
				},
				{
					Type:               kcmv1.CAPIClusterSummaryCondition,
					Status:             metav1.ConditionFalse,
					Reason:             "IssuesReported",
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-5 * time.Minute)},
					Message:            "InfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.ProgressingReason,
				Message:            "InfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				ObservedGeneration: generation,
			},
		},
		{
			name: "CAPI Cluster is provisioning for more than 30 minutes; should reflect Failed reason in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:               kcmv1.CAPIClusterSummaryCondition,
					Status:             metav1.ConditionFalse,
					Reason:             "IssuesReported",
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-40 * time.Minute)},
					Message:            "InfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.FailedReason,
				Message:            "Cluster is not ready. Check the provider logs for more details.\nInfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				ObservedGeneration: generation,
			},
		},
		{
			name: "Cluster is ready, should reflect Succeeded reason in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.CAPIClusterSummaryCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.SucceededReason,
					Message: "Cluster is ready",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionTrue,
				Reason:             kcmv1.SucceededReason,
				Message:            "Object is ready",
				ObservedGeneration: generation,
			},
		},
		{
			name: "Cluster is deleting, Ready condition should be equal to Deleting condition",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.DeletingCondition,
					Status:  metav1.ConditionTrue,
					Reason:  "IssuesReported",
					Message: "Some error with cluster deletion",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.DeletingReason,
				Message:            "Some error with cluster deletion",
				ObservedGeneration: generation,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := conditionsutil.UpdateReadyCondition(tt.currentConditions, generation, handleClusterDeploymentFailedConditions)
			checkReadyCondition(t, result, tt.expectedReadyCondition)
		})
	}
}

// checkReadyCondition verifies if the Ready condition in the list of all conditions matches the expected Ready condition.
func checkReadyCondition(t *testing.T, conditions []metav1.Condition, expectedReadyCondition metav1.Condition) {
	t.Helper()

	for _, cond := range conditions {
		if cond.Type == kcmv1.ReadyCondition {
			if cond.Status != expectedReadyCondition.Status || cond.Reason != expectedReadyCondition.Reason ||
				cond.Message != expectedReadyCondition.Message || cond.ObservedGeneration != expectedReadyCondition.ObservedGeneration {
				printCondition := func(c metav1.Condition) string {
					return fmt.Sprintf("{Status: %s, Reason: %s, Message: %s, ObservedGeneration: %d}", c.Status, c.Reason, c.Message, c.ObservedGeneration)
				}
				t.Errorf("Ready condition does not match expected.\nGot: %s,\nWant: %s", printCondition(cond), printCondition(expectedReadyCondition))
			}
			return
		}
	}
	t.Errorf("Ready condition not found")
}
