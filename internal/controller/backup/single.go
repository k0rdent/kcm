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
	"context"
	"fmt"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// createAllSingleBackups creates one-time (non-scheduled) backups for management cluster
// and all regions. It ensures only one backup per region is created and skips any that
// have already been created.
func (r *Reconciler) createAllSingleBackups(ctx context.Context, s *scope) (ctrl.Result, error) {
	mgmtBackup := s.mgmtBackup
	now := time.Now().UTC()
	l := ctrl.LoggerFrom(ctx).V(1)

	// ensure RegionsLastBackups is initialized
	if mgmtBackup.Status.RegionsLastBackups == nil {
		mgmtBackup.Status.RegionsLastBackups = []kcmv1.ManagementBackupSingleStatus{}
	}

	// skip if management backup has already been created
	if mgmtBackup.Status.LastBackupName == "" {
		// always create management backup first
		mgmtBackupName := mgmtBackup.Name

		if err := r.createNewVeleroBackup(ctx, r.mgmtCl, "", s, mgmtBackupName,
			withStorageLocation(mgmtBackup.Spec.StorageLocation),
		); err != nil {
			if isMetaError(err) {
				return r.propagateMetaError(ctx, "", mgmtBackup, err.Error())
			}
			return ctrl.Result{}, err
		}

		l.V(1).Info("Created management backup", "new_backup_name", mgmtBackupName)
		mgmtBackup.Status.LastBackupName = mgmtBackupName
		mgmtBackup.Status.LastBackupTime = &metav1.Time{Time: now}
	}

	for region, loadedCl := range s.regionClients {
		if region == "" || !loadedCl.loaded { // sanity check
			l.V(1).Info("Skip single backup creation", "region", region, "client_loaded", loadedCl.loaded)
			continue
		}

		regionBackupStatusIdx := slices.IndexFunc(mgmtBackup.Status.RegionsLastBackups, func(rb kcmv1.ManagementBackupSingleStatus) bool {
			return rb.Region == region
		})

		// skip if this regional backup has already been created
		if regionBackupStatusIdx > -1 && mgmtBackup.Status.RegionsLastBackups[regionBackupStatusIdx].LastBackupName != "" {
			continue
		}

		// create backup name with region suffix
		backupName := mgmtBackup.Name + "-" + region

		// create backup in the appropriate cluster
		if err := r.createNewVeleroBackup(ctx, loadedCl.cl, region, s, backupName,
			withRegionLabel(region),
			withStorageLocation(mgmtBackup.Spec.StorageLocation),
		); err != nil {
			l.Error(err, "failed to create single regional backup", "region", region, "storage_location", mgmtBackup.Spec.StorageLocation)
			// on error set the error to the corresponding backup and proceed to the next region;
			// since it is a single shot, we do care about timings, just set the status,
			// on the next reconcile we will try once again to create a backup for this region
			if isMetaError(err) {
				setMetaError(region, mgmtBackup, err.Error())
				continue
			}
			setError(region, mgmtBackup, err.Error())
			continue
		}

		l.V(1).Info("Created regional backup", "new_backup_name", backupName, "region", region)

		// upsert regional backup status
		if regionBackupStatusIdx > -1 {
			mgmtBackup.Status.RegionsLastBackups[regionBackupStatusIdx].LastBackupName = backupName
			mgmtBackup.Status.RegionsLastBackups[regionBackupStatusIdx].LastBackupTime = &metav1.Time{Time: now}
			mgmtBackup.Status.RegionsLastBackups[regionBackupStatusIdx].Error = ""
		} else {
			mgmtBackup.Status.RegionsLastBackups = append(mgmtBackup.Status.RegionsLastBackups,
				kcmv1.ManagementBackupSingleStatus{
					Region:         region,
					LastBackupName: backupName,
					LastBackupTime: &metav1.Time{Time: now},
				})
		}
	}

	if err := r.mgmtCl.Status().Update(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status: %w", mgmtBackup.Name, err)
	}

	return ctrl.Result{}, nil
}
