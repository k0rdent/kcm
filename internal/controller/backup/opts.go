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
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// createOpt is a function type for configuring Velero backup options.
type createOpt func(*velerov1.Backup)

// withScheduleLabel returns a createOpt that adds a schedule label to a backup,
// associating it with a ManagementBackup resource.
func withScheduleLabel(scheduleName string) createOpt {
	return func(b *velerov1.Backup) {
		if b.Labels == nil {
			b.Labels = make(map[string]string)
		}
		b.Labels[scheduleMgmtNameLabel] = scheduleName
	}
}

// withStorageLocation returns a createOpt that sets the storage location for a backup.
func withStorageLocation(loc string) createOpt {
	return func(b *velerov1.Backup) {
		b.Spec.StorageLocation = loc
	}
}

// withRegionLabel returns a createOpt that adds a region label to a backup
// if the region is not empty.
func withRegionLabel(region string) createOpt {
	return func(b *velerov1.Backup) {
		if region == "" {
			return
		}
		if b.Labels == nil {
			b.Labels = make(map[string]string)
		}
		b.Labels[kcmv1.KCMRegionLabelKey] = region
	}
}
