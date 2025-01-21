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

			actualB, actualExists := getMostRecentProducedBackup(scheduleName, backups)
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
		rn := time.Duration(rand.Intn(100) * int(time.Minute))
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
