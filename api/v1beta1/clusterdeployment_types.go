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
	"encoding/json"
	"fmt"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	BlockingFinalizer          = "k0rdent.mirantis.com/cleanup"
	ClusterDeploymentFinalizer = "k0rdent.mirantis.com/cluster-deployment"

	FluxHelmChartNameKey      = "helm.toolkit.fluxcd.io/name"
	FluxHelmChartNamespaceKey = "helm.toolkit.fluxcd.io/namespace"

	KCMManagedLabelKey   = "k0rdent.mirantis.com/managed"
	KCMManagedLabelValue = "true"
)

// ClusterDeploymentKind is the string representation of a ClusterDeployment.
const ClusterDeploymentKind = "ClusterDeployment"

const (
	// TemplateReadyCondition indicates the referenced Template exists and valid.
	TemplateReadyCondition = "TemplateReady"
	// HelmChartReadyCondition indicates the corresponding HelmChart is valid and ready.
	HelmChartReadyCondition = "HelmChartReady"
	// HelmReleaseReadyCondition indicates the corresponding HelmRelease is ready and fully reconciled.
	HelmReleaseReadyCondition = "HelmReleaseReady"
	// CAPIClusterSummaryCondition aggregates all the important conditions from the Cluster object.
	CAPIClusterSummaryCondition = "CAPIClusterSummary"
	// SveltosClusterReadyCondition indicates the sveltos cluster is valid and ready.
	SveltosClusterReadyCondition = "SveltosClusterReady"
	// CloudResourcesDeletedCondition indicates whether the cloud resources have been deleted.
	CloudResourcesDeletedCondition = "CloudResourcesDeletedCondition"
	// ClusterAuthenticationReadyCondition indicates whether the referenced [ClusterAuthentication] object exists
	// and ready.
	ClusterAuthenticationReadyCondition = "ClusterAuthenticationReady"
	// DataSourceReadyCondition indicates whether the referenced [DataSource] object exists and ready.
	DataSourceReadyCondition = "DataSourceReady"
	// ClusterDataSourceReadyCondition indicates whether the dedicated [ClusterDataSource] object exists and its data is ready to be used.
	ClusterDataSourceReadyCondition = "ClusterDataSourceReady"
)

// ClusterDeploymentSpec defines the desired state of ClusterDeployment
type ClusterDeploymentSpec struct {
	// Config allows to provide parameters for template customization.
	// If no Config provided, the field will be populated with the default values for
	// the template and DryRun will be enabled.
	Config *apiextv1.JSON `json:"config,omitempty"`

	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253

	// Template is a reference to a Template object located in the same namespace.
	Template string `json:"template"`
	// Name reference to the related [Credential] object located in the same namespace.
	Credential string `json:"credential,omitempty"`
	// Name reference to the related [ClusterAuthentication] object.
	ClusterAuth string `json:"clusterAuth,omitempty"`
	// DataSource is the name reference to the related [DataSource] object located in the same namespace.
	DataSource string `json:"dataSource,omitempty"`
	// IPAMClaim defines IP Address Management (IPAM) requirements for the cluster.
	// It can either reference an existing IPAM claim or specify an inline claim.
	IPAMClaim ClusterIPAMClaimType `json:"ipamClaim,omitempty"`
	// ServiceSpec is spec related to deployment of services.
	ServiceSpec ServiceSpec `json:"serviceSpec,omitempty"`
	// DryRun specifies whether the template should be applied after validation or only validated.
	DryRun bool `json:"dryRun,omitempty"`
	// +kubebuilder:default:=true

	// PropagateCredentials indicates whether credentials should be propagated
	// for use by CCM (Cloud Controller Manager).
	PropagateCredentials bool `json:"propagateCredentials,omitempty"`

	// CleanupOnDeletion specifies whether potentially orphaned Services and PVCs
	// should be removed during the object deletion.
	// This is a best-effort cleanup, if there is no possibility to acquire
	// a managed cluster's kubeconfig, the cleanup will NOT happen.
	CleanupOnDeletion bool `json:"cleanupOnDeletion,omitempty"`

	// +kubebuilder:default:=true
	// InheritRegistryCredentials indicates whether registry credentials from the
	// management cluster should be propagated to this child cluster.
	// If false, registry credentials will not be automatically distributed.
	InheritRegistryCredentials bool `json:"inheritRegistryCredentials,omitempty"`

	// RegistryOverride allows this cluster to use a different registry configuration
	// than the management cluster. When specified, this overrides the global registry
	// settings for this specific cluster.
	// +optional
	RegistryOverride *RegistryConfig `json:"registryOverride,omitempty"`
}

// ClusterIPAMClaimType represents the IPAM claim configuration for a cluster deployment.
// It allows referencing an existing claim or defining a new one inline.
type ClusterIPAMClaimType struct {
	// ClusterIPAMClaimSpec defines the inline IPAM claim specification if no reference is provided.
	// This allows for dynamic IP address allocation during cluster provisioning.
	ClusterIPAMClaimSpec *ClusterIPAMClaimSpec `json:"spec,omitempty"`

	// ClusterIPAMClaimRef is the name of an existing ClusterIPAMClaim resource to use.
	ClusterIPAMClaimRef string `json:"ref,omitempty"`
}

// ClusterDeploymentStatus defines the observed state of ClusterDeployment
type ClusterDeploymentStatus struct {
	// Services contains details for the state of services.
	Services []ServiceState `json:"services,omitempty"`
	// ServicesUpgradePaths contains details for the state of services upgrade paths.
	ServicesUpgradePaths []ServiceUpgradePaths `json:"servicesUpgradePaths,omitempty"`
	// Currently compatible exact Kubernetes version of the cluster. Being set only if
	// provided by the corresponding ClusterTemplate.
	KubernetesVersion string `json:"k8sVersion,omitempty"`
	// Region shows the region the [ClusterDeployment] targets.
	Region string `json:"region,omitempty"`

	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type

	// Conditions contains details for the current state of the ClusterDeployment.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// AvailableUpgrades is the list of ClusterTemplate names to which
	// this cluster can be upgraded. It can be an empty array, which means no upgrades are
	// available.
	AvailableUpgrades []string `json:"availableUpgrades,omitempty"`

	// ObservedGeneration is the last observed generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=clusterd;cld
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].status`,description="Shows readiness of the ClusterDeployment",priority=0
// +kubebuilder:printcolumn:name="Services",type="string",JSONPath=`.status.conditions[?(@.type=="ServicesInReadyState")].message`,description="Number of ready out of total services",priority=0
// +kubebuilder:printcolumn:name="Template",type="string",JSONPath=`.spec.template`,description="ClusterTemplate used for the ClusterDeployment",priority=0
// +kubebuilder:printcolumn:name="Messages",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].message`,description="Shows either readiness or error messages from child objects",priority=0
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="Time elapsed since object creation",priority=0
// +kubebuilder:printcolumn:name="DryRun",type="string",JSONPath=`.spec.dryRun`,description="Dry Run",priority=1

// ClusterDeployment is the Schema for the ClusterDeployments API
type ClusterDeployment struct { //nolint:govet // false-positive
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterDeploymentSpec   `json:"spec"`
	Status ClusterDeploymentStatus `json:"status,omitempty"`
}

func (in *ClusterDeployment) HelmValues() (map[string]any, error) {
	var values map[string]any

	if in.Spec.Config != nil {
		if err := yaml.Unmarshal(in.Spec.Config.Raw, &values); err != nil {
			return nil, fmt.Errorf("error unmarshalling helm values for clusterTemplate %s: %w", in.Spec.Template, err)
		}
	}

	return values, nil
}

func (in *ClusterDeployment) SetHelmValues(values map[string]any) error {
	b, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("error marshalling helm values for clusterTemplate %s: %w", in.Spec.Template, err)
	}

	in.Spec.Config = &apiextv1.JSON{Raw: b}
	return nil
}

func (in *ClusterDeployment) AddHelmValues(fn func(map[string]any) error) error {
	values, err := in.HelmValues()
	if err != nil {
		return err
	}

	if err := fn(values); err != nil {
		return err
	}

	return in.SetHelmValues(values)
}

func (in *ClusterDeployment) GetConditions() *[]metav1.Condition {
	return &in.Status.Conditions
}

// +kubebuilder:object:root=true

// ClusterDeploymentList contains a list of ClusterDeployment
type ClusterDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterDeployment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterDeployment{}, &ClusterDeploymentList{})
}
