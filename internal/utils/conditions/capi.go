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

package conditions

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capiconditions "sigs.k8s.io/cluster-api/util/conditions/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
)

type clusterConditionCustomMergeStrategy struct {
	negativePolarityConditionTypes []string
}

func (c clusterConditionCustomMergeStrategy) Merge(conditions []capiconditions.ConditionWithOwnerInfo, negativePolarityConditionTypes []string) (status metav1.ConditionStatus, reason, message string, err error) {
	status, reason, message, err = capiconditions.DefaultMergeStrategy(
		capiconditions.GetPriorityFunc(func(condition metav1.Condition) capiconditions.MergePriority {
			return capiconditions.GetDefaultMergePriorityFunc(c.negativePolarityConditionTypes...)(condition)
		}),
	).Merge(conditions, negativePolarityConditionTypes)
	for _, cond := range conditions {
		if cond.Type == clusterapiv1.ClusterDeletingV1Beta2Condition {
			reason = "Deleting"
			break
		}
	}
	return status, reason, message, err
}

func getCAPIConditionsSummaryOptions(conditionTypes capiconditions.ForConditionTypes, negativePolarityConditionTypes capiconditions.NegativePolarityConditionTypes) []capiconditions.SummaryOption {
	return []capiconditions.SummaryOption{
		conditionTypes,
		negativePolarityConditionTypes,
		capiconditions.CustomMergeStrategy{
			MergeStrategy: clusterConditionCustomMergeStrategy{
				negativePolarityConditionTypes: negativePolarityConditionTypes,
			},
		},
	}
}

func GetCAPIClusterSummaryCondition(cd *kcm.ClusterDeployment, cluster *clusterapiv1.Cluster) (*metav1.Condition, error) {
	var (
		capiConditionTypes             capiconditions.ForConditionTypes
		negativePolarityConditionTypes capiconditions.NegativePolarityConditionTypes
	)

	if cd.DeletionTimestamp.IsZero() {
		capiConditionTypes = append(capiConditionTypes,
			clusterapiv1.ClusterInfrastructureReadyV1Beta2Condition,
			clusterapiv1.ClusterControlPlaneInitializedV1Beta2Condition,
			clusterapiv1.ClusterControlPlaneAvailableV1Beta2Condition,
			clusterapiv1.ClusterControlPlaneMachinesReadyV1Beta2Condition,
			clusterapiv1.ClusterWorkersAvailableV1Beta2Condition,
			clusterapiv1.ClusterWorkerMachinesReadyV1Beta2Condition,
			clusterapiv1.ClusterRemoteConnectionProbeV1Beta2Condition,
		)
	} else {
		capiConditionTypes = capiconditions.ForConditionTypes{
			clusterapiv1.ClusterDeletingV1Beta2Condition,
		}
		// When the Cluster is being deleted, these conditions should have negative polarity to collect them if Status:True
		negativePolarityConditionTypes = []string{
			clusterapiv1.ClusterDeletingV1Beta2Condition,
		}
	}

	capiCondition, err := capiconditions.NewSummaryCondition(
		cluster,
		kcm.CAPIClusterSummaryCondition,
		getCAPIConditionsSummaryOptions(capiConditionTypes, negativePolarityConditionTypes)...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get condition summary from Cluster %s: %w", client.ObjectKeyFromObject(cluster), err)
	}
	capiCondition.ObservedGeneration = cd.Generation
	return capiCondition, nil
}
