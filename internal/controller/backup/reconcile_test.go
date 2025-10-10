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

package backup

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const tsFormat = "20060102150405"

func Test_getMostRecentProducedBackup(t *testing.T) {
	const scheduleName = "test-schedule-name"

	now := time.Now().In(time.UTC)

	timeToTS := func(input time.Time) string {
		return input.Format(tsFormat)
	}

	tcases := []struct {
		name                string
		genOpts             []createOpt
		expectedNameTS      string
		expectedExistsValue bool
	}{
		{
			name:    "no backups from schedule",
			genOpts: []createOpt{withScheduleLabel("another-schedule")},
		},
		{
			name:    "all backups are in future",
			genOpts: []createOpt{futureName()},
		},
		{
			name:    "wrong backup name format",
			genOpts: []createOpt{wrongFormatName()},
		},
		{
			name:                "correct input",
			expectedNameTS:      timeToTS(now),
			expectedExistsValue: true,
		},
	}

	for _, tc := range tcases {
		t.Run(tc.name, func(t *testing.T) {
			backups := generateVeleroBackups(t, scheduleName, now, 10, tc.genOpts...)

			actualB, actualExists := getMostRecentlyProducedBackup(scheduleName, backups, "")
			if tc.expectedExistsValue != actualExists {
				t.Errorf("%s: actual '%v'; want: '%v'", tc.name, actualExists, tc.expectedExistsValue)
			}

			if tc.expectedExistsValue {
				actualNameTS := actualB.Name[strings.LastIndexByte(actualB.Name, '-')+1:]
				if tc.expectedNameTS != actualNameTS {
					t.Errorf("%s: actual '%v'; want: '%v'", tc.name, actualNameTS, tc.expectedNameTS)
				}
			}
		})
	}
}

func futureName() createOpt {
	return func(b *velerov1.Backup) {
		rn := time.Duration((rand.Intn(100) + 1) * int(time.Minute))
		future := time.Now().Add(rn)
		b.Name = b.Name[:strings.LastIndexByte(b.Name, '-')] + "-" + future.Format(tsFormat)
	}
}

func wrongFormatName() createOpt {
	return func(b *velerov1.Backup) {
		b.Name = b.Name[:strings.LastIndexByte(b.Name, '-')] // trim ts
	}
}

func generateVeleroBackups(t *testing.T, scheduleName string, now time.Time, n int, opts ...createOpt) []velerov1.Backup {
	t.Helper()

	past := now.Add(24 * time.Hour)

	name := func(b *velerov1.Backup) {
		b.Name = scheduleName + "-" + past.Format(tsFormat)
	}

	label := func(b *velerov1.Backup) {
		if b.Labels == nil {
			b.Labels = make(map[string]string)
		}
		b.Labels[scheduleMgmtNameLabel] = scheduleName
	}

	ret := make([]velerov1.Backup, n)
	for i := range n {
		instance := &velerov1.Backup{}
		name(instance)
		label(instance)
		for _, o := range opts {
			o(instance)
		}
		ret[i] = *instance
		past = past.Add(-24 * time.Hour)
	}

	return ret
}

func Test_isRestored(t *testing.T) {
	tests := []struct {
		name       string
		mgmtBackup *kcmv1.ManagementBackup
		want       bool
	}{
		{
			name: "backup has velero restoration labels",
			mgmtBackup: &kcmv1.ManagementBackup{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						velerov1.BackupNameLabel:  "backup-name",
						velerov1.RestoreNameLabel: "restore-name",
					},
				},
			},
			want: true,
		},
		{
			name: "missing restore name label",
			mgmtBackup: &kcmv1.ManagementBackup{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						velerov1.BackupNameLabel: "backup-name",
					},
				},
			},
			want: false,
		},
		{
			name: "missing backup name label",
			mgmtBackup: &kcmv1.ManagementBackup{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						velerov1.RestoreNameLabel: "restore-name",
					},
				},
			},
			want: false,
		},
		{
			name: "no labels",
			mgmtBackup: &kcmv1.ManagementBackup{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
			},
			want: false,
		},
		{
			name: "nil labels",
			mgmtBackup: &kcmv1.ManagementBackup{
				ObjectMeta: metav1.ObjectMeta{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRestored(tt.mgmtBackup)
			if got != tt.want {
				t.Errorf("isRestored() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_backupStatusEqual(t *testing.T) {
	timeNow := metav1.Now()

	tests := []struct {
		name string
		a    *velerov1.BackupStatus
		b    *velerov1.BackupStatus
		want bool
	}{
		{
			name: "identical statuses",
			a: &velerov1.BackupStatus{
				Phase:               velerov1.BackupPhaseCompleted,
				StartTimestamp:      &timeNow,
				CompletionTimestamp: &timeNow,
				Errors:              10,
				Warnings:            5,
			},
			b: &velerov1.BackupStatus{
				Phase:               velerov1.BackupPhaseCompleted,
				StartTimestamp:      &timeNow,
				CompletionTimestamp: &timeNow,
				Errors:              10,
				Warnings:            5,
			},
			want: true,
		},
		{
			name: "different phase",
			a: &velerov1.BackupStatus{
				Phase:               velerov1.BackupPhaseCompleted,
				StartTimestamp:      &timeNow,
				CompletionTimestamp: &timeNow,
			},
			b: &velerov1.BackupStatus{
				Phase:               velerov1.BackupPhaseFailed,
				StartTimestamp:      &timeNow,
				CompletionTimestamp: &timeNow,
			},
			want: false,
		},
		{
			name: "different start timestamp",
			a: &velerov1.BackupStatus{
				Phase:               velerov1.BackupPhaseCompleted,
				StartTimestamp:      &timeNow,
				CompletionTimestamp: &timeNow,
			},
			b: &velerov1.BackupStatus{
				Phase:               velerov1.BackupPhaseCompleted,
				StartTimestamp:      &metav1.Time{Time: timeNow.Add(1 * time.Hour)},
				CompletionTimestamp: &timeNow,
			},
			want: false,
		},
		{
			name: "different completion timestamp",
			a: &velerov1.BackupStatus{
				Phase:               velerov1.BackupPhaseCompleted,
				StartTimestamp:      &timeNow,
				CompletionTimestamp: &timeNow,
			},
			b: &velerov1.BackupStatus{
				Phase:               velerov1.BackupPhaseCompleted,
				StartTimestamp:      &timeNow,
				CompletionTimestamp: &metav1.Time{Time: timeNow.Add(1 * time.Hour)},
			},
			want: false,
		},
		{
			name: "different errors count",
			a: &velerov1.BackupStatus{
				Phase:  velerov1.BackupPhaseCompleted,
				Errors: 10,
			},
			b: &velerov1.BackupStatus{
				Phase:  velerov1.BackupPhaseCompleted,
				Errors: 5,
			},
			want: false,
		},
		{
			name: "different warnings count",
			a: &velerov1.BackupStatus{
				Phase:    velerov1.BackupPhaseCompleted,
				Warnings: 10,
			},
			b: &velerov1.BackupStatus{
				Phase:    velerov1.BackupPhaseCompleted,
				Warnings: 5,
			},
			want: false,
		},
		{
			name: "nil timestamps should not panic",
			a: &velerov1.BackupStatus{
				Phase:               velerov1.BackupPhaseCompleted,
				StartTimestamp:      nil,
				CompletionTimestamp: nil,
			},
			b: &velerov1.BackupStatus{
				Phase:               velerov1.BackupPhaseCompleted,
				StartTimestamp:      nil,
				CompletionTimestamp: nil,
			},
			want: true,
		},
		{
			name: "one nil timestamp",
			a: &velerov1.BackupStatus{
				Phase:               velerov1.BackupPhaseCompleted,
				StartTimestamp:      &timeNow,
				CompletionTimestamp: nil,
			},
			b: &velerov1.BackupStatus{
				Phase:               velerov1.BackupPhaseCompleted,
				StartTimestamp:      &timeNow,
				CompletionTimestamp: &timeNow,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := backupStatusEqual(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("backupStatusEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}
