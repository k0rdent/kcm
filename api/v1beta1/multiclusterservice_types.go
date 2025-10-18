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
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// MultiClusterServiceFinalizer is finalizer applied to MultiClusterService objects.
	MultiClusterServiceFinalizer = "k0rdent.mirantis.com/multicluster-service"
	// MultiClusterServiceKind is the string representation of a MultiClusterServiceKind.
	MultiClusterServiceKind = "MultiClusterService"
)

const (
	// SveltosProfileReadyCondition indicates if the Sveltos Profile is ready.
	SveltosProfileReadyCondition = "SveltosProfileReady"
	// SveltosClusterProfileReadyCondition indicates if the Sveltos ClusterProfile is ready.
	SveltosClusterProfileReadyCondition = "SveltosClusterProfileReady"
	// SveltosHelmReleaseReadyCondition indicates if the HelmRelease
	// managed by a Sveltos Profile/ClusterProfile is ready.
	SveltosHelmReleaseReadyCondition = "SveltosHelmReleaseReady"

	// FetchServicesStatusSuccessCondition indicates if status
	// for the deployed services have been fetched successfully.
	FetchServicesStatusSuccessCondition = "FetchServicesStatusSuccess"

	// ServicesInReadyStateCondition shows the number of multiclusterservices or clusterdeployments
	// services that are ready. A service is marked as ready if all its conditions are ready.
	// The format is "<ready-num>/<total-num>", e.g. "2/3" where 2 services of total 3 are ready.
	ServicesInReadyStateCondition = "ServicesInReadyState"

	// ClusterInReadyStateCondition shows the number of clusters that are ready.
	// A Cluster is ready if corresponding ClusterDeployment is ready.
	// The format is "<ready-num>/<total-num>", e.g. "2/3" where 2 clusters of total 3 are ready.
	ClusterInReadyStateCondition = "ClusterInReadyState"

	// ServicesReferencesValidationCondition defines the condition of services' references validation.
	ServicesReferencesValidationCondition = "ServicesReferencesValidation"

	// ServicesDependencyValidationCondition defines the condition of services' dependencies.
	ServicesDependencyValidationCondition = "ServicesDependencyValidation"

	// MultiClusterDependencyValidationCondition defines the condition of MultiClusterService dependencies.
	MultiClusterServiceDependencyValidationCondition = "MultiClusterServiceDependencyValidation"
)

// Reasons are provided as utility, and not part of the declarative API.
const (
	// ServicesReferencesValidationFailedReason signals that the validation for references related to services (KSM) failed.
	ServicesReferencesValidationFailedReason = "ServicesReferencesValidationFailed"
	// ServicesReferencesValidationSucceededReason signals that the validation for references related to services (KSM) succeeded.
	ServicesReferencesValidationSucceededReason = "ServicesReferencesValidationSucceeded"
	// SveltosProfileNotReadyReason signals that the Sveltos Profile object is not yet ready.
	SveltosProfileNotReadyReason = "SveltosProfileNotReady"
	// SveltosClusterProfileNotReadyReason signals that the Sveltos ClusterProfile object is not yet ready.
	SveltosClusterProfileNotReadyReason = "SveltosClusterProfileNotReady"
	// FetchServicesStatusFailedReason signals that fetching the status of services from Sveltos objects failed.
	FetchServicesStatusFailedReason = "FetchServicesStatusFailed"
	// SveltosHelmReleaseNotReadyReason signals that the helm release managed by Sveltos on target cluster is not yet ready.
	SveltosHelmReleaseNotReadyReason = "SveltosHelmReleaseNotReady"
	// SveltosFeatureReadyReason signals that the feature managed by Sveltos on target cluster is ready.
	SveltosFeatureReadyReason = "SveltosFeatureReady"
	// SveltosFeatureNotReadyReason signals that the feature managed by Sveltos on target cluster is not yet ready.
	SveltosFeatureNotReadyReason = "SveltosFeatureNotReady"
)

// Service represents a Service to be deployed.
type Service struct {
	// Values is the helm values to be passed to the chart used by the template.
	// The string type is used in order to allow for templating.
	Values string `json:"values,omitempty"`

	// HelmOptions are the options to be passed to the provider for helm installation or updates
	HelmOptions *ServiceHelmOptions `json:"helmOptions,omitempty"`

	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253

	// Template is a reference to a Template object located in the same namespace.
	Template string `json:"template"`

	// TemplateChain defines the ServiceTemplateChain object that will be used to deploy the service
	// along with desired ServiceTemplate version.
	TemplateChain string `json:"templateChain,omitempty"`

	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253

	// Name is the chart release.
	Name string `json:"name"`

	// +kubebuilder:default:=default

	// Namespace is the namespace the release will be installed in.
	// It will default to "default" if not provided.
	Namespace string `json:"namespace,omitempty"`
	// ValuesFrom can reference a ConfigMap or Secret containing helm values.
	ValuesFrom []ValuesFrom `json:"valuesFrom,omitempty"`
	// DependsOn specifies a list of other services that this service depends on.
	DependsOn []ServiceDependsOn `json:"dependsOn,omitempty"`
	// Disable can be set to disable handling of this service.
	Disable bool `json:"disable,omitempty"`
}

// ServiceDependsOn identifies a service by its release name and namespace.
type ServiceDependsOn struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253

	// Name is the release name on target cluster.
	Name string `json:"name"`
	// Namespace is the release namespace on target cluster.
	Namespace string `json:"namespace,omitempty"`
}

type ServiceHelmOptions struct {
	// +optional

	// EnableClientCache is a flag to enable Helm client cache. If it is not specified, it will be set to false.
	EnableClientCache *bool `json:"enableClientCache,omitempty"`

	// +optional

	// update dependencies if they are missing before installing the chart
	DependencyUpdate *bool `json:"dependencyUpdate,omitempty"`
	// +optional

	// if set, will wait until all Pods, PVCs, Services, and minimum number of Pods of a Deployment, StatefulSet, or ReplicaSet
	// are in a ready state before marking the release as successful. It will wait for as long as --timeout
	Wait *bool `json:"wait,omitempty"`

	// +optional

	// if set and --wait enabled, will wait until all Jobs have been completed before marking the release as successful.
	// It will wait for as long as --timeout
	WaitForJobs *bool `json:"waitForJobs,omitempty"`

	// +optional

	CreateNamespace *bool `json:"createNamespace,omitempty"`

	// +optional

	// SkipCRDs controls whether CRDs should be installed during install/upgrade operation.
	// By default, CRDs are installed if not already present.
	SkipCRDs *bool `json:"skipCRDs,omitempty"`

	// +optional

	// if set, the installation process deletes the installation/upgrades on failure.
	// The --wait flag will be set automatically if --atomic is used
	Atomic *bool `json:"atomic,omitempty"`

	// +optional

	// prevent hooks from running during install/upgrade/uninstall
	DisableHooks *bool `json:"disableHooks,omitempty"`

	// +optional

	// if set, the installation process will not validate rendered templates against the Kubernetes OpenAPI Schema
	DisableOpenAPIValidation *bool `json:"disableOpenAPIValidation,omitempty"`

	// +optional

	// time to wait for any individual Kubernetes operation (like Jobs for hooks) (default 5m0s)
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// +optional

	// SkipSchemaValidation determines if JSON schema validation is disabled.
	SkipSchemaValidation *bool `json:"skipSchemaValidation,omitempty"`

	// +optional

	// Replaces if set indicates to replace an older release with this one
	Replace *bool `json:"replace,omitempty"`

	// +optional

	// Labels that would be added to release metadata.
	Labels *map[string]string `json:"labels,omitempty"`

	// +optional

	// Description is the description of an helm operation
	Description *string `json:"description,omitempty"`
}

// ServiceSpec contains all the spec related to deployment of services.
type ServiceSpec struct {
	// +kubebuilder:default:=Continuous
	// +kubebuilder:validation:Enum:=OneTime;Continuous;ContinuousWithDriftDetection;DryRun

	// SyncMode specifies how services are synced in the target cluster.
	// Deprecated: use .provider.config field to define provider-specific configuration.
	SyncMode string `json:"syncMode,omitempty"`
	// Provider is the definition of the provider to use to deploy services.
	Provider StateManagementProviderConfig `json:"provider,omitempty"`

	// +listType=map
	// +listMapKey=name
	// +listMapKey=namespace

	// Services is a list of services created via ServiceTemplates
	// that could be installed on the target cluster.
	Services []Service `json:"services,omitempty"`

	// TemplateResourceRefs is a list of resources to collect from the management cluster,
	// the values from which can be used in templates.
	// Deprecated: use .provider.config field to define provider-specific configuration.
	TemplateResourceRefs []addoncontrollerv1beta1.TemplateResourceRef `json:"templateResourceRefs,omitempty"`

	// +listType=atomic

	// PolicyRefs references all the ConfigMaps/Secrets/Flux Sources containing kubernetes resources
	// that need to be deployed in the target clusters.
	// The values contained in those resources can be static or leverage Go templates for dynamic customization.
	// When expressed as templates, the values are filled in using information from
	// resources within the management cluster before deployment (Cluster and TemplateResourceRefs)
	// Deprecated: use .provider.config field to define provider-specific configuration.
	PolicyRefs []addoncontrollerv1beta1.PolicyRef `json:"policyRefs,omitempty"`

	// DriftIgnore specifies resources to ignore for drift detection.
	// Deprecated: use .provider.config field to define provider-specific configuration.
	DriftIgnore []libsveltosv1beta1.PatchSelector `json:"driftIgnore,omitempty"`

	// DriftExclusions specifies specific configurations of resources to ignore for drift detection.
	// Deprecated: use .provider.config field to define provider-specific configuration.
	DriftExclusions []libsveltosv1beta1.DriftExclusion `json:"driftExclusions,omitempty"`

	// +kubebuilder:default:=100
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2147483646

	// Priority sets the priority for the services defined in this spec.
	// Higher value means higher priority and lower means lower.
	// In case of conflict with another object managing the service,
	// the one with higher priority will get to deploy its services.
	// Deprecated: use .provider.config field to define provider-specific configuration.
	Priority int32 `json:"priority,omitempty"`

	// +kubebuilder:default:=false

	// StopOnConflict specifies what to do in case of a conflict.
	// E.g. If another object is already managing a service.
	// By default the remaining services will be deployed even if conflict is detected.
	// If set to true, the deployment will stop after encountering the first conflict.
	// Deprecated: use .provider.config field to define provider-specific configuration.
	StopOnConflict bool `json:"stopOnConflict,omitempty"`

	// Reload instances via rolling upgrade when a ConfigMap/Secret mounted as volume is modified.
	// Deprecated: use .provider.config field to define provider-specific configuration.
	Reload bool `json:"reload,omitempty"`

	// +kubebuilder:default:=false

	// ContinueOnError specifies if the services deployment should continue if an error occurs.
	// Deprecated: use .provider.config field to define provider-specific configuration.
	ContinueOnError bool `json:"continueOnError,omitempty"`
}

// MultiClusterServiceSpec defines the desired state of MultiClusterService
type MultiClusterServiceSpec struct {
	// ClusterSelector identifies target clusters to manage services on.
	ClusterSelector metav1.LabelSelector `json:"clusterSelector,omitempty"`
	// DependsOn is a list of other MultiClusterServices this one depends on.
	DependsOn []string `json:"dependsOn,omitempty"`
	// ServiceSpec is spec related to deployment of services.
	ServiceSpec ServiceSpec `json:"serviceSpec,omitempty"`
}

// ServiceStatus contains details for the state of services.
type ServiceStatus struct {
	// ClusterName is the name of the associated cluster.
	ClusterName string `json:"clusterName"`
	// ClusterNamespace is the namespace of the associated cluster.
	ClusterNamespace string `json:"clusterNamespace,omitempty"`
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type

	// Conditions contains details for the current state of managed services.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// MultiClusterServiceStatus defines the observed state of MultiClusterService.
type MultiClusterServiceStatus struct {
	// Services contains details for the state of services.
	Services []ServiceState `json:"services,omitempty"`
	// ServicesUpgradePaths contains details for the state of services upgrade paths.
	ServicesUpgradePaths []ServiceUpgradePaths `json:"servicesUpgradePaths,omitempty"`
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type

	// Conditions contains details for the current state of the MultiClusterService.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ObservedGeneration is the last observed generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// ServiceUpgradePaths contains details for the state of service upgrade paths.
type ServiceUpgradePaths struct {
	// Name is the name of the service.
	Name string `json:"name"`
	// Namespace is the namespace of the service.
	Namespace string `json:"namespace"`
	// Template is the name of the current service template.
	Template string `json:"template"`
	// AvailableUpgrades contains details for the state of available upgrades.
	AvailableUpgrades []UpgradePath `json:"availableUpgrades,omitempty"`
}

// UpgradePath contains details for the state of service upgrade paths.
type UpgradePath struct {
	// Versions contains the list of versions that service can be upgraded to.
	Versions []string `json:"upgradePaths,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=mcs
// +kubebuilder:printcolumn:name="Services",type="string",JSONPath=`.status.conditions[?(@.type=="ServicesInReadyState")].message`,description="Number of ready out of total services",priority=0
// +kubebuilder:printcolumn:name="Clusters",type="string",JSONPath=`.status.conditions[?(@.type=="ClusterInReadyState")].message`,description="Number of ready out of total selected clusters",priority=0
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="Time elapsed since object creation",priority=0

// MultiClusterService is the Schema for the multiclusterservices API
type MultiClusterService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MultiClusterServiceSpec   `json:"spec,omitempty"`
	Status MultiClusterServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MultiClusterServiceList contains a list of MultiClusterService
type MultiClusterServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MultiClusterService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MultiClusterService{}, &MultiClusterServiceList{})
}
