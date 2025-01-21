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
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	cron "github.com/robfig/cron/v3"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/utils"
)

// scheduleMgmtNameLabel holds a reference to the [github.com/K0rdent/kcm/api/v1alpha1.ManagementBackup] object name.
const scheduleMgmtNameLabel = "k0rdent.mirantis.com/management-backup"

func (r *Reconciler) ReconcileBackup(ctx context.Context, mgmtBackup *kcmv1alpha1.ManagementBackup) (ctrl.Result, error) {
	if mgmtBackup == nil {
		return ctrl.Result{}, nil
	}

	if updated, err := utils.AddKCMComponentLabel(ctx, r.cl, mgmtBackup); updated || err != nil { // put all mgmtbackup to backup
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add component label: %w", err)
		}
		return ctrl.Result{}, nil
	}

	if isRestored(mgmtBackup) {
		return r.updateAfterRestoration(ctx, mgmtBackup)
	}

	if mgmtBackup.IsSchedule() { // schedule-creation path
		cronSchedule, err := cron.ParseStandard(mgmtBackup.Spec.Schedule)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to parse cron schedule %s: %w", mgmtBackup.Spec.Schedule, err)
		}

		isDue, nextAttemptTime := getNextAttemptTime(mgmtBackup, cronSchedule)

		// here we can put as many conditions as we want, e.g. if upgrade is progressing
		// TODO: add a condition to check if management upgrade is progressing
		isOkayToCreateBackup := isDue && !r.isVeleroBackupProgressing(ctx, mgmtBackup)

		if isOkayToCreateBackup {
			return r.createScheduleBackup(ctx, mgmtBackup, nextAttemptTime)
		}

		newNextAttemptTime := &metav1.Time{Time: nextAttemptTime}
		if !mgmtBackup.Status.NextAttempt.Equal(newNextAttemptTime) {
			mgmtBackup.Status.NextAttempt = newNextAttemptTime

			if err := r.cl.Status().Update(ctx, mgmtBackup); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status with next attempt time: %w", mgmtBackup.Name, err)
			}
		}

		if mgmtBackup.Status.LastBackupName == "" { // is not due, nothing to do
			return ctrl.Result{}, nil
		}
	} else if mgmtBackup.Status.LastBackupName == "" && !isRestored(mgmtBackup) { // single mgmtbackup, velero backup has not been created yet
		return r.createSingleBackup(ctx, mgmtBackup)
	}

	l := ctrl.LoggerFrom(ctx)
	l.V(1).Info("Collecting backup status")

	backupName := mgmtBackup.Name
	if mgmtBackup.IsSchedule() {
		backupName = mgmtBackup.Status.LastBackupName
	}
	veleroBackup := new(velerov1.Backup)
	if err := r.cl.Get(ctx, client.ObjectKey{
		Name:      backupName,
		Namespace: r.systemNamespace,
	}, veleroBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get velero Backup: %w", err)
	}

	l.V(1).Info("Updating backup status")
	mgmtBackup.Status.LastBackup = &veleroBackup.Status
	if err := r.cl.Status().Update(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status: %w", mgmtBackup.Name, err)
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) updateAfterRestoration(ctx context.Context, mgmtBackup *kcmv1alpha1.ManagementBackup) (ctrl.Result, error) {
	removeVeleroLabels := func() {
		delete(mgmtBackup.Labels, velerov1.BackupNameLabel)
		delete(mgmtBackup.Labels, velerov1.RestoreNameLabel)
	}

	l := ctrl.LoggerFrom(ctx).V(1)

	if mgmtBackup.Status.LastBackup != nil || mgmtBackup.Status.LastBackupName != "" || !mgmtBackup.Status.LastBackupTime.IsZero() { // fast-track
		l.Info("Removing velero labels after restoration when status is already set")
		removeVeleroLabels()
		if err := r.cl.Update(ctx, mgmtBackup); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup labels after restoration: %w", err)
		}

		return ctrl.Result{}, nil
	}

	veleroBackups := new(velerov1.BackupList)
	if err := r.cl.List(ctx, veleroBackups); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list velero Backups: %w", err)
	}

	updateStatus := false
	if mgmtBackup.IsSchedule() {
		l.Info("Updating schedule after restoration")
		lastBackup, ok := getMostRecentProducedBackup(mgmtBackup.Name, veleroBackups.Items)
		if ok { // if have not found then there were no backups yet
			l.Info("Found last backup", "last_backup_name", lastBackup.Name)
			// next attempt will be fetched on the next event
			mgmtBackup.Status.LastBackup = &lastBackup.Status
			mgmtBackup.Status.LastBackupName = lastBackup.Name
			mgmtBackup.Status.LastBackupTime = lastBackup.Status.StartTimestamp
			updateStatus = true
		} else {
			l.Info("No last backup has been found")
		}
	} else {
		l.Info("Updating single backup after restoration")
		for _, v := range veleroBackups.Items {
			if mgmtBackup.Name == v.Name {
				mgmtBackup.Status.LastBackup = &v.Status
				mgmtBackup.Status.LastBackupName = v.Name
				mgmtBackup.Status.LastBackupTime = v.Status.StartTimestamp
				updateStatus = true
				break
			}
		}
	}

	if updateStatus {
		l.Info("Updating status after restoration")
		if err := r.cl.Status().Update(ctx, mgmtBackup); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup status after restoration: %w", err)
		}

		return ctrl.Result{}, nil
	}

	l.Info("Removing velero labels after restoration without status set")
	removeVeleroLabels()
	if err := r.cl.Update(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup labels after restoration: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) createScheduleBackup(ctx context.Context, mgmtBackup *kcmv1alpha1.ManagementBackup, nextAttemptTime time.Time) (ctrl.Result, error) {
	now := time.Now().In(time.UTC)
	backupName := mgmtBackup.TimestampedBackupName(now)

	if err := r.createNewVeleroBackup(ctx, backupName, withScheduleLabel(mgmtBackup.Name), withStorageLocation(mgmtBackup.Spec.StorageLocation)); err != nil {
		if isMetaError(err) {
			return r.propagateMetaError(ctx, mgmtBackup, err.Error())
		}
		return ctrl.Result{}, err
	}

	mgmtBackup.Status.LastBackupName = backupName
	mgmtBackup.Status.LastBackupTime = &metav1.Time{Time: now}
	mgmtBackup.Status.NextAttempt = &metav1.Time{Time: nextAttemptTime}

	if err := r.cl.Status().Update(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status: %w", mgmtBackup.Name, err)
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) createSingleBackup(ctx context.Context, mgmtBackup *kcmv1alpha1.ManagementBackup) (ctrl.Result, error) {
	if err := r.createNewVeleroBackup(ctx, mgmtBackup.Name, withStorageLocation(mgmtBackup.Spec.StorageLocation)); err != nil {
		if isMetaError(err) {
			return r.propagateMetaError(ctx, mgmtBackup, err.Error())
		}
		return ctrl.Result{}, err
	}

	mgmtBackup.Status.LastBackupName = mgmtBackup.Name
	mgmtBackup.Status.LastBackupTime = &metav1.Time{Time: time.Now().In(time.UTC)}

	if err := r.cl.Status().Update(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status: %w", mgmtBackup.Name, err)
	}

	return ctrl.Result{}, nil
}

type createOpt func(*velerov1.Backup)

func withScheduleLabel(scheduleName string) createOpt {
	return func(b *velerov1.Backup) {
		if b.Labels == nil {
			b.Labels = make(map[string]string)
		}
		b.Labels[scheduleMgmtNameLabel] = scheduleName
	}
}

func withStorageLocation(loc string) createOpt {
	return func(b *velerov1.Backup) {
		b.Spec.StorageLocation = loc
	}
}

func (r *Reconciler) createNewVeleroBackup(ctx context.Context, backupName string, createOpts ...createOpt) error {
	l := ctrl.LoggerFrom(ctx)

	veleroBackup, err := r.getNewVeleroBackup(ctx, backupName)
	if err != nil {
		return err
	}

	for _, o := range createOpts {
		o(veleroBackup)
	}

	if err := r.cl.Create(ctx, veleroBackup); client.IgnoreAlreadyExists(err) != nil { // avoid err-loop on status update error
		return fmt.Errorf("failed to create velero Backup: %w", err)
	}

	l.V(1).Info("Velero Backup has been created", "new_backup_name", client.ObjectKeyFromObject(veleroBackup))
	return nil
}

func (r *Reconciler) getNewVeleroBackup(ctx context.Context, backupName string) (*velerov1.Backup, error) {
	templateSpec, err := getBackupTemplateSpec(ctx, r.cl)
	if err != nil {
		return nil, fmt.Errorf("failed to construct velero backup spec: %w", err)
	}

	veleroBackup := &velerov1.Backup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: velerov1.SchemeGroupVersion.String(),
			Kind:       "Backup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupName,
			Namespace: r.systemNamespace,
		},
		Spec: *templateSpec,
	}

	return veleroBackup, nil
}

func (r *Reconciler) isVeleroBackupProgressing(ctx context.Context, schedule *kcmv1alpha1.ManagementBackup) bool {
	backups := &velerov1.BackupList{}
	if err := r.cl.List(ctx, backups, client.InNamespace(r.systemNamespace), client.MatchingLabels{scheduleMgmtNameLabel: schedule.Name}); err != nil {
		return true
	}

	for _, backup := range backups.Items {
		if backup.Status.Phase == velerov1.BackupPhaseNew ||
			backup.Status.Phase == velerov1.BackupPhaseInProgress {
			return true
		}
	}

	return false
}

func (r *Reconciler) propagateMetaError(ctx context.Context, mgmtBackup *kcmv1alpha1.ManagementBackup, errorMsg string) (ctrl.Result, error) {
	mgmtBackup.Status.Error = "Probably Velero is not installed: " + errorMsg
	if err := r.cl.Status().Update(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status: %w", mgmtBackup.Name, err)
	}

	return ctrl.Result{}, nil // no need to requeue if got such error
}

func getMostRecentProducedBackup(mgmtBackupName string, backups []velerov1.Backup) (*velerov1.Backup, bool) {
	if len(backups) == 0 {
		return &velerov1.Backup{}, false
	}

	now := time.Now().In(time.UTC)

	const timeFormat = "20060102150405"

	var (
		mostRecent time.Time
		minIdx     int
		prefix     = mgmtBackupName + "-"
	)
	for i, backup := range backups {
		if backup.Labels[scheduleMgmtNameLabel] != mgmtBackupName {
			continue // process only backups produced by this schedule
		}

		ts := strings.TrimPrefix(backup.Name, prefix)

		t, err := time.Parse(timeFormat, ts)
		if err != nil {
			continue
		}

		if !t.After(now) && (mostRecent.IsZero() || t.After(mostRecent)) {
			mostRecent = t
			minIdx = i
		}
	}

	return &backups[minIdx], !mostRecent.IsZero()
}

func isRestored(mgmtBackup *kcmv1alpha1.ManagementBackup) bool {
	return mgmtBackup.Labels[velerov1.RestoreNameLabel] != "" && mgmtBackup.Labels[velerov1.BackupNameLabel] != ""
}

func getNextAttemptTime(schedule *kcmv1alpha1.ManagementBackup, cronSchedule cron.Schedule) (bool, time.Time) {
	lastBackupTime := schedule.CreationTimestamp.Time
	if !schedule.Status.LastBackupTime.IsZero() {
		lastBackupTime = schedule.Status.LastBackupTime.Time
	}

	nextAttemptTime := cronSchedule.Next(lastBackupTime) // might be in past so rely on now
	now := time.Now().In(time.UTC)
	isDue := now.After(nextAttemptTime)
	if isDue {
		nextAttemptTime = now
	}

	return isDue, nextAttemptTime
}

func isMetaError(err error) bool {
	return err != nil && (apimeta.IsNoMatchError(err) ||
		apimeta.IsAmbiguousError(err) ||
		apierrors.IsNotFound(err) || // if resource is not found
		errors.Is(err, &discovery.ErrGroupDiscoveryFailed{}))
}
