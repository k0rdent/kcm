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

// DatabaseType represents the type of backend used for connecting to external data source.
type DatabaseType string

const (
	// KineTypePostresql represents the PostgreSQL backend for Kine database connections.
	KineTypePostresql = "postgresql"
)

// DataSourceSpec defines the desired state of DataSource
type DataSourceSpec struct {
	// CertificateAuthority optionally specifies the reference to a Secret containing
	// the certificate authority (CA) certificate used to verify the data source's
	// server certificate during TLS handshake.
	CertificateAuthority *SecretKeyReference `json:"certificateAuthority,omitempty"`

	// Auth provides basic authentication with username and password.
	Auth DataSourceAuth `json:"auth"`

	// +kubebuilder:validation:Enum=postgresql
	// +kubebuilder:example:=`postgresql`

	// Type specifies the database type to connect to the data source.
	Type DatabaseType `json:"type"`

	// +kubebuilder:example:=`[postgres-db1.example.com:5432]`

	// Endpoints contains one or more host/port pairs that clients should use to connect to the data source.
	// No need in schema, only IP/FQDN and port.
	Endpoints []string `json:"endpoints"`
}

// DataSourceAuth represents authentication credentials for connecting to a data source.
// It contains references to secrets that store the username and password required
// for authenticating with external data sources.
type DataSourceAuth struct {
	// Username is a reference to a secret key containing the username credential
	// used for data source authentication.
	Username SecretKeyReference `json:"username"`

	// Password is a reference to a secret key containing the password credential
	// used for data source authentication.
	Password SecretKeyReference `json:"password"`
}

// +kubebuilder:object:root=true

// DataSource is the Schema for the datasources API
type DataSource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec DataSourceSpec `json:"spec"`
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
