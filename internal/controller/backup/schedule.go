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
	"time"

	cron "github.com/robfig/cron/v3"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// handleScheduledBackup processes a scheduled ManagementBackup.
// It checks if it's time to create a new backup according to the cron schedule,
// ensures no backups are currently in progress, and updates next attempt time.
func (r *Reconciler) handleScheduledBackup(ctx context.Context, s *scope) (ctrl.Result, error) {
	mgmtBackup := s.mgmtBackup
	cronSchedule, err := cron.ParseStandard(mgmtBackup.Spec.Schedule)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse cron schedule %s: %w", mgmtBackup.Spec.Schedule, err)
	}

	isDue, nextAttemptTime := getNextAttemptTime(mgmtBackup, cronSchedule)

	// here we can put as many conditions as we want, e.g. if upgrade is progressing
	isOkayToCreateBackup := isDue && !r.areAnyBackupsProgressing(ctx, s)

	if isOkayToCreateBackup {
		return r.createAllScheduledBackups(ctx, s, nextAttemptTime)
	}

	// update next attempt time if needed
	// we check only the mgmt backup status because all regional backups should have the same next attempt time
	newNextAttemptTime := &metav1.Time{Time: nextAttemptTime}
	if !mgmtBackup.Status.NextAttempt.Equal(newNextAttemptTime) {
		mgmtBackup.Status.NextAttempt = newNextAttemptTime

		// update all regional backups with the same next attempt time
		for i := range mgmtBackup.Status.RegionsLastBackups {
			mgmtBackup.Status.RegionsLastBackups[i].NextAttempt = newNextAttemptTime
		}

		if err := r.mgmtCl.Status().Update(ctx, mgmtBackup); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status with next attempt time: %w", mgmtBackup.Name, err)
		}
	}

	// if any backups already exist, update their status
	if anyBackupInitiated(mgmtBackup) {
		return r.updateAllBackupsStatus(ctx, s)
	}

	return ctrl.Result{}, nil
}

// anyBackupInitiated checks if ANY backup has been initiated (management or regional).
// Returns true if at least one backup has a name, false if none have been created.
func anyBackupInitiated(mgmtBackup *kcmv1.ManagementBackup) bool {
	if mgmtBackup.Status.LastBackupName != "" {
		return true
	}

	for _, rb := range mgmtBackup.Status.RegionsLastBackups {
		if rb.LastBackupName != "" {
			return true
		}
	}

	return false
}

// createAllScheduledBackups creates scheduled backups for management cluster and all regions.
// It tracks which regions have been processed to avoid duplicates, and updates the
// ManagementBackup status with backup names, timestamps, and next attempt time.
func (r *Reconciler) createAllScheduledBackups(ctx context.Context, s *scope, nextAttemptTime time.Time) (ctrl.Result, error) {
	mgmtBackup := s.mgmtBackup
	now := time.Now().UTC()
	ldebug := ctrl.LoggerFrom(ctx).V(1)

	// ensure RegionsLastBackups is initialized
	if mgmtBackup.Status.RegionsLastBackups == nil {
		mgmtBackup.Status.RegionsLastBackups = []kcmv1.ManagementBackupSingleStatus{}
	}

	processedRegions := make(map[string]bool)

	mgmtBackupName := mgmtBackup.TimestampedBackupName(now, "")
	if err := r.createNewVeleroBackup(ctx, r.mgmtCl, "", s, mgmtBackupName,
		withStorageLocation(mgmtBackup.Spec.StorageLocation),
		withScheduleLabel(mgmtBackup.Name),
	); err != nil {
		if isMetaError(err) {
			return r.propagateMetaError(ctx, "", mgmtBackup, err.Error())
		}
		return ctrl.Result{}, err
	}

	ldebug.Info("Created scheduled management backup", "new_backup_name", mgmtBackupName)
	mgmtBackup.Status.LastBackupName = mgmtBackupName
	mgmtBackup.Status.LastBackupTime = &metav1.Time{Time: now}
	mgmtBackup.Status.NextAttempt = &metav1.Time{Time: nextAttemptTime}
	processedRegions[""] = true

	for _, deploy := range s.clientsByDeployment {
		region := deploy.cld.Status.Region

		// skip management cluster (already processed) or duplicate regions
		if region == "" || processedRegions[region] {
			continue
		}
		processedRegions[region] = true

		// generate backup name with timestamp and region suffix
		backupName := mgmtBackup.TimestampedBackupName(now, region)

		// create backup in the appropriate cluster
		if err := r.createNewVeleroBackup(ctx, deploy.cl, region, s, backupName,
			withRegionLabel(region),
			withStorageLocation(mgmtBackup.Spec.StorageLocation),
			withScheduleLabel(mgmtBackup.Name),
		); err != nil {
			if isMetaError(err) {
				return r.propagateMetaError(ctx, region, mgmtBackup, err.Error())
			}
			return ctrl.Result{}, err
		}

		ldebug.Info("Created scheduled regional backup", "new_backup_name", backupName, "region", region)

		// find or create regional backup status
		found := false
		for i, rb := range mgmtBackup.Status.RegionsLastBackups {
			if rb.Region == region {
				mgmtBackup.Status.RegionsLastBackups[i].LastBackupName = backupName
				mgmtBackup.Status.RegionsLastBackups[i].LastBackupTime = &metav1.Time{Time: now}
				mgmtBackup.Status.RegionsLastBackups[i].NextAttempt = &metav1.Time{Time: nextAttemptTime}
				found = true
				break
			}
		}

		if !found {
			mgmtBackup.Status.RegionsLastBackups = append(mgmtBackup.Status.RegionsLastBackups,
				kcmv1.ManagementBackupSingleStatus{
					Region:         region,
					LastBackupName: backupName,
					LastBackupTime: &metav1.Time{Time: now},
					NextAttempt:    &metav1.Time{Time: nextAttemptTime},
				})
		}
	}

	if err := r.mgmtCl.Status().Update(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status: %w", mgmtBackup.Name, err)
	}

	return ctrl.Result{}, nil
}

// areAnyBackupsProgressing checks if any backup (management or regional) is currently
// in progress (in New or InProgress phase). Returns true if any backup is progressing,
// or if status cannot be determined due to errors.
func (r *Reconciler) areAnyBackupsProgressing(ctx context.Context, s *scope) bool {
	ldebug := ctrl.LoggerFrom(ctx).V(1)
	ldebug.Info("Checking for progressing backups")

	listOpts := []client.ListOption{
		client.InNamespace(r.systemNamespace),
		client.MatchingLabels{scheduleMgmtNameLabel: s.mgmtBackup.Name},
	}

	backups := &velerov1.BackupList{}
	if err := r.mgmtCl.List(ctx, backups, listOpts...); err != nil {
		ldebug.Error(err, "Failed to list backups in management cluster")
		return true
	}

	for _, backup := range backups.Items {
		if backup.Status.Phase == velerov1.BackupPhaseNew ||
			backup.Status.Phase == velerov1.BackupPhaseInProgress {
			ldebug.Info("Found progressing backup in management cluster", "backup", backup.Name)
			return true
		}
	}

	for _, deploy := range s.clientsByDeployment {
		region := deploy.cld.Status.Region
		if region == "" {
			continue
		}

		backups := &velerov1.BackupList{}
		if err := deploy.cl.List(ctx, backups, listOpts...); err != nil {
			ldebug.Error(err, "Failed to list backups in region", "region", region)
			return true
		}

		for _, backup := range backups.Items {
			if backup.Status.Phase == velerov1.BackupPhaseNew ||
				backup.Status.Phase == velerov1.BackupPhaseInProgress {
				ldebug.Info("Found progressing backup in region", "backup", backup.Name, "region", region)
				return true
			}
		}
	}

	return false
}

// getNextAttemptTime calculates the next backup attempt time based on the cron schedule.
// Returns a boolean indicating if a backup is due and the next attempt time.
func getNextAttemptTime(schedule *kcmv1.ManagementBackup, cronSchedule cron.Schedule) (bool, time.Time) {
	lastBackupTime := schedule.CreationTimestamp.Time
	if !schedule.Status.LastBackupTime.IsZero() {
		lastBackupTime = schedule.Status.LastBackupTime.Time
	}

	nextAttemptTime := cronSchedule.Next(lastBackupTime) // might be in past so rely on now
	now := time.Now().UTC()
	isDue := now.After(nextAttemptTime)
	if isDue {
		nextAttemptTime = now
	}

	return isDue, nextAttemptTime
}
