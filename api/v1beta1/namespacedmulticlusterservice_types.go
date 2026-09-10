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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// NamespacedMultiClusterServiceFinalizer is finalizer applied to NamespacedMultiClusterService objects.
	NamespacedMultiClusterServiceFinalizer = "k0rdent.mirantis.com/namespaced-multicluster-service"
	// NamespacedMultiClusterServiceKind is the string representation of a NamespacedMultiClusterServiceKind.
	NamespacedMultiClusterServiceKind = "NamespacedMultiClusterService"
)

const (
	// NamespacedMultiClusterServiceDependencyValidationCondition defines the condition
	// of NamespacedMultiClusterService dependencies.
	NamespacedMultiClusterServiceDependencyValidationCondition = "NamespacedMultiClusterServiceDependencyValidation"

	// NamespacedMultiClusterServiceDependencyReadyCondition defines the condition of whether every
	// NamespacedMultiClusterService this one depends on has finished deploying its services to
	// every cluster this NamespacedMultiClusterService matches.
	NamespacedMultiClusterServiceDependencyReadyCondition = "NamespacedMultiClusterServiceDependencyReady"
)

// Reasons are provided as utility, and not part of the declarative API.
const (
	// NamespacedMultiClusterServiceDependencyNotReadyReason signals that this NamespacedMultiClusterService
	// is waiting for a NamespacedMultiClusterService it depends on to deploy its services to one or more
	// matching clusters.
	NamespacedMultiClusterServiceDependencyNotReadyReason = "NamespacedMultiClusterServiceDependencyNotReady"
	// NamespacedMultiClusterServiceDependencyCheckFailedReason signals that an unexpected error prevented
	// this NamespacedMultiClusterService from determining whether its NamespacedMultiClusterService
	// dependencies are ready on one or more matching clusters.
	NamespacedMultiClusterServiceDependencyCheckFailedReason = "NamespacedMultiClusterServiceDependencyCheckFailed"
)

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=nmcs
// +kubebuilder:printcolumn:name="Clusters",type="string",JSONPath=`.status.conditions[?(@.type=="ClusterInReadyState")].message`,description="Number of ready out of total selected clusters",priority=0
// +kubebuilder:printcolumn:name="provider",type=string,JSONPath=`.spec.serviceSpec.provider.name`,description="StateManagementProvider name",priority=0
// +kubebuilder:printcolumn:name="self-management",type=boolean,JSONPath=`.spec.serviceSpec.provider.selfManagement`,description="Is the NamespacedMultiClusterService for self-management",priority=0
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="Time elapsed since object creation",priority=0

// NamespacedMultiClusterService is the Schema for the namespacedmulticlusterservices API.
// It is the namespace-scoped counterpart of [MultiClusterService] and deliberately shares that
// type's spec and status: the two objects describe the same desired and observed state, and only
// differ in scope. Should the two ever need to diverge, the shared types can be split then -
// doing so changes neither object's serialized form nor its CRD schema.
type NamespacedMultiClusterService struct { //nolint:govet // false-positive
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MultiClusterServiceSpec   `json:"spec,omitempty"`
	Status MultiClusterServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NamespacedMultiClusterServiceList contains a list of NamespacedMultiClusterService
type NamespacedMultiClusterServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NamespacedMultiClusterService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NamespacedMultiClusterService{}, &NamespacedMultiClusterServiceList{})
}

func (m *NamespacedMultiClusterService) GetObjectMeta() metav1.ObjectMeta {
	return m.ObjectMeta
}

func (m *NamespacedMultiClusterService) GetMultiClusterServiceSpec() *MultiClusterServiceSpec {
	return &m.Spec
}

func (m *NamespacedMultiClusterService) GetMultiClusterServiceStatus() *MultiClusterServiceStatus {
	return &m.Status
}

// GetFullname identifies this object across namespaces. It is not merely a display string:
// it is the value stored in a ServiceSet's .spec.namespacedMultiClusterService and the value
// matched by the ServiceSetNamespacedMultiClusterServiceIndexKey selector, so writer and
// reader must format it identically.
func (m *NamespacedMultiClusterService) GetFullname() string {
	return m.Namespace + "/" + m.Name
}
