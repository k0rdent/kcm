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
	"time"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Name to label most of the KCM-related components.
	// Mostly utilized by the backup feature.
	GenericComponentNameLabel = "k0rdent.mirantis.com/component"
	// Component label value for the KCM-related components.
	GenericComponentLabelValueKCM = "kcm"
)

// ManagementBackupSpec defines the desired state of ManagementBackup
type ManagementBackupSpec struct {
	// StorageLocation is the name of a [github.com/vmware-tanzu/velero/pkg/apis/velero/v1.StorageLocation]
	// where the backup should be stored.
	StorageLocation string `json:"storageLocation,omitempty"`
	// Schedule is a Cron expression defining when to run the scheduled [ManagementBackup].
	// If not set, the object is considered to be run only once.
	Schedule string `json:"schedule,omitempty"`
	// PerformOnManagementUpgrade indicates that a single [ManagementBackup]
	// should be created and stored in the [ManagementBackup] storage location if not default
	// before the [Management] release upgrade.
	PerformOnManagementUpgrade bool `json:"performOnManagementUpgrade,omitempty"`
}

// ManagementBackupStatus defines the observed state of ManagementBackup
type ManagementBackupStatus struct {
	// NextAttempt indicates the time when the next backup will be created.
	// Always absent for a single [ManagementBackup].
	NextAttempt *metav1.Time `json:"nextAttempt,omitempty"`
	// Time of the most recently created [github.com/vmware-tanzu/velero/pkg/apis/velero/v1.Backup].
	LastBackupTime *metav1.Time `json:"lastBackupTime,omitempty"`
	// Most recently [github.com/vmware-tanzu/velero/pkg/apis/velero/v1.Backup] that has been created.
	LastBackup *velerov1.BackupStatus `json:"lastBackup,omitempty"`
	// Name of most recently created [github.com/vmware-tanzu/velero/pkg/apis/velero/v1.Backup].
	LastBackupName string `json:"lastBackupName,omitempty"`
	// Error stores messages in case of failed backup creation.
	Error string `json:"error,omitempty"`
}

// IsSchedule checks if an instance of [ManagementBackup] is schedulable.
func (s *ManagementBackup) IsSchedule() bool {
	return s.Spec.Schedule != ""
}

// IsCompleted checks if the latest underlaying backup has been completed.
func (s *ManagementBackup) IsCompleted() bool {
	return s.Status.LastBackup != nil && !s.Status.LastBackup.CompletionTimestamp.IsZero()
}

// TimestampedBackupName returns the backup name related to scheduled [ManagementBackup] based on the given timestamp.
func (s *ManagementBackup) TimestampedBackupName(timestamp time.Time) string {
	return s.Name + "-" + timestamp.Format("20060102150405")
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=kcmbackup;mgmtbackup
// +kubebuilder:printcolumn:name="LastBackupStatus",type=string,JSONPath=`.status.lastBackup.phase`,description="Status of last backup run",priority=0
// +kubebuilder:printcolumn:name="NextBackup",type=string,JSONPath=`.status.nextAttempt`,description="Next scheduled attempt to back up",priority=0
// +kubebuilder:printcolumn:name="SinceLastBackup",type=date,JSONPath=`.status.lastBackupTime`,description="Time elapsed since last backup run",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`,description="Time elapsed since object creation",priority=0
// +kubebuilder:printcolumn:name="Error",type=string,JSONPath=`.status.error`,description="Error during creation",priority=1

// ManagementBackup is the Schema for the managementbackups API
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
