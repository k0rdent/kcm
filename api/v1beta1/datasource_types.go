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

const (
	DataSourceTypePostgresql = "postgresql"
)

// DataSourceSpec defines the desired state of DataSource
type DataSourceSpec struct {
	// Auth provides authentication and optional TLS configuration for connecting to the data source.
	Auth DataSourceAuth `json:"auth,omitempty"`

	// +kubebuilder:default:=postgresql

	// Type specifies the data service type (e.g. "postgresql")
	Type string `json:"type"`

	// +kubebuilder:example:=`postgres-db1.example.com:5432`

	// Endpoints contains one or more host/port pairs that clients should use to connect to the data source.
	Endpoints []string `json:"endpoints"`
}

// DataSourceAuth defines credentials and optional TLS settings for a data source connection.
type DataSourceAuth struct {
	// PasswordSecretKeyRef references a Secret key containing the password for the username.
	PasswordSecretKeyRef *SecretKeyRef `json:"passwordSecretKeyRef,omitempty"`

	// CASecretKeyRef optionally references a Secret key containing the CA certificate used to verify
	// TLS connections to the data source.
	CASecretKeyRef *SecretKeyRef `json:"caSecretKeyRef,omitempty"`

	// Name is the username used when authenticating to the data source.
	Name string `json:"name"`
}

type SecretKeyRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=ds

// DataSource is the Schema for the datasources API
type DataSource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec DataSourceSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// DataSourceList contains a list of DataSource
type DataSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DataSource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DataSource{}, &DataSourceList{})
}
