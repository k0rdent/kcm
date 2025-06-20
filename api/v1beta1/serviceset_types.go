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
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// ServiceSetKind is the string representation of the ServiceSet.
	ServiceSetKind = "ServiceSet"
	// ServiceSetFinalizer is the finalizer for ServiceSet
	ServiceSetFinalizer = "k0rdent.mirantis.com/service-set"

	// ServiceStateDeployed is the state when the Service is deployed
	ServiceStateDeployed = "Deployed"
	// ServiceStateFailed is the state when the Service is failed
	ServiceStateFailed = "Failed"
	// ServiceStateNotDeployed is the state when the Service is not deployed
	ServiceStateNotDeployed = "Pending"
	// ServiceStateProvisioning is the state when the Service is being provisioned
	ServiceStateProvisioning = "Provisioning"
	// ServiceStateDeleting is the state when the Service is being deleted
	ServiceStateDeleting = "Deleting"

	DriftIgnorePatch = `- op: add
  path: /metadata/annotations/projectsveltos.io~1driftDetectionIgnore
  value: ok`

	// ServiceSetProfileCondition is the condition type for ServiceSet profile
	ServiceSetProfileCondition = "ServiceSetProfile"
	// ServiceSetProfileNotReadyReason is the reason for the profile is not ready
	ServiceSetProfileNotReadyReason = "ServiceSetProfileNotReady"
	// ServiceSetProfileNotReadyMessage is the message for the profile is not ready
	ServiceSetProfileNotReadyMessage = "Profile is not ready"
	// ServiceSetProfileReadyReason is the reason for the profile is ready
	ServiceSetProfileReadyReason = "ServiceSetProfileReady"
	// ServiceSetProfileReadyMessage is the message for the profile is ready
	ServiceSetProfileReadyMessage = "Profile is ready"

	// ServiceSetProfileBuildFailedReason is the reason for the profile build failed
	ServiceSetProfileBuildFailedReason = "ServiceSetProfileBuildFailed"
	// ServiceSetHelmChartsBuildFailedReason is the reason for the Helm charts build failed
	ServiceSetHelmChartsBuildFailedReason = "ServiceSetHelmChartsBuildFailed"
	// ServiceSetKustomizationRefsBuildFailedReason is the reason for the Kustomization build failed
	ServiceSetKustomizationRefsBuildFailedReason = "ServiceSetKustomizationRefsBuildFailed"
	// ServiceSetPolicyRefsBuildFailedReason is the reason for the PolicyRefs build failed
	ServiceSetPolicyRefsBuildFailedReason = "ServiceSetPolicyRefsBuildFailed"

	// ServiceSetHelmChartsBuildFailedEvent indicates the event for Helm charts build failed
	ServiceSetHelmChartsBuildFailedEvent = "ServiceSetHelmChartsBuildFailed"
	// ServiceSetKustomizationRefsBuildFailedEvent indicates the event for Kustomization build failed
	ServiceSetKustomizationRefsBuildFailedEvent = "ServiceSetKustomizationBuildFailed"
	// ServiceSetPolicyRefsBuildFailedEvent indicates the event for PolicyRefs build failed
	ServiceSetPolicyRefsBuildFailedEvent = "ServiceSetPolicyRefsBuildFailed"
	// ServiceSetProfileBuildFailedEvent indicates the event for Profile build failed
	ServiceSetProfileBuildFailedEvent = "ServiceSetProfileBuildFailed"
	// ServiceSetEnsureProfileFailedEvent indicates the event for Profile create or update failed
	ServiceSetEnsureProfileFailedEvent = "ServiceSetEnsureProfileFailed"
	// ServiceSetEnsureProfileSuccessEvent indicates the event for Profile create or update succeeded
	ServiceSetEnsureProfileSuccessEvent = "ServiceSetEnsureProfileSuccess"
	// ServiceSetCollectServiceStatusesFailedEvent indicates the event for services status collection failed
	ServiceSetCollectServiceStatusesFailedEvent = "ServiceSetCollectServiceStatusesFailed"

	// ServiceSetIsBeingDeletedEvent indicates the event for services set being deleted.
	ServiceSetIsBeingDeletedEvent = "ServiceSetIsBeingDeleted"
)

type ServiceSetOperation string

const (
	ServiceSetOperationCreate ServiceSetOperation = "create"
	ServiceSetOperationUpdate ServiceSetOperation = "update"
	ServiceSetOperationDelete ServiceSetOperation = "delete"
	ServiceSetOperationNone   ServiceSetOperation = "none"
)

// ServiceSetSpec defines the desired state of ServiceSet
type ServiceSetSpec struct {
	// Cluster is the name of the ClusterDeployment
	Cluster string `json:"cluster"`

	// MultiClusterService is the name of the MultiClusterService
	MultiClusterService string `json:"multiClusterService,omitempty"`

	// EffectiveNamespace is the namespace where the ServiceTemplates are deployed.
	// It reflects the namespace of the corresponding [ClusterDeployment].
	EffectiveNamespace string `json:"effectiveNamespace"`

	// Provider is the definition of the provider to use to deploy services defined in the ServiceSet.
	Provider ProviderSpec `json:"provider"`

	// Services is the list of services to deploy.
	Services []ServiceWithValues `json:"services,omitempty"`
}

// ProviderSpec contains all the spec related to the state management provider.
type ProviderSpec struct {
	// Config is the provider-specific configuration applied to the produced objects.
	Config *apiextv1.JSON `json:"config,omitempty"`

	// Name is the name of the [StateManagementProvider] object.
	Name string `json:"name"`
}

type ServiceWithValues struct {
	// Name is the name of the service. If the ServiceTemplate is backed by Helm chart,
	// then the name is the name of the Helm release.
	Name string `json:"name"`

	// Namespace is the namespace where the service is deployed. If the ServiceTemplate
	// is backed by Helm chart, then the namespace is the namespace where the Helm release is deployed.
	Namespace string `json:"namespace"`

	// Template is the name of the ServiceTemplate to use to deploy the service.
	Template string `json:"template"`

	// Values is the values to pass to the ServiceTemplate.
	Values string `json:"values,omitempty"`

	// ValuesFrom is the list of sources of the values to pass to the ServiceTemplate.
	ValuesFrom []ValuesFrom `json:"valuesFrom,omitempty"`
}

// ValuesFrom is the source of the values to pass to the ServiceTemplate. The source
// can be a ConfigMap or a Secret located in the same namespace as the ServiceSet.
type ValuesFrom struct {
	// +kubebuilder:validation:Enum=ConfigMap;Secret

	// Kind is the kind of the source.
	Kind string `json:"kind"`

	// Name is the name of the source.
	Name string `json:"name"`
}

// ServiceSetStatus defines the observed state of ServiceSet
type ServiceSetStatus struct {
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type

	// Conditions is a list of conditions for the ServiceSet
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Services is a list of Service states in the ServiceSet
	Services []ServiceState `json:"services,omitempty"`

	// +kubebuilder:default=false

	// Deployed is true if the ServiceSet has been deployed
	Deployed bool `json:"deployed"`

	// Provider is the state of the provider
	Provider ProviderState `json:"provider,omitempty"`
}

// ProviderState is the state of the provider
type ProviderState struct {
	// Ready is true if the provider is ready
	Ready bool `json:"ready,omitempty"`

	// Suspended is true if the provider is suspended
	Suspended bool `json:"suspended,omitempty"`
}

// ServiceState is the state of a Service
type ServiceState struct {
	// LastStateTransitionTime is the time the State was last transitioned
	LastStateTransitionTime *metav1.Time `json:"lastStateTransitionTime"`

	// Name is the name of the Service
	Name string `json:"name"`

	// Namespace is the namespace of the Service
	Namespace string `json:"namespace"`

	// Template is the name of the ServiceTemplate used to deploy the Service
	Template string `json:"template"`

	// Version is the version of the Service
	Version string `json:"version"`

	// State is the state of the Service
	// +kubebuilder:validation:Enum=Deployed;Provisioning;Failed;Pending;Deleting
	State string `json:"state"`

	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type

	// Conditions is a list of conditions for the Service
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="age",type=date,JSONPath=`.metadata.creationTimestamp`,description="Time elapsed since object creation",priority=0

// ServiceSet is the Schema for the servicesets API
type ServiceSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServiceSetSpec   `json:"spec,omitempty"`
	Status ServiceSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceSetList contains a list of ServiceSet
type ServiceSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceSet{}, &ServiceSetList{})
}
