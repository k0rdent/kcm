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
	GenericComponentLabelName = "hmc.mirantis.com/component"
	// Component label value for the HMC-related components.
	GenericComponentLabelValueHMC = "hmc"
)

// ManagementBackupSpec defines the desired state of ManagementBackup
type ManagementBackupSpec struct{}

// ManagementBackupStatus defines the observed state of ManagementBackup
type ManagementBackupStatus struct {
	// Reference to the underlying Velero object being managed.
	// Might be either Velero Backup or Schedule.
	Reference *corev1.ObjectReference `json:"reference,omitempty"`
	// NextAttempt indicates the time when the next scheduled backup will be performed.
	// Always absent for the ManagementBackups with a schedule.
	NextAttempt *metav1.Time `json:"nextAttempt,omitempty"`
	// Last Velero Backup that has been created.
	LastBackup *velerov1.BackupStatus `json:"lastBackup,omitempty"`
	// Status of the Velero Schedule for the Management scheduled backups.
	// Always absent for the ManagementBackups with a schedule.
	Schedule *velerov1.ScheduleStatus `json:"schedule,omitempty"`
	// SchedulePaused indicates if the Velero Schedule is paused.
	SchedulePaused bool `json:"schedulePaused,omitempty"`
}

func (in *ManagementBackupStatus) GetLastBackupCopy() velerov1.BackupStatus {
	if in.LastBackup == nil {
		return velerov1.BackupStatus{}
	}
	return *in.LastBackup
}

func (in *ManagementBackupStatus) GetScheduleCopy() velerov1.ScheduleStatus {
	if in.Schedule == nil {
		return velerov1.ScheduleStatus{}
	}
	return *in.Schedule
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=hmcbackup;mgmtbackup
// +kubebuilder:printcolumn:name="NextBackup",type=string,JSONPath=`.status.nextAttempt`,description="Next scheduled attempt to back up",priority=0
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.schedule.phase`,description="Schedule phase",priority=0
// +kubebuilder:printcolumn:name="SinceLastBackup",type=date,JSONPath=`.status.schedule.lastBackup`,description="Time elapsed since last backup run",priority=1
// +kubebuilder:printcolumn:name="LastBackupStatus",type=string,JSONPath=`.status.lastBackup.phase`,description="Status of last backup run",priority=0
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="Time elapsed since object creation",priority=0
// +kubebuilder:printcolumn:name="Paused",type=boolean,JSONPath=`.status.schedulePaused`,description="Schedule is on pause",priority=1

// ManagementBackup is the Schema for the backups API
type ManagementBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ManagementBackupSpec   `json:"spec,omitempty"`
	Status ManagementBackupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ManagementBackupList contains a list of ManagementBackup
type ManagementBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ManagementBackup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ManagementBackup{}, &ManagementBackupList{})
}
