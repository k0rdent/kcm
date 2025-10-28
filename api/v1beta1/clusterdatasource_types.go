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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterDataSourceSpec defines the desired state of ClusterDataSource
type ClusterDataSourceSpec struct {
	// Schema is the name of the generated schema for the Cluster.
	Schema string `json:"schema"`
	// DataSource references the DataSource object (in the same namespace) that provides database connection
	// information and credentials.
	DataSource string `json:"dataSource"`
}

// ClusterDataSourceStatus defines the observed state of ClusterDataSource
type ClusterDataSourceStatus struct {
	// KineDataSourceSecret is the name of the Secret containing credentials for the Kine datastore connection.
	// Created and managed by the controller.
	KineDataSourceSecret string `json:"kineDataSourceSecret,omitempty"`
	// CASecret is the name of the Secret containing the CA certificate used to establish a TLS-secured
	// connection to the datastore, if applicable.
	CASecret string `json:"caSecret,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cds

// ClusterDataSource is the Schema for the clusterdatasources API
type ClusterDataSource struct { //nolint:govet // false-positive
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterDataSourceSpec   `json:"spec,omitempty"`
	Status ClusterDataSourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterDataSourceList contains a list of ClusterDataSource
type ClusterDataSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterDataSource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterDataSource{}, &ClusterDataSourceList{})
}
