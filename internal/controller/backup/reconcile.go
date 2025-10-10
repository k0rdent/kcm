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
	"slices"
	"strings"
	"time"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	labelsutil "github.com/K0rdent/kcm/internal/util/labels"
)

// scheduleMgmtNameLabel holds a reference to the [github.com/K0rdent/kcm/api/v1beta1.ManagementBackup] object name.
const scheduleMgmtNameLabel = "k0rdent.mirantis.com/management-backup"

// ReconcileBackup is the main reconciliation function for ManagementBackup resources.
// It handles different reconciliation paths based on the state of the backup:
// - For restored backups, it updates status after restoration
// - For scheduled backups, it handles scheduled execution
// - For one-time backups that haven't been initiated, it creates them
// - Otherwise, it updates status for existing backups
func (r *Reconciler) ReconcileBackup(ctx context.Context, mgmtBackup *kcmv1.ManagementBackup) (ctrl.Result, error) {
	if mgmtBackup == nil {
		return ctrl.Result{}, nil
	}

	s, err := getScope(ctx, r.mgmtCl, r.systemNamespace, r.regionalFactory)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to construct backup scope: %w", err)
	}
	s.mgmtBackup = mgmtBackup

	if updated, err := labelsutil.AddKCMComponentLabel(ctx, r.mgmtCl, mgmtBackup); updated || err != nil {
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add component label: %w", err)
		}
		return ctrl.Result{}, nil
	}

	if isRestored(mgmtBackup) {
		return r.updateAfterRestoration(ctx, s)
	}

	if mgmtBackup.IsSchedule() { // schedule-creation path
		return r.handleScheduledBackup(ctx, s)
	} else if !allBackupsInitiated(mgmtBackup) && !isRestored(mgmtBackup) { // single backup mode, not yet created
		return r.createAllSingleBackups(ctx, s)
	}

	// collect status for all existing backups
	return r.updateAllBackupsStatus(ctx, s)
}

// allBackupsInitiated checks if backup creation has been initiated for both
// the management cluster and all regions. Returns true only if ALL backups
// have been initiated, false if any backup is missing.
func allBackupsInitiated(mgmtBackup *kcmv1.ManagementBackup) bool {
	// Check management backup
	if mgmtBackup.Status.LastBackupName == "" {
		return false
	}

	// Check all regional backups are initiated
	for _, regionalBackup := range mgmtBackup.Status.RegionsLastBackups {
		if regionalBackup.LastBackupName == "" {
			return false
		}
	}

	return true
}

// updateAllBackupsStatus updates the status of all existing backups (management and regional)
// setting exclusively the LastBackup field.
// It retrieves the current status of each backup from the Velero API and updates
// the ManagementBackup status accordingly.
func (r *Reconciler) updateAllBackupsStatus(ctx context.Context, s *scope) (ctrl.Result, error) {
	mgmtBackup := s.mgmtBackup
	l := ctrl.LoggerFrom(ctx)
	l.V(1).Info("Collecting backup status for all backups")

	updateStatus := false

	if mgmtBackup.Status.LastBackupName != "" {
		backupName := mgmtBackup.Status.LastBackupName
		l.V(1).Info("Found management backup to update status from", "backup", backupName)

		veleroBackup := new(velerov1.Backup)
		if err := r.mgmtCl.Get(ctx, client.ObjectKey{
			Name:      backupName,
			Namespace: r.systemNamespace,
		}, veleroBackup); err != nil {
			l.Error(err, "Failed to get velero Backup", "backup", backupName)
			return ctrl.Result{}, fmt.Errorf("failed to get velero Backup %s: %w", backupName, err)
		}

		if mgmtBackup.Status.LastBackup == nil || !backupStatusEqual(mgmtBackup.Status.LastBackup, &veleroBackup.Status) {
			mgmtBackup.Status.LastBackup = &veleroBackup.Status
			updateStatus = true
		}
	}

	for region, loadedCl := range s.regionClients {
		if region == "" {
			continue
		}

		regionBackupStatusIdx := slices.IndexFunc(mgmtBackup.Status.RegionsLastBackups, func(rb kcmv1.ManagementBackupSingleStatus) bool {
			return rb.Region == region
		})

		var backupName string
		if regionBackupStatusIdx > -1 {
			backupName = mgmtBackup.Status.RegionsLastBackups[regionBackupStatusIdx].LastBackupName
		}

		if backupName == "" { // implies region not found
			continue // no backup to check for this region
		}

		l.V(1).Info("Found regional backup to update status from", "backup", backupName, "region", region)

		// there might be no Region objects yet thus we can't retrieve the regional client
		// but because we utilize a single BSL across clusters (HACK), all velero Backup objects
		// should also exist on the mgmt cluster
		crClient := r.mgmtCl
		if loadedCl.loaded {
			crClient = loadedCl.cl
		}

		veleroBackup := new(velerov1.Backup)
		if err := crClient.Get(ctx, client.ObjectKey{
			Name:      backupName,
			Namespace: r.systemNamespace,
		}, veleroBackup); err != nil {
			l.Error(err, "Failed to get velero Backup", "backup", backupName, "region", region)
			return ctrl.Result{}, fmt.Errorf("failed to get velero Backup %s in region %s: %w", backupName, region, err)
		}

		if mgmtBackup.Status.RegionsLastBackups[regionBackupStatusIdx].LastBackup == nil ||
			!backupStatusEqual(mgmtBackup.Status.RegionsLastBackups[regionBackupStatusIdx].LastBackup, &veleroBackup.Status) {
			mgmtBackup.Status.RegionsLastBackups[regionBackupStatusIdx].LastBackup = &veleroBackup.Status
			updateStatus = true
		}
	}

	if updateStatus {
		if err := r.mgmtCl.Status().Update(ctx, mgmtBackup); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status: %w", mgmtBackup.Name, err)
		}
	}

	return ctrl.Result{}, nil
}

// backupStatusEqual compares two Velero backup statuses for equality.
// Returns true if both statuses have matching phase, timestamps, errors and warnings.
func backupStatusEqual(a, b *velerov1.BackupStatus) bool {
	if a == nil || b == nil {
		return a == b
	}

	return a.Phase == b.Phase &&
		a.StartTimestamp.Equal(b.StartTimestamp) &&
		a.CompletionTimestamp.Equal(b.CompletionTimestamp) &&
		a.Errors == b.Errors &&
		a.Warnings == b.Warnings
}

// updateAfterRestoration updates ManagementBackup resource after it has been restored.
// It removes Velero restoration labels and updates backup statuses from existing backups.
func (r *Reconciler) updateAfterRestoration(ctx context.Context, s *scope) (ctrl.Result, error) {
	mgmtBackup := s.mgmtBackup

	patchHelper, err := patch.NewHelper(mgmtBackup, r.mgmtCl)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create patch helper: %w", err)
	}

	ldebug := ctrl.LoggerFrom(ctx).V(1)

	// initialize status fields if needed
	if mgmtBackup.Status.RegionsLastBackups == nil {
		mgmtBackup.Status.RegionsLastBackups = []kcmv1.ManagementBackupSingleStatus{}
	}

	ldebug.Info("Updating management backup after restoration")

	veleroBackups := new(velerov1.BackupList)
	if err := r.mgmtCl.List(ctx, veleroBackups, client.InNamespace(r.systemNamespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list velero Backups in management cluster: %w", err)
	}

	if mgmtBackup.IsSchedule() {
		lastBackup, ok := getMostRecentlyProducedBackup(mgmtBackup.Name, veleroBackups.Items, "")
		if ok {
			ldebug.Info("Found last management backup", "last_backup_name", lastBackup.Name)

			mgmtBackup.Status.LastBackup = &lastBackup.Status
			mgmtBackup.Status.LastBackupName = lastBackup.Name
			mgmtBackup.Status.LastBackupTime = lastBackup.Status.StartTimestamp
		} else {
			ldebug.Info("No last management backup found")
		}
	} else {
		backupName := mgmtBackup.Name
		for _, v := range veleroBackups.Items {
			if backupName == v.Name {
				mgmtBackup.Status.LastBackup = &v.Status
				mgmtBackup.Status.LastBackupName = v.Name
				mgmtBackup.Status.LastBackupTime = v.Status.StartTimestamp
				break
			}
		}
	}

	for region, loadedCl := range s.regionClients {
		if region == "" {
			ldebug.Info("Skipping restoration path for empty region")
			continue
		}

		ldebug.Info("Updating region related backups after restoration", "region", region)

		// there might be no Region objects yet thus we can't retrieve the regional client
		// but because we utilize a single BSL across clusters (HACK), all velero Backup objects
		// should also exist on the mgmt cluster
		if loadedCl.loaded {
			// list in the according cluster only if region client exist,
			// otherwise reuse the ones fetched from the mgmt cluster
			veleroBackups = new(velerov1.BackupList)
			if err := loadedCl.cl.List(ctx, veleroBackups, client.InNamespace(r.systemNamespace)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to list velero Backups in region %s: %w", region, err)
			}
		}

		regionBackupStatusIdx := slices.IndexFunc(mgmtBackup.Status.RegionsLastBackups, func(rb kcmv1.ManagementBackupSingleStatus) bool {
			return rb.Region == region
		})

		var srcBackup *velerov1.Backup
		if mgmtBackup.IsSchedule() {
			lastRegionalBackup, ok := getMostRecentlyProducedBackup(mgmtBackup.Name, veleroBackups.Items, region)
			if ok {
				ldebug.Info("Found last regional backup", "last_backup_name", lastRegionalBackup.Name, "region", region)
				srcBackup = lastRegionalBackup
			} else {
				ldebug.Info("No last regional backup found", "region", region)
			}
		} else {
			backupName := mgmtBackup.Name + "-" + region
			for _, v := range veleroBackups.Items {
				if backupName != v.Name {
					continue
				}
				srcBackup = &v
				break
			}
		}

		if srcBackup == nil {
			continue
		}

		if regionBackupStatusIdx > -1 {
			mgmtBackup.Status.RegionsLastBackups[regionBackupStatusIdx].LastBackup = &srcBackup.Status
			mgmtBackup.Status.RegionsLastBackups[regionBackupStatusIdx].LastBackupName = srcBackup.Name
			mgmtBackup.Status.RegionsLastBackups[regionBackupStatusIdx].LastBackupTime = srcBackup.Status.StartTimestamp
		} else {
			mgmtBackup.Status.RegionsLastBackups = append(mgmtBackup.Status.RegionsLastBackups,
				kcmv1.ManagementBackupSingleStatus{
					Region:         region,
					LastBackup:     &srcBackup.Status,
					LastBackupName: srcBackup.Name,
					LastBackupTime: srcBackup.Status.StartTimestamp,
				})
		}
	}

	if mgmtBackup.Labels != nil {
		ldebug.Info("Removing velero labels after restoration")
		delete(mgmtBackup.Labels, velerov1.BackupNameLabel)
		delete(mgmtBackup.Labels, velerov1.RestoreNameLabel)
	}
	if err := patchHelper.Patch(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch ManagementBackup labels and status after restoration: %w", err)
	}

	return ctrl.Result{}, nil
}

// createNewVeleroBackup creates a Velero backup in the specified cluster client with given options.
// It creates a new backup with template spec and applies all provided createOpt functions.
func (r *Reconciler) createNewVeleroBackup(ctx context.Context, cl client.Client, region string, s *scope, backupName string, createOpts ...createOpt) error {
	veleroBackup := r.getNewVeleroBackup(backupName, s, region)

	for _, o := range createOpts {
		o(veleroBackup)
	}

	if err := cl.Create(ctx, veleroBackup); client.IgnoreAlreadyExists(err) != nil { // avoid err-loop on status update error
		return fmt.Errorf("failed to create velero Backup: %w", err)
	}

	return nil
}

// getNewVeleroBackup creates a new Velero backup object with basic fields populated.
// It uses the backup template spec from getBackupTemplateSpec.
func (r *Reconciler) getNewVeleroBackup(backupName string, s *scope, region string) *velerov1.Backup {
	return &velerov1.Backup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: velerov1.SchemeGroupVersion.String(),
			Kind:       "Backup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupName,
			Namespace: r.systemNamespace,
		},
		Spec: *getBackupTemplateSpec(s, region),
	}
}

// propagateMetaError handles metadata-related errors by updating the ManagementBackup status
// with an error message. It indicates if Velero is likely not installed.
func (r *Reconciler) propagateMetaError(ctx context.Context, region string, mgmtBackup *kcmv1.ManagementBackup, errorMsg string) (ctrl.Result, error) {
	setMetaError(region, mgmtBackup, errorMsg)

	if err := r.mgmtCl.Status().Update(ctx, mgmtBackup); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ManagementBackup %s status: %w", mgmtBackup.Name, err)
	}

	return ctrl.Result{}, nil // no need to requeue if got such error
}

func setMetaError(region string, mgmtBackup *kcmv1.ManagementBackup, errorMsg string) {
	setError(region, mgmtBackup, "Probably Velero is not installed: "+errorMsg)
}

func setError(region string, mgmtBackup *kcmv1.ManagementBackup, errorMsg string) {
	if region == "" {
		mgmtBackup.Status.Error = errorMsg
	} else {
		found := false
		for i, lb := range mgmtBackup.Status.RegionsLastBackups {
			if lb.Region == region {
				mgmtBackup.Status.RegionsLastBackups[i].Error = errorMsg
				found = true
				break
			}
		}

		if !found {
			mgmtBackup.Status.RegionsLastBackups = append(mgmtBackup.Status.RegionsLastBackups, kcmv1.ManagementBackupSingleStatus{
				Error:  errorMsg,
				Region: region,
			})
		}
	}
}

// getMostRecentlyProducedBackup finds the most recent backup for a given ManagementBackup
// and region from a list of backups. It parses backup names to extract timestamps.
func getMostRecentlyProducedBackup(mgmtBackupName string, backups []velerov1.Backup, region string) (*velerov1.Backup, bool) {
	if len(backups) == 0 {
		return &velerov1.Backup{}, false
	}

	now := time.Now().UTC()

	const timeFormat = "20060102150405"

	var (
		mostRecent time.Time
		minIdx     int
		prefix     = mgmtBackupName + "-"
	)
	if region != "" {
		prefix = mgmtBackupName + "-" + region + "-"
	}

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

// isRestored checks if a ManagementBackup resource has been restored
// by checking for Velero restoration labels.
func isRestored(mgmtBackup *kcmv1.ManagementBackup) bool {
	return mgmtBackup.Labels[velerov1.RestoreNameLabel] != "" && mgmtBackup.Labels[velerov1.BackupNameLabel] != ""
}

// isMetaError checks if an error is related to metadata or discovery.
// This includes no match errors, ambiguous errors, not found errors,
// and group discovery failures.
func isMetaError(err error) bool {
	return err != nil && (apimeta.IsNoMatchError(err) ||
		apimeta.IsAmbiguousError(err) ||
		apierrors.IsNotFound(err) || // if resource is not found
		errors.Is(err, &discovery.ErrGroupDiscoveryFailed{}))
}
