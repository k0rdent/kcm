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

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
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

	ClusterNameLabelKey = "cluster.x-k8s.io/cluster-name"
)

const (
	// ClusterDeploymentKind is the string representation of a ClusterDeployment.
	ClusterDeploymentKind = "ClusterDeployment"
	// TemplateReadyCondition indicates the referenced Template exists and valid.
	TemplateReadyCondition = "TemplateReady"
	// HelmChartReadyCondition indicates the corresponding HelmChart is valid and ready.
	HelmChartReadyCondition = "HelmChartReady"
	// HelmReleaseReadyCondition indicates the corresponding HelmRelease is ready and fully reconciled.
	HelmReleaseReadyCondition = "HelmReleaseReady"
	// ReadyCondition indicates the ClusterDeployment is ready and fully reconciled.
	ReadyCondition string = "Ready"
)

// ClusterDeploymentSpec defines the desired state of ClusterDeployment
type ClusterDeploymentSpec struct {
	// Config allows to provide parameters for template customization.
	// If no Config provided, the field will be populated with the default values for
	// the template and DryRun will be enabled.
	Config *apiextensionsv1.JSON `json:"config,omitempty"`

	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253

	// Template is a reference to a Template object located in the same namespace.
	Template string `json:"template"`
	// Name reference to the related Credentials object.
	Credential string `json:"credential,omitempty"`
	// PropagateCredentials indicates whether credentials should be propagated
	// for use by CCM (Cloud Controller Manager).
	// +kubebuilder:default:=true
	PropagateCredentials bool `json:"propagateCredentials,omitempty"`
	// ServiceSpec is spec related to deployment of services.
	ServiceSpec ServiceSpec `json:"serviceSpec,omitempty"`
	// DryRun specifies whether the template should be applied after validation or only validated.
	DryRun bool `json:"dryRun,omitempty"`
}

// ClusterDeploymentStatus defines the observed state of ClusterDeployment
type ClusterDeploymentStatus struct {
	// Services contains details for the state of services.
	Services []ServiceStatus `json:"services,omitempty"`
	// Currently compatible exact Kubernetes version of the cluster. Being set only if
	// provided by the corresponding ClusterTemplate.
	KubernetesVersion string `json:"k8sVersion,omitempty"`
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
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=clusterd;cld
// +kubebuilder:printcolumn:name="ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description="Ready",priority=0
// +kubebuilder:printcolumn:name="status",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].message",description="Status",priority=0
// +kubebuilder:printcolumn:name="dryRun",type="string",JSONPath=".spec.dryRun",description="Dry Run",priority=1

// ClusterDeployment is the Schema for the ClusterDeployments API
type ClusterDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterDeploymentSpec   `json:"spec,omitempty"`
	Status ClusterDeploymentStatus `json:"status,omitempty"`
}

func (in *ClusterDeployment) HelmValues() (values map[string]any, err error) {
	if in.Spec.Config != nil {
		err = yaml.Unmarshal(in.Spec.Config.Raw, &values)
	}
	return values, err
}

func (in *ClusterDeployment) GetConditions() *[]metav1.Condition {
	return &in.Status.Conditions
}

func (in *ClusterDeployment) InitConditions() {
	apimeta.SetStatusCondition(in.GetConditions(), metav1.Condition{
		Type:    TemplateReadyCondition,
		Status:  metav1.ConditionUnknown,
		Reason:  ProgressingReason,
		Message: "Template is not yet ready",
	})
	apimeta.SetStatusCondition(in.GetConditions(), metav1.Condition{
		Type:    HelmChartReadyCondition,
		Status:  metav1.ConditionUnknown,
		Reason:  ProgressingReason,
		Message: "HelmChart is not yet ready",
	})
	if !in.Spec.DryRun {
		apimeta.SetStatusCondition(in.GetConditions(), metav1.Condition{
			Type:    HelmReleaseReadyCondition,
			Status:  metav1.ConditionUnknown,
			Reason:  ProgressingReason,
			Message: "HelmRelease is not yet ready",
		})
	}
	apimeta.SetStatusCondition(in.GetConditions(), metav1.Condition{
		Type:    ReadyCondition,
		Status:  metav1.ConditionUnknown,
		Reason:  ProgressingReason,
		Message: "ClusterDeployment is not yet ready",
	})
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
