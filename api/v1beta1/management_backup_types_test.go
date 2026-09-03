// Copyright 2026
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
	"testing"
	"time"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestManagementBackupIsSchedule(t *testing.T) {
	if !(&ManagementBackup{Spec: ManagementBackupSpec{Schedule: "@daily"}}).IsSchedule() {
		t.Error("IsSchedule() = false, want true")
	}
	if (&ManagementBackup{}).IsSchedule() {
		t.Error("IsSchedule() = true, want false")
	}
}

func TestManagementBackupIsCompleted(t *testing.T) {
	completedTS := metav1.Now()

	t.Run("no LastBackup: not completed", func(t *testing.T) {
		mb := &ManagementBackup{}
		if mb.IsCompleted() {
			t.Error("IsCompleted() = true, want false")
		}
	})

	t.Run("LastBackup with no completion timestamp: not completed", func(t *testing.T) {
		mb := &ManagementBackup{Status: ManagementBackupStatus{
			ManagementBackupSingleStatus: ManagementBackupSingleStatus{LastBackup: &velerov1.BackupStatus{}},
		}}
		if mb.IsCompleted() {
			t.Error("IsCompleted() = true, want false")
		}
	})

	t.Run("LastBackup completed, no region backups: completed", func(t *testing.T) {
		mb := &ManagementBackup{Status: ManagementBackupStatus{
			ManagementBackupSingleStatus: ManagementBackupSingleStatus{LastBackup: &velerov1.BackupStatus{CompletionTimestamp: &completedTS}},
		}}
		if !mb.IsCompleted() {
			t.Error("IsCompleted() = false, want true")
		}
	})

	t.Run("LastBackup completed but a region backup is not: not completed", func(t *testing.T) {
		mb := &ManagementBackup{Status: ManagementBackupStatus{
			ManagementBackupSingleStatus: ManagementBackupSingleStatus{LastBackup: &velerov1.BackupStatus{CompletionTimestamp: &completedTS}},
			RegionsLastBackups: []ManagementBackupSingleStatus{
				{LastBackup: &velerov1.BackupStatus{}},
			},
		}}
		if mb.IsCompleted() {
			t.Error("IsCompleted() = true, want false")
		}
	})

	t.Run("all region backups and last backup completed: completed", func(t *testing.T) {
		mb := &ManagementBackup{Status: ManagementBackupStatus{
			ManagementBackupSingleStatus: ManagementBackupSingleStatus{LastBackup: &velerov1.BackupStatus{CompletionTimestamp: &completedTS}},
			RegionsLastBackups: []ManagementBackupSingleStatus{
				{LastBackup: &velerov1.BackupStatus{CompletionTimestamp: &completedTS}},
			},
		}}
		if !mb.IsCompleted() {
			t.Error("IsCompleted() = false, want true")
		}
	})
}

func TestManagementBackupTimestampedBackupName(t *testing.T) {
	mb := &ManagementBackup{ObjectMeta: metav1.ObjectMeta{Name: "mb1"}}
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	if got := mb.TimestampedBackupName(ts, ""); got != "mb1-20260102030405" {
		t.Errorf("got %q, want %q", got, "mb1-20260102030405")
	}
	if got := mb.TimestampedBackupName(ts, "region1"); got != "mb1-region1-20260102030405" {
		t.Errorf("got %q, want %q", got, "mb1-region1-20260102030405")
	}
}
