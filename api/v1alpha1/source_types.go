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
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1beta2 "github.com/fluxcd/source-controller/api/v1beta2"
)

// EmbeddedGitRepositorySpec is the partial spec for a GitRepository reused from
// the GitRepositorySpec provided by fluxcd/source-controller API. It reflects all
// fields defined in the GitRepositorySpec except those containing the references
// to a Secret with authorization credentials.
type EmbeddedGitRepositorySpec struct {
	// URL specifies the Git repository URL, it can be an HTTP/S or SSH address.
	// +kubebuilder:validation:Pattern="^(http|https|ssh)://.*$"
	// +required
	URL string `json:"url"`

	// Reference specifies the Git reference to resolve and monitor for
	// changes, defaults to the 'master' branch.
	// +optional
	Reference *sourcev1.GitRepositoryRef `json:"ref,omitempty"`

	// Verification specifies the configuration to verify the Git commit
	// signature(s).
	// +optional
	Verification *sourcev1.GitRepositoryVerification `json:"verify,omitempty"`

	// RecurseSubmodules enables the initialization of all submodules within
	// the GitRepository as cloned from the URL, using their default settings.
	// +optional
	RecurseSubmodules bool `json:"recurseSubmodules,omitempty"`

	// Include specifies a list of GitRepository resources which Artifacts
	// should be included in the Artifact produced for this GitRepository.
	// +optional
	Include []sourcev1.GitRepositoryInclude `json:"include,omitempty"`
}

// EmbeddedBucketSpec is the partial spec for a Bucket reused from the BucketSpec
// provided by fluxcd/source-controller API. It reflects all fields defined in the
// BucketSpec except those containing the references to a Secret with authorization
// credentials.
type EmbeddedBucketSpec struct {
	// BucketName is the name of the object storage bucket.
	// +required
	BucketName string `json:"bucketName"`

	// Endpoint is the object storage address the BucketName is located at.
	// +required
	Endpoint string `json:"endpoint"`

	// STS specifies the required configuration to use a Security Token
	// Service for fetching temporary credentials to authenticate in a
	// Bucket provider.
	//
	// This field is only supported for the `aws` and `generic` providers.
	// +optional
	STS *sourcev1.BucketSTSSpec `json:"sts,omitempty"`

	// Insecure allows connecting to a non-TLS HTTP Endpoint.
	// +optional
	Insecure bool `json:"insecure,omitempty"`

	// Region of the Endpoint where the BucketName is located in.
	// +optional
	Region string `json:"region,omitempty"`

	// Prefix to use for server-side filtering of files in the Bucket.
	// +optional
	Prefix string `json:"prefix,omitempty"`
}

// EmbeddedOCIRepositorySpec is the partial spec for an OCIRepository reused from
// the OCIRepositorySpec provided by fluxcd/source-controller API. It reflects all
// fields defined in the OCIRepositorySpec except those containing the references
// to a Secret with authorization credentials.
type EmbeddedOCIRepositorySpec struct {
	// URL is a reference to an OCI artifact repository hosted
	// on a remote container registry.
	// +kubebuilder:validation:Pattern="^oci://.*$"
	// +required
	URL string `json:"url"`

	// The OCI reference to pull and monitor for changes,
	// defaults to the latest tag.
	// +optional
	Reference *sourcev1beta2.OCIRepositoryRef `json:"ref,omitempty"`

	// LayerSelector specifies which layer should be extracted from the OCI artifact.
	// When not specified, the first layer found in the artifact is selected.
	// +optional
	LayerSelector *sourcev1beta2.OCILayerSelector `json:"layerSelector,omitempty"`

	// Verify contains the secret name containing the trusted public keys
	// used to verify the signature and specifies which provider to use to check
	// whether OCI image is authentic.
	// +optional
	Verify *sourcev1.OCIRepositoryVerification `json:"verify,omitempty"`

	// ServiceAccountName is the name of the Kubernetes ServiceAccount used to authenticate
	// the image pull if the service account has attached pull secrets. For more information:
	// https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/#add-imagepullsecrets-to-a-service-account
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Insecure allows connecting to a non-TLS HTTP container registry.
	// +optional
	Insecure bool `json:"insecure,omitempty"`
}
