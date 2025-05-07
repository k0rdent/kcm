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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// StateManagementProviderKind is the string representation of the StateManagementProviderKind
	StateManagementProviderKind = "StateManagementProvider"

	// StateManagementProviderRBACCondition indicates the status of the rbac
	StateManagementProviderRBACCondition = "RBACReady"
	// StateManagementProviderRBACFailedReason indicates the reason for the rbac failure
	StateManagementProviderRBACFailedReason = "RBACFailed"
	// StateManagementProviderRBACFailedMessage indicates the message for the rbac failure
	StateManagementProviderRBACFailedMessage = "rbac not ready"
	// StateManagementProviderRBACSuccessReason indicates the reason for the rbac success
	StateManagementProviderRBACSuccessReason = "RBACReady"
	// StateManagementProviderRBACSuccessMessage indicates the message for the rbac success
	StateManagementProviderRBACSuccessMessage = "rbac ready"

	// StateManagementProviderAdapterCondition indicates the status of the adapter
	StateManagementProviderAdapterCondition = "AdapterReady"
	// StateManagementProviderAdapterFailedReason indicates the reason for the adapter failure
	StateManagementProviderAdapterFailedReason = "AdapterFailed"
	// StateManagementProviderAdapterFailedMessage indicates the message for the adapter failure
	StateManagementProviderAdapterFailedMessage = "adapter not ready"
	// StateManagementProviderAdapterSuccessReason indicates the reason for the adapter success
	StateManagementProviderAdapterSuccessReason = "AdapterReady"
	// StateManagementProviderAdapterSuccessMessage indicates the message for the adapter success
	StateManagementProviderAdapterSuccessMessage = "adapter ready"

	// StateManagementProviderProvisionersCondition indicates the status of the provisioners
	StateManagementProviderProvisionersCondition = "ProvisionersReady"
	// StateManagementProviderProvisionersFailedReason indicates the reason for the provisioners failure
	StateManagementProviderProvisionersFailedReason = "ProvisionersFailed"
	// StateManagementProviderProvisionersFailedMessage indicates the message for the provisioners failure
	StateManagementProviderProvisionersFailedMessage = "provisioners not ready"
	// StateManagementProviderProvisionersSuccessReason indicates the reason for the provisioners success
	StateManagementProviderProvisionersSuccessReason = "ProvisionersReady"
	// StateManagementProviderProvisionersSuccessMessage indicates the message for the provisioners success
	StateManagementProviderProvisionersSuccessMessage = "provisioners ready"

	// StateManagementProviderProvisionerCRDsCondition indicates the status of the ProvisionerCRDs
	StateManagementProviderProvisionerCRDsCondition = "ProvisionerCRDsReady"
	// StateManagementProviderProvisionerCRDsFailedReason indicates the reason for the ProvisionerCRDs failure
	StateManagementProviderProvisionerCRDsFailedReason = "ProvisionerCRDsFailed"
	// StateManagementProviderProvisionerCRDsFailedMessage indicates the message for the ProvisionerCRDs failure
	StateManagementProviderProvisionerCRDsFailedMessage = "provisioner CRDs not ready"
	// StateManagementProviderProvisionerCRDsSuccessReason indicates the reason for the ProvisionerCRDs success
	StateManagementProviderProvisionerCRDsSuccessReason = "ProvisionerCRDsReady"
	// StateManagementProviderProvisionerCRDsSuccessMessage indicates the message for the ProvisionerCRDs success
	StateManagementProviderProvisionerCRDsSuccessMessage = "provisioner CRDs ready"

	// StateManagementProviderFailedRBACEvent indicates the event for the RBAC failure
	StateManagementProviderFailedRBACEvent = "FailedToEnsureRBAC"
	// StateManagementProviderFailedAdapterEvent indicates the event for the adapter failure
	StateManagementProviderFailedAdapterEvent = "FailedToEnsureAdapter"
	// StateManagementProviderFailedProvisionersEvent indicates the event for the provisioners failure
	StateManagementProviderFailedProvisionersEvent = "FailedToEnsureProvisioners"
	// StateManagementProviderFailedGVREvent indicates the event for the ProvisionerCRDs failure
	StateManagementProviderFailedGVREvent = "FailedToEnsureGVR"
)

// StateManagementProviderSpec defines the desired state of StateManagementProvider
type StateManagementProviderSpec struct {
	// Suspend suspends the StateManagementProvider
	Suspend bool `json:"suspend,omitempty"`

	// Adapter is an operator with translates the k0rdent API objects into provider-specific API objects.
	// It is represented as a reference to operator object
	Adapter ResourceReference `json:"adapter"`

	// Provisioner is a set of resources required for the provider to operate. These resources
	// reconcile provider-specific API objects. It is represented as a list of references to
	// provider's objects
	Provisioner []ResourceReference `json:"provisioner"`

	// ProvisionerCRDs is a set of references to provider-specific CustomResourceDefinition objects,
	// which are required for the provider to operate.
	ProvisionerCRDs []ProvisionerCRD `json:"provisionerCRDs"`
}

// ResourceReference is a cross-namespace reference to a resource
type ResourceReference struct {
	// APIVersion is the API version of the resource
	APIVersion string `json:"apiVersion"`

	// Kind is the kind of the resource
	Kind string `json:"kind"`

	// Name is the name of the resource
	Name string `json:"name"`

	// Namespace is the namespace of the resource
	Namespace string `json:"namespace"`

	// ReadinessRule is a CEL expression that evaluates to true when the resource is ready
	ReadinessRule string `json:"readinessRule"`
}

// ProvisionerCRD is a GVRs for a custom resource reconciled by provisioners
type ProvisionerCRD struct {
	// Group is the API group of the resources
	Group string `json:"group"`

	// Version is the API version of the resources
	Version string `json:"version"`

	// Resources is the list of resources under given APIVersion
	Resources []string `json:"resources"`
}

// StateManagementProviderStatus defines the observed state of StateManagementProvider
type StateManagementProviderStatus struct {
	// Conditions is a list of conditions for the state management provider
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Valid is true if the state management provider is valid
	Valid bool `json:"valid"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=smp
// +kubebuilder:printcolumn:name="rbac",type="string",JSONPath=`.status.conditions[?(@.type=="RBACReady")].status`,description="Shows readiness of RBAC objects",priority=0
// +kubebuilder:printcolumn:name="adapter",type="string",JSONPath=`.status.conditions[?(@.type=="AdapterReady")].status`,description="Shows readiness of adapter",priority=0
// +kubebuilder:printcolumn:name="provisioners",type="string",JSONPath=`.status.conditions[?(@.type=="ProvisionersReady")].status`,description="Shows readiness of provisioners",priority=0
// +kubebuilder:printcolumn:name="provisioner crds",type="string",JSONPath=`.status.conditions[?(@.type=="ProvisionerCRDsReady")].status`,description="Shows readiness of required custom resources",priority=0
// +kubebuilder:printcolumn:name="valid",type="boolean",JSONPath=".status.valid",description="Valid",priority=0
// +kubebuilder:printcolumn:name="suspended",type="boolean",JSONPath=".spec.suspend",description="Valid",priority=0
// +kubebuilder:printcolumn:name="age",type=date,JSONPath=`.metadata.creationTimestamp`,description="Time elapsed since object creation",priority=0

// StateManagementProvider is the Schema for the statemanagementproviders API
type StateManagementProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StateManagementProviderSpec   `json:"spec,omitempty"`
	Status StateManagementProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// StateManagementProviderList contains a list of StateManagementProvider
type StateManagementProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&StateManagementProvider{}, &StateManagementProviderList{})
}
