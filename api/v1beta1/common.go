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

import corev1 "k8s.io/api/core/v1"

const (
	// SucceededReason indicates a condition or event observed a success, for example when declared desired state
	// matches actual state, or a performed action succeeded.
	SucceededReason string = "Succeeded"

	// FailedReason indicates a condition or event observed a failure, for example when declared state does not match
	// actual state, or a performed action failed.
	FailedReason string = "Failed"

	// ProgressingReason indicates a condition or event observed progression, for example when the reconciliation of a
	// resource or an action has started.
	ProgressingReason string = "Progressing"

	// NotPausedReason appears when an object is not paused.
	NotPausedReason = "NotPaused"

	// PausedReason appears when an object is paused.
	PausedReason = "Paused"
)

const (
	// ReadyCondition indicates a resource is ready and fully reconciled.
	ReadyCondition string = "Ready"

	// DeletingCondition indicates a resource is deleting.
	DeletingCondition string = "Deleting"

	// PredeclaredSecretsExistCondition indicates whether all object's required (and predeclared) Secrets exist.
	PredeclaredSecretsExistCondition = "PredeclaredSecretsExist"

	// PausedCondition indicates whether an object is paused and not being reconciled.
	PausedCondition = "Paused"
)

type (
	// Holds different types of CAPI providers.
	Providers []string

	// Holds key-value pairs with compatibility [contract versions],
	// where the key is the core CAPI contract version,
	// and the value is an underscore-delimited (_) list of provider contract versions
	// supported by the core CAPI.
	//
	// [contract versions]: https://cluster-api.sigs.k8s.io/developer/providers/contracts
	CompatibilityContracts map[string]string
)

const (
	// Provider K0smotron
	ProviderK0smotronName = "k0smotron"
	// Provider Sveltos
	ProviderSveltosName = "projectsveltos"
)

// SecretKeyReference is the combination of the Secret reference and the key of the stored value.
type SecretKeyReference struct {
	corev1.SecretReference `json:",inline"`
	// +kubebuilder:validation:MinLength=1

	// Key is the name of the key for the given Secret reference where the value is stored.
	Key string `json:"key"`
}

// RegistryCredentials defines credentials for authenticating to a container registry.
// Supports either inline credentials or a reference to an existing Secret.
type RegistryCredentials struct {
	// Username for registry authentication. Used when providing inline credentials.
	// +optional
	Username string `json:"username,omitempty"`

	// Password for registry authentication. Used when providing inline credentials.
	// For token-based authentication, provide the token as the password.
	// +optional
	Password string `json:"password,omitempty"`

	// SecretRef references an existing Secret containing registry credentials.
	// The Secret should be of type Opaque with 'username' and 'password' keys,
	// or 'token' key for token-based authentication.
	// If specified, inline Username and Password fields are ignored.
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`
}

// RegistryConfig defines the configuration for a container registry,
// including the registry URL, credentials, and security settings.
type RegistryConfig struct {
	// Registry is the URL of the container registry (e.g., "registry.example.com").
	// +optional
	Registry string `json:"registry,omitempty"`

	// Credentials for authenticating to the registry.
	// Can be provided inline or via a Secret reference.
	// +optional
	Credentials *RegistryCredentials `json:"credentials,omitempty"`

	// InsecureRegistry allows connecting to the registry over HTTP instead of HTTPS.
	// +optional
	InsecureRegistry bool `json:"insecureRegistry,omitempty"`
}
