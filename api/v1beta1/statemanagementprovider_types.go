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
	// StateManagementProviderRBACNotReadyReason indicates the reason for the rbac is not ready
	StateManagementProviderRBACNotReadyReason = "RBACNotReady"
	// StateManagementProviderRBACFailedToGetGVKForAdapterReason indicates the reason for the rbac failed to get gvk for adapter
	StateManagementProviderRBACFailedToGetGVKForAdapterReason = "FailedToGetGVKForAdapter"
	// StateManagementProviderRBACFailedToGetGVKForProvisionerReason indicates the reason for the rbac failed to get gvk for provisioner
	StateManagementProviderRBACFailedToGetGVKForProvisionerReason = "FailedToGetGVKForProvisioner"
	// StateManagementProviderRBACFailedToEnsureClusterRoleReason indicates the reason for the rbac failed to ensure cluster role
	StateManagementProviderRBACFailedToEnsureClusterRoleReason = "FailedToEnsureClusterRole"
	// StateManagementProviderRBACFailedToEnsureClusterRoleBindingReason indicates the reason for the rbac failed to ensure cluster role binding
	StateManagementProviderRBACFailedToEnsureClusterRoleBindingReason = "FailedToEnsureClusterRoleBinding"
	// StateManagementProviderRBACFailedToEnsureServiceAccountReason indicates the reason for the rbac failed to ensure service account
	StateManagementProviderRBACFailedToEnsureServiceAccountReason = "FailedToEnsureServiceAccount"
	// StateManagementProviderRBACNotReadyMessage indicates the message for the rbac is not ready
	StateManagementProviderRBACNotReadyMessage = "rbac not ready"
	// StateManagementProviderRBACReadyReason indicates the reason for the rbac readiness
	StateManagementProviderRBACReadyReason = "RBACEnsuredSuccessfully"
	// StateManagementProviderRBACReadyMessage indicates the message for the rbac readiness
	StateManagementProviderRBACReadyMessage = "Successfully ensured RBAC"
	// StateManagementProviderRBACUnknownReason indicates the reason for the rbac unknown
	StateManagementProviderRBACUnknownReason = "RBACUnknown"

	// StateManagementProviderAdapterCondition indicates the status of the adapter
	StateManagementProviderAdapterCondition = "AdapterReady"
	// StateManagementProviderAdapterNotReadyReason indicates the reason for the adapter is not ready
	StateManagementProviderAdapterNotReadyReason = "AdapterNotReady"
	// StateManagementProviderAdapterNotReadyMessage indicates the message for the adapter is not ready
	StateManagementProviderAdapterNotReadyMessage = "adapter not ready"
	// StateManagementProviderAdapterReadyReason indicates the reason for the adapter readiness
	StateManagementProviderAdapterReadyReason = "AdapterEnsuredSuccessfully"
	// StateManagementProviderAdapterReadyMessage indicates the message for the adapter readiness
	StateManagementProviderAdapterReadyMessage = "Successfully ensured adapter"
	// StateManagementProviderAdapterUnknownReason indicates the reason for the adapter unknown
	StateManagementProviderAdapterUnknownReason = "AdapterUnknown"

	// StateManagementProviderProvisionerCondition indicates the status of the provisioners
	StateManagementProviderProvisionerCondition = "ProvisionerReady"
	// StateManagementProviderProvisionerNotReadyReason indicates the reason for the provisioner is not ready
	StateManagementProviderProvisionerNotReadyReason = "ProvisionerNotReady"
	// StateManagementProviderProvisionerFailedMessage indicates the message for the provisioner is not ready
	StateManagementProviderProvisionerFailedMessage = "provisioner not ready"
	// StateManagementProviderProvisionerReadyReason indicates the reason for the provisioner readiness
	StateManagementProviderProvisionerReadyReason = "ProvisionerEnsuredSuccessfully"
	// StateManagementProviderProvisionerReadyMessage indicates the message for the provisioner readiness
	StateManagementProviderProvisionerReadyMessage = "Successfully ensured provisioner"
	// StateManagementProviderProvisionerUnknownReason indicates the reason for the rbac unknown
	StateManagementProviderProvisionerUnknownReason = "ProvisionerUnknown"

	// StateManagementProviderProvisionerCRDsCondition indicates the status of the ProvisionerCRDs
	StateManagementProviderProvisionerCRDsCondition = "ProvisionerCRDsReady"
	// StateManagementProviderProvisionerCRDsNotReadyReason indicates the reason for the ProvisionerCRDs are not ready
	StateManagementProviderProvisionerCRDsNotReadyReason = "ProvisionerCRDsNotReady"
	// StateManagementProviderProvisionerCRDsNotReadyMessage indicates the message for the ProvisionerCRDs are not ready
	StateManagementProviderProvisionerCRDsNotReadyMessage = "provisioner CRDs not ready"
	// StateManagementProviderProvisionerCRDsReadyReason indicates the reason for the ProvisionerCRDs readiness
	StateManagementProviderProvisionerCRDsReadyReason = "ProvisionerCRDsEnsuredSuccessfully"
	// StateManagementProviderProvisionerCRDsReadyMessage indicates the message for the ProvisionerCRDs readiness
	StateManagementProviderProvisionerCRDsReadyMessage = "Successfully ensured provisioner CRDs"
	// StateManagementProviderProvisionerCRDsUnknownReason indicates the reason for the ProvisionerCRDs unknown
	StateManagementProviderProvisionerCRDsUnknownReason = "ProvisionerCRDsUnknown"

	// StateManagementProviderFailedToGetResourceReason indicates the reason for the failure
	// due to the failure to get the resource
	StateManagementProviderFailedToGetResourceReason = "FailedToGetAdapter"
	// StateManagementProviderFailedToEvaluateReadinessReason indicates the reason for the failure
	// due to the failure to evaluate the readiness
	StateManagementProviderFailedToEvaluateReadinessReason = "FailedToEvaluateReadiness"

	// StateManagementProviderFailedRBACEvent indicates the event for the RBAC failure
	StateManagementProviderFailedRBACEvent = "FailedToEnsureRBAC"
	// StateManagementProviderSuccessRBACEvent indicates the event for the RBAC success
	StateManagementProviderSuccessRBACEvent = "SuccessfullyEnsuredRBAC"
	// StateManagementProviderFailedAdapterEvent indicates the event for the adapter failure
	StateManagementProviderFailedAdapterEvent = "FailedToEnsureAdapter"
	// StateManagementProviderSuccessAdapterEvent indicates the event for the adapter success
	StateManagementProviderSuccessAdapterEvent = "SuccessfullyEnsuredAdapter"
	// StateManagementProviderFailedProvisionerEvent indicates the event for the provisioners failure
	StateManagementProviderFailedProvisionerEvent = "FailedToEnsureProvisioner"
	// StateManagementProviderSuccessProvisionerEvent indicates the event for the provisioners success
	StateManagementProviderSuccessProvisionerEvent = "SuccessfullyEnsuredProvisioner"
	// StateManagementProviderFailedProvisionerCRDsEvent indicates the event for the ProvisionerCRDs failure
	StateManagementProviderFailedProvisionerCRDsEvent = "FailedToEnsureProvisionerCRDs"
	// StateManagementProviderSuccessProvisionerCRDsEvent indicates the event for the ProvisionerCRDs success
	StateManagementProviderSuccessProvisionerCRDsEvent = "SuccessfullyEnsuredProvisionerCRDs"
)

// StateManagementProviderSpec defines the desired state of StateManagementProvider
type StateManagementProviderSpec struct {
	// Suspend suspends the StateManagementProvider. Suspending a StateManagementProvider
	// will prevent the adapter from reconciling any resources.
	// +kubebuilder:default=false
	// +required
	Suspend bool `json:"suspend"`

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

	// Ready is true if the state management provider is valid
	Ready bool `json:"ready"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=smp
// +kubebuilder:printcolumn:name="rbac",type="string",JSONPath=`.status.conditions[?(@.type=="RBACReady")].status`,description="Shows readiness of RBAC objects",priority=0
// +kubebuilder:printcolumn:name="adapter",type="string",JSONPath=`.status.conditions[?(@.type=="AdapterReady")].status`,description="Shows readiness of adapter",priority=0
// +kubebuilder:printcolumn:name="provisioner",type="string",JSONPath=`.status.conditions[?(@.type=="ProvisionerReady")].status`,description="Shows readiness of provisioner",priority=0
// +kubebuilder:printcolumn:name="provisioner crds",type="string",JSONPath=`.status.conditions[?(@.type=="ProvisionerCRDsReady")].status`,description="Shows readiness of required custom resources",priority=0
// +kubebuilder:printcolumn:name="ready",type="boolean",JSONPath=".status.ready",description="Shows readiness of provider",priority=0
// +kubebuilder:printcolumn:name="suspended",type="boolean",JSONPath=".spec.suspend",description="Shows whether provider is suspended",priority=0
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
