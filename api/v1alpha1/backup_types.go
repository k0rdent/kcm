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
	velerov1 "github.com/zerospiel/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Name to label most of the HMC-related components.
	// Mostly utilized by the backup feature.
	GenericComponentLabelName = "k0rdent.mirantis.com/component"
	// Component label value for the HMC-related components.
	GenericComponentLabelValueHMC = "hmc"
)

// BackupSpec defines the desired state of Backup
type BackupSpec struct {
	// Oneshot indicates whether the Backup should not be scheduled
	// and rather created immediately and only once.
	Oneshot bool `json:"oneshot,omitempty"`
}

// BackupStatus defines the observed state of Backup
type BackupStatus struct {
	// Reference to the underlying Velero object being managed.
	// Might be either Velero Backup or Schedule.
	Reference *corev1.ObjectReference `json:"reference,omitempty"`
	// Status of the Velero Schedule for the Management scheduled backups.
	// Always absent for the Backups with the .spec.oneshot set to true.
	Schedule *velerov1.ScheduleStatus `json:"schedule,omitempty"`
	// NextAttempt indicates the time when the next scheduled backup will be performed.
	// Always absent for the Backups with the .spec.oneshot set to true.
	NextAttempt *metav1.Time `json:"nextAttempt,omitempty"`
	// Last Velero Backup that has been created.
	LastBackup *velerov1.BackupStatus `json:"lastBackup,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// Backup is the Schema for the backups API
type Backup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupSpec   `json:"spec,omitempty"`
	Status BackupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BackupList contains a list of Backup
type BackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Backup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Backup{}, &BackupList{})
}
