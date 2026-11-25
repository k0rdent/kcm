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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// The Argo CD Application API, cut down to what this adapter sets and reads.
//
// Argo CD publishes these types from github.com/argoproj/argo-cd, a module
// that carries the whole product - server, CLI, controllers and their
// dependencies - for a handful of structs. KCM only ever writes the fields
// below and reads four status ones, so it declares them itself rather than
// take that on. The JSON tags are what has to match, and they do.
//
// Everything Argo CD owns but KCM does not model lives under .status, which
// this adapter never writes, so a typed update cannot drop it.

const (
	// ApplicationKind is the kind of the Argo CD Application resource.
	ApplicationKind = "Application"

	// ResourcesFinalizer makes Argo CD delete the resources of an Application
	// when the Application itself is deleted, instead of orphaning them.
	ResourcesFinalizer = "resources-finalizer.argocd.argoproj.io"
)

// GroupVersion is the Argo CD application group/version.
var GroupVersion = schema.GroupVersion{Group: "argoproj.io", Version: "v1alpha1"}

// Health statuses reported by Argo CD for an Application.
const (
	HealthStatusHealthy     = "Healthy"
	HealthStatusProgressing = "Progressing"
	HealthStatusDegraded    = "Degraded"
	HealthStatusSuspended   = "Suspended"
	HealthStatusMissing     = "Missing"
)

// Sync statuses reported by Argo CD for an Application.
const (
	SyncStatusSynced    = "Synced"
	SyncStatusOutOfSync = "OutOfSync"
)

// Phases of the last sync operation.
const (
	OperationRunning     = "Running"
	OperationSucceeded   = "Succeeded"
	OperationFailed      = "Failed"
	OperationError       = "Error"
	OperationTerminating = "Terminating"
)

// Application is an Argo CD Application.
type Application struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApplicationSpec   `json:"spec"`
	Status ApplicationStatus `json:"status,omitempty"`
}

// ApplicationList is a list of Applications.
type ApplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Application `json:"items"`
}

// ApplicationSpec is the desired state of an Application.
type ApplicationSpec struct {
	// Source is where the manifests come from.
	Source *ApplicationSource `json:"source,omitempty"`
	// SyncPolicy controls whether and how Argo CD syncs on its own.
	SyncPolicy *SyncPolicy `json:"syncPolicy,omitempty"`
	// Project is the Argo CD project the Application belongs to.
	Project string `json:"project,omitempty"`
	// Destination is the cluster and namespace to deploy to.
	Destination ApplicationDestination `json:"destination"`
}

// ApplicationSource locates the manifests of an Application.
type ApplicationSource struct {
	// Helm holds the options of a Helm source.
	Helm *ApplicationSourceHelm `json:"helm,omitempty"`
	// RepoURL is a Helm repository, an OCI registry path or a Git repository.
	RepoURL string `json:"repoURL"`
	// Chart is the Helm chart name; empty for a Git source.
	Chart string `json:"chart,omitempty"`
	// Path is the directory within a Git repository; empty for a Helm one.
	Path string `json:"path,omitempty"`
	// TargetRevision is a chart version, a branch, a tag or a commit.
	TargetRevision string `json:"targetRevision,omitempty"`
}

// ApplicationSourceHelm are the Helm options of a source.
type ApplicationSourceHelm struct {
	// ReleaseName is the name of the Helm release.
	ReleaseName string `json:"releaseName,omitempty"`
	// Values is the values document, as YAML.
	Values string `json:"values,omitempty"`
	// Namespace is the namespace the chart is rendered against.
	Namespace string `json:"namespace,omitempty"`
	// SkipCrds skips the chart's crds directory.
	SkipCrds bool `json:"skipCrds,omitempty"`
	// SkipSchemaValidation skips validating values against values.schema.json.
	SkipSchemaValidation bool `json:"skipSchemaValidation,omitempty"`
}

// ApplicationDestination is where an Application is deployed.
type ApplicationDestination struct {
	// Server is the API server URL of the target cluster.
	Server string `json:"server,omitempty"`
	// Name is the registered name of the target cluster; alternative to Server.
	Name string `json:"name,omitempty"`
	// Namespace is the default namespace of the manifests.
	Namespace string `json:"namespace,omitempty"`
}

// SyncPolicy controls Argo CD's syncing of an Application.
type SyncPolicy struct {
	// Automated syncs without anyone asking; nil means manual syncs only.
	Automated *SyncPolicyAutomated `json:"automated,omitempty"`
	// Retry is the retry behaviour of a failed sync.
	Retry *RetryStrategy `json:"retry,omitempty"`
	// SyncOptions are Argo CD's per-Application sync options.
	SyncOptions []string `json:"syncOptions,omitempty"`
}

// SyncPolicyAutomated configures automated syncing.
type SyncPolicyAutomated struct {
	// Prune deletes resources that are no longer in the source.
	Prune bool `json:"prune,omitempty"`
	// SelfHeal reverts changes made outside of Argo CD.
	SelfHeal bool `json:"selfHeal,omitempty"`
}

// RetryStrategy is how a failed sync is retried.
type RetryStrategy struct {
	// Backoff is the delay between attempts.
	Backoff *Backoff `json:"backoff,omitempty"`
	// Limit is the number of attempts; a negative value means unlimited.
	Limit int64 `json:"limit,omitempty"`
}

// Backoff is the delay between sync retries.
type Backoff struct {
	// Factor multiplies the duration after each attempt.
	Factor *int64 `json:"factor,omitempty"`
	// Duration is the delay before the first retry, e.g. "5s".
	Duration string `json:"duration,omitempty"`
	// MaxDuration caps the delay.
	MaxDuration string `json:"maxDuration,omitempty"`
}

// ApplicationStatus is the observed state of an Application.
type ApplicationStatus struct {
	// OperationState describes the last sync operation.
	OperationState *OperationState `json:"operationState,omitempty"`
	// Health is the aggregate health of the deployed resources.
	Health HealthStatus `json:"health,omitempty"`
	// Sync says whether the cluster matches the source.
	Sync SyncStatus `json:"sync,omitempty"`
	// Conditions are the Application's conditions; Argo CD does not use the
	// metav1 shape here, so neither does this.
	Conditions []ApplicationCondition `json:"conditions,omitempty"`
}

// HealthStatus is the health of an Application.
type HealthStatus struct {
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
}

// SyncStatus is the sync state of an Application.
type SyncStatus struct {
	Status string `json:"status,omitempty"`
	// Revision is the chart version, or the Git revision, last synced.
	Revision string `json:"revision,omitempty"`
}

// OperationState describes the last sync operation of an Application.
type OperationState struct {
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message,omitempty"`
}

// ApplicationCondition is one condition of an Application.
type ApplicationCondition struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

// AddToScheme registers the Argo CD types with the given scheme.
func AddToScheme(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &Application{}, &ApplicationList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}

// DeepCopyObject implements [k8s.io/apimachinery/pkg/runtime.Object].
func (in *Application) DeepCopyObject() runtime.Object { return in.DeepCopy() }

// DeepCopyObject implements [k8s.io/apimachinery/pkg/runtime.Object].
func (in *ApplicationList) DeepCopyObject() runtime.Object { return in.DeepCopy() }

// DeepCopy returns a deep copy of the Application.
func (in *Application) DeepCopy() *Application {
	if in == nil {
		return nil
	}
	out := new(Application)
	out.TypeMeta = in.TypeMeta
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	out.Spec = *in.Spec.DeepCopy()
	out.Status = *in.Status.DeepCopy()
	return out
}

// DeepCopy returns a deep copy of the ApplicationList.
func (in *ApplicationList) DeepCopy() *ApplicationList {
	if in == nil {
		return nil
	}
	out := new(ApplicationList)
	out.TypeMeta = in.TypeMeta
	out.ListMeta = *in.ListMeta.DeepCopy()
	if in.Items != nil {
		out.Items = make([]Application, len(in.Items))
		for i := range in.Items {
			out.Items[i] = *in.Items[i].DeepCopy()
		}
	}
	return out
}

// DeepCopy returns a deep copy of the ApplicationSpec.
func (in *ApplicationSpec) DeepCopy() *ApplicationSpec {
	if in == nil {
		return nil
	}
	out := new(ApplicationSpec)
	*out = *in
	if in.Source != nil {
		out.Source = in.Source.DeepCopy()
	}
	if in.SyncPolicy != nil {
		out.SyncPolicy = in.SyncPolicy.DeepCopy()
	}
	return out
}

// DeepCopy returns a deep copy of the ApplicationSource.
func (in *ApplicationSource) DeepCopy() *ApplicationSource {
	if in == nil {
		return nil
	}
	out := new(ApplicationSource)
	*out = *in
	if in.Helm != nil {
		helm := *in.Helm
		out.Helm = &helm
	}
	return out
}

// DeepCopy returns a deep copy of the SyncPolicy.
func (in *SyncPolicy) DeepCopy() *SyncPolicy {
	if in == nil {
		return nil
	}
	out := new(SyncPolicy)
	*out = *in
	if in.Automated != nil {
		automated := *in.Automated
		out.Automated = &automated
	}
	if in.SyncOptions != nil {
		out.SyncOptions = append([]string(nil), in.SyncOptions...)
	}
	if in.Retry != nil {
		out.Retry = in.Retry.DeepCopy()
	}
	return out
}

// DeepCopy returns a deep copy of the RetryStrategy.
func (in *RetryStrategy) DeepCopy() *RetryStrategy {
	if in == nil {
		return nil
	}
	out := new(RetryStrategy)
	*out = *in
	if in.Backoff != nil {
		backoff := *in.Backoff
		if in.Backoff.Factor != nil {
			factor := *in.Backoff.Factor
			backoff.Factor = &factor
		}
		out.Backoff = &backoff
	}
	return out
}

// DeepCopy returns a deep copy of the ApplicationStatus.
func (in *ApplicationStatus) DeepCopy() *ApplicationStatus {
	if in == nil {
		return nil
	}
	out := new(ApplicationStatus)
	*out = *in
	if in.OperationState != nil {
		state := *in.OperationState
		out.OperationState = &state
	}
	if in.Conditions != nil {
		out.Conditions = append([]ApplicationCondition(nil), in.Conditions...)
	}
	return out
}
