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

// Package adapterutil holds what every KSM adapter has to do the same way.
// A ServiceSet's conditions, its cluster reference and the services it never
// got to are the ServiceSet's own contract, not the provider's, so they must
// not drift between adapters.
package adapterutil

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/serviceset"
)

// ClusterReference is the object the ServiceSet's workloads land on.
func ClusterReference(serviceSet *kcmv1.ServiceSet) *corev1.ObjectReference {
	if serviceSet.Spec.Provider.SelfManagement {
		return serviceset.SelfManagementClusterReference()
	}
	return &corev1.ObjectReference{
		Kind:       kcmv1.ClusterDeploymentKind,
		Name:       serviceSet.Spec.Cluster,
		Namespace:  serviceSet.Namespace,
		APIVersion: kcmv1.GroupVersion.WithKind(kcmv1.ClusterDeploymentKind).GroupVersion().String(),
	}
}

// FillNotDeployedServices gives a service that is in the spec but has no state
// yet an explicit Pending one, so that a ServiceSet never reports less than it
// was asked for.
func FillNotDeployedServices(serviceSet *kcmv1.ServiceSet, now func() time.Time) {
	known := make(map[client.ObjectKey]struct{}, len(serviceSet.Status.Services))
	for _, service := range serviceSet.Status.Services {
		known[client.ObjectKey{Namespace: service.Namespace, Name: service.Name}] = struct{}{}
	}

	for _, service := range serviceSet.Spec.Services {
		key := client.ObjectKey{Namespace: service.Namespace, Name: service.Name}
		if _, ok := known[key]; ok {
			continue
		}
		serviceSet.Status.Services = append(serviceSet.Status.Services, kcmv1.ServiceState{
			Name:                    service.Name,
			Namespace:               service.Namespace,
			Template:                service.Template,
			Type:                    kcmv1.ServiceTypeHelm,
			State:                   kcmv1.ServiceStateNotDeployed,
			LastStateTransitionTime: new(metav1.NewTime(now())),
		})
	}
}

// LabelsMatchSelector reports whether a ServiceSet's labels satisfy its
// provider's selector. A nil selector matches everything.
func LabelsMatchSelector(serviceSetLabels map[string]string, selector *metav1.LabelSelector) (bool, error) {
	if selector == nil {
		return true, nil
	}
	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return false, fmt.Errorf("failed to convert selector: %w", err)
	}
	return labelSelector.Matches(labels.Set(serviceSetLabels)), nil
}

// FindCondition returns the ServiceSet's condition of that type, or an Unknown
// one to start from.
func FindCondition(serviceSet *kcmv1.ServiceSet, conditionType string) metav1.Condition {
	condition := apimeta.FindStatusCondition(serviceSet.Status.Conditions, conditionType)
	if condition == nil {
		return metav1.Condition{
			Type:   conditionType,
			Status: metav1.ConditionUnknown,
		}
	}
	return *condition
}

// UpdateCondition sets the condition on the ServiceSet and reports whether
// anything changed.
func UpdateCondition(
	serviceSet *kcmv1.ServiceSet,
	condition metav1.Condition,
	status metav1.ConditionStatus,
	reason, message string,
	now time.Time,
) bool {
	condition.Status = status
	condition.Reason = reason
	condition.Message = message
	condition.ObservedGeneration = serviceSet.Generation
	condition.LastTransitionTime = metav1.NewTime(now)
	return apimeta.SetStatusCondition(&serviceSet.Status.Conditions, condition)
}

// ConditionStatusChangedToFalse reports whether a condition has just gone
// false, which is when a failure is worth an event rather than every round.
func ConditionStatusChangedToFalse(conditionOldState, conditionNewState *metav1.Condition) bool {
	if conditionNewState == nil {
		return false
	}
	if conditionOldState == nil {
		return conditionNewState.Status == metav1.ConditionFalse
	}
	return conditionOldState.Status != conditionNewState.Status && conditionNewState.Status == metav1.ConditionFalse
}

// UpdateServicesInReadyStateCondition restates the "<deployed>/<total>" the
// ServiceSet and MultiClusterService columns show.
func UpdateServicesInReadyStateCondition(serviceSet *kcmv1.ServiceSet, now time.Time) {
	deployedCount := 0
	for _, svc := range serviceSet.Status.Services {
		if svc.State == kcmv1.ServiceStateDeployed {
			deployedCount++
		}
	}
	totalCount := len(serviceSet.Spec.Services)

	// Only when deployed <= total: a service removed from the spec can still
	// be in the status for a round, and "3/2" helps nobody.
	if deployedCount > totalCount {
		return
	}

	svcReadyStatus := metav1.ConditionTrue
	if deployedCount != totalCount {
		svcReadyStatus = metav1.ConditionFalse
	}
	UpdateCondition(serviceSet, FindCondition(serviceSet, kcmv1.ServicesInReadyStateCondition),
		svcReadyStatus, kcmv1.ServicesInReadyStateCondition,
		fmt.Sprintf("%d/%d", deployedCount, totalCount), now)
}
