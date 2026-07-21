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

package backup

import (
	"testing"
	"testing/synctest"
	"time"

	cron "github.com/robfig/cron/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func Test_anyBackupInitiated(t *testing.T) {
	tests := []struct {
		name       string
		mgmtBackup *kcmv1.ManagementBackup
		want       bool
	}{
		{
			name: "no backups initiated",
			mgmtBackup: &kcmv1.ManagementBackup{
				Status: kcmv1.ManagementBackupStatus{
					ManagementBackupSingleStatus: kcmv1.ManagementBackupSingleStatus{
						LastBackupName: "",
					},
					RegionsLastBackups: []kcmv1.ManagementBackupSingleStatus{
						{
							Region:         "region1",
							LastBackupName: "",
						},
						{
							Region:         "region2",
							LastBackupName: "",
						},
					},
				},
			},
			want: false,
		},
		{
			name: "only management backup initiated",
			mgmtBackup: &kcmv1.ManagementBackup{
				Status: kcmv1.ManagementBackupStatus{
					ManagementBackupSingleStatus: kcmv1.ManagementBackupSingleStatus{
						LastBackupName: "management-backup",
					},
					RegionsLastBackups: []kcmv1.ManagementBackupSingleStatus{
						{
							Region:         "region1",
							LastBackupName: "",
						},
						{
							Region:         "region2",
							LastBackupName: "",
						},
					},
				},
			},
			want: true,
		},
		{
			name: "only regional backup initiated",
			mgmtBackup: &kcmv1.ManagementBackup{
				Status: kcmv1.ManagementBackupStatus{
					ManagementBackupSingleStatus: kcmv1.ManagementBackupSingleStatus{
						LastBackupName: "",
					},
					RegionsLastBackups: []kcmv1.ManagementBackupSingleStatus{
						{
							Region:         "region1",
							LastBackupName: "region1-backup",
						},
						{
							Region:         "region2",
							LastBackupName: "",
						},
					},
				},
			},
			want: true,
		},
		{
			name: "all backups initiated",
			mgmtBackup: &kcmv1.ManagementBackup{
				Status: kcmv1.ManagementBackupStatus{
					ManagementBackupSingleStatus: kcmv1.ManagementBackupSingleStatus{
						LastBackupName: "management-backup",
					},
					RegionsLastBackups: []kcmv1.ManagementBackupSingleStatus{
						{
							Region:         "region1",
							LastBackupName: "region1-backup",
						},
						{
							Region:         "region2",
							LastBackupName: "region2-backup",
						},
					},
				},
			},
			want: true,
		},
		{
			name: "empty regions list",
			mgmtBackup: &kcmv1.ManagementBackup{
				Status: kcmv1.ManagementBackupStatus{
					ManagementBackupSingleStatus: kcmv1.ManagementBackupSingleStatus{
						LastBackupName: "",
					},
					RegionsLastBackups: []kcmv1.ManagementBackupSingleStatus{},
				},
			},
			want: false,
		},
		{
			name: "nil regions list",
			mgmtBackup: &kcmv1.ManagementBackup{
				Status: kcmv1.ManagementBackupStatus{
					ManagementBackupSingleStatus: kcmv1.ManagementBackupSingleStatus{
						LastBackupName: "",
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anyBackupInitiated(tt.mgmtBackup)
			if got != tt.want {
				t.Errorf("anyBackupInitiated() = %v, want %v", got, tt.want)
			}
		})
	}
}

type mockCronSchedule struct{ nextTime time.Time }

func (m mockCronSchedule) Next(time.Time) time.Time { return m.nextTime }

func Test_getNextAttemptTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now().UTC()
		future := now.Add(time.Hour)
		past := now.Add(-time.Hour)

		tests := []struct {
			name         string
			schedule     *kcmv1.ManagementBackup
			cronSchedule cron.Schedule
			wantDue      bool
			wantTime     time.Time
		}{
			{
				name: "next attempt in future",
				schedule: &kcmv1.ManagementBackup{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(past),
					},
					Status: kcmv1.ManagementBackupStatus{
						ManagementBackupSingleStatus: kcmv1.ManagementBackupSingleStatus{
							LastBackupTime: &metav1.Time{Time: past},
						},
					},
				},
				cronSchedule: mockCronSchedule{nextTime: future},
				wantTime:     future,
			},
			{
				name: "next attempt in past",
				schedule: &kcmv1.ManagementBackup{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(past.Add(-2 * time.Hour)),
					},
					Status: kcmv1.ManagementBackupStatus{
						ManagementBackupSingleStatus: kcmv1.ManagementBackupSingleStatus{
							LastBackupTime: &metav1.Time{Time: past.Add(-time.Hour)},
						},
					},
				},
				cronSchedule: mockCronSchedule{nextTime: past},
				wantDue:      true,
				wantTime:     now,
			},
			{
				name: "no last backup time uses creation time",
				schedule: &kcmv1.ManagementBackup{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(past.Add(-2 * time.Hour)),
					},
					Status: kcmv1.ManagementBackupStatus{
						ManagementBackupSingleStatus: kcmv1.ManagementBackupSingleStatus{
							LastBackupTime: &metav1.Time{},
						},
					},
				},
				cronSchedule: mockCronSchedule{nextTime: past},
				wantDue:      true,
				wantTime:     now,
			},
			{
				name: "next attempt exact now",
				schedule: &kcmv1.ManagementBackup{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(past),
					},
					Status: kcmv1.ManagementBackupStatus{
						ManagementBackupSingleStatus: kcmv1.ManagementBackupSingleStatus{
							LastBackupTime: &metav1.Time{Time: past},
						},
					},
				},
				cronSchedule: mockCronSchedule{nextTime: now},
				wantDue:      true,
				wantTime:     now,
			},
		}

		for _, tt := range tests {
			gotDue, gotTime := getNextAttemptTime(tt.schedule, tt.cronSchedule)
			if gotDue != tt.wantDue {
				t.Errorf("%s: getNextAttemptTime() due = %v, want %v", tt.name, gotDue, tt.wantDue)
			}
			if !gotTime.Equal(tt.wantTime) {
				t.Errorf("%s: getNextAttemptTime() time = %v, want %v", tt.name, gotTime, tt.wantTime)
			}
		}
	})
}
