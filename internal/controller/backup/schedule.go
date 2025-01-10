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
	"fmt"

	cron "github.com/robfig/cron/v3"
	velerov1api "github.com/zerospiel/velero/pkg/apis/velero/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
)

func (c *Config) ReconcileScheduledBackup(ctx context.Context, scheduledBackup *kcmv1alpha1.ManagementBackup, cronRaw string) (ctrl.Result, error) {
	zeroResult := ctrl.Result{}

	if scheduledBackup == nil {
		return zeroResult, nil
	}

	l := ctrl.LoggerFrom(ctx).WithName("schedule-reconciler")

	templateSpec, err := c.getBackupTemplateSpec(ctx)
	if err != nil {
		return zeroResult, fmt.Errorf("failed to construct velero backup template spec: %w", err)
	}

	if scheduledBackup.Status.Reference == nil {
		if scheduledBackup.CreationTimestamp.IsZero() || scheduledBackup.UID == "" {
			l.Info("Creating scheduled ManagementBackup")
			if err := c.cl.Create(ctx, scheduledBackup); err != nil {
				return zeroResult, fmt.Errorf("failed to create scheduled ManagementBackup: %w", err)
			}
		}

		veleroSchedule := &velerov1api.Schedule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      scheduledBackup.Name,
				Namespace: c.systemNamespace,
			},
			Spec: velerov1api.ScheduleSpec{
				Template:                   *templateSpec,
				Schedule:                   cronRaw,
				UseOwnerReferencesInBackup: ref(true),
				SkipImmediately:            ref(false),
			},
		}

		_ = ctrl.SetControllerReference(scheduledBackup, veleroSchedule, c.scheme, controllerutil.WithBlockOwnerDeletion(false))

		createErr := c.cl.Create(ctx, veleroSchedule)
		isAlreadyExistsErr := apierrors.IsAlreadyExists(createErr)
		if createErr != nil && !isAlreadyExistsErr {
			return zeroResult, fmt.Errorf("failed to create velero Schedule: %w", createErr)
		}

		scheduledBackup.Status.Reference = &corev1.ObjectReference{
			APIVersion: velerov1api.SchemeGroupVersion.String(),
			Kind:       "Schedule",
			Namespace:  veleroSchedule.Namespace,
			Name:       veleroSchedule.Name,
		}

		if !isAlreadyExistsErr {
			l.Info("Initial schedule has been created")
			if err := c.cl.Status().Update(ctx, scheduledBackup); err != nil {
				return zeroResult, fmt.Errorf("failed to update scheduled backup status with updated reference: %w", err)
			}
			// velero schedule has been created, nothing yet to update here
			return zeroResult, nil
		}

		// velero schedule is already exists, scheduled-backup has been "restored", update its status
	}

	l.Info("Collecting scheduled backup status")

	veleroSchedule := new(velerov1api.Schedule)
	if err := c.cl.Get(ctx, client.ObjectKey{
		Name:      scheduledBackup.Status.Reference.Name,
		Namespace: scheduledBackup.Status.Reference.Namespace,
	}, veleroSchedule); err != nil {
		return zeroResult, fmt.Errorf("failed to get velero Schedule: %w", err)
	}

	if cronRaw != "" && veleroSchedule.Spec.Schedule != cronRaw {
		l.Info("Velero Schedule has outdated crontab, updating", "current_crontab", veleroSchedule.Spec.Schedule, "expected_crontab", cronRaw)
		originalSchedule := veleroSchedule.DeepCopy()
		veleroSchedule.Spec.Schedule = cronRaw
		if err := c.cl.Patch(ctx, veleroSchedule, client.MergeFrom(originalSchedule)); err != nil {
			return zeroResult, fmt.Errorf("failed to update velero schedule %s with a new crontab '%s': %w", client.ObjectKeyFromObject(veleroSchedule), cronRaw, err)
		}

		return zeroResult, nil
	}

	if !equality.Semantic.DeepEqual(templateSpec.OrLabelSelectors, veleroSchedule.Spec.Template.OrLabelSelectors) {
		l.Info("Velero Schedule has outdated template spec selectors, updating")
		originalSchedule := veleroSchedule.DeepCopy()
		veleroSchedule.Spec.Template = *templateSpec
		if err := c.cl.Patch(ctx, veleroSchedule, client.MergeFrom(originalSchedule)); err != nil {
			return zeroResult, fmt.Errorf("failed to update velero schedule %s with a new template selectors: %w", client.ObjectKeyFromObject(veleroSchedule), err)
		}

		return zeroResult, nil
	}

	// if backup does not exist then it has not been run yet
	veleroBackup := new(velerov1api.Backup)
	if !veleroSchedule.Status.LastBackup.IsZero() {
		l.V(1).Info("Fetching velero Backup to sync its status")
		if err := c.cl.Get(ctx, client.ObjectKey{
			Name:      veleroSchedule.TimestampedName(veleroSchedule.Status.LastBackup.Time),
			Namespace: scheduledBackup.Status.Reference.Namespace,
		}, veleroBackup); client.IgnoreNotFound(err) != nil {
			return zeroResult, fmt.Errorf("failed to get velero Backup: %w", err)
		}
	}

	var nextAttempt *metav1.Time
	if !veleroSchedule.Spec.Paused {
		l.V(1).Info("Parsing crontab schedule", "crontab", cronRaw)
		cronSchedule, err := cron.ParseStandard(cronRaw)
		if err != nil {
			return zeroResult, fmt.Errorf("failed to parse cron schedule %s: %w", cronRaw, err)
		}

		nextAttempt = getNextAttemptTime(veleroSchedule, cronSchedule)
	}

	// decrease API calls, on first .status.reference set the status itself is empty so no need to check it
	{
		if scheduledBackup.Status.NextAttempt.Equal(nextAttempt) &&
			scheduledBackup.Status.SchedulePaused == veleroSchedule.Spec.Paused &&
			equality.Semantic.DeepEqual(scheduledBackup.Status.GetScheduleCopy(), veleroSchedule.Status) &&
			equality.Semantic.DeepEqual(scheduledBackup.Status.GetLastBackupCopy(), veleroBackup.Status) {
			l.V(1).Info("No new changes to show in the scheduled ManagementBackup")
			return ctrl.Result{RequeueAfter: c.objectsRequeueAfter}, nil
		}
	}

	scheduledBackup.Status.Schedule = &veleroSchedule.Status
	scheduledBackup.Status.NextAttempt = nextAttempt
	scheduledBackup.Status.SchedulePaused = veleroSchedule.Spec.Paused
	if !veleroBackup.CreationTimestamp.IsZero() { // exists
		scheduledBackup.Status.LastBackup = &veleroBackup.Status
	}

	l.Info("Updating scheduled backup status")
	if err := c.cl.Status().Update(ctx, scheduledBackup); err != nil {
		return zeroResult, fmt.Errorf("failed to update status of the scheduled ManagementBackup %s: %w", scheduledBackup.Name, err)
	}

	return zeroResult, nil
}

// DisableSchedule sets pause to the referenced velero schedule.
// Do nothing is ManagedBackup is already marked as paused.
func (c *Config) DisableSchedule(ctx context.Context, scheduledBackup *kcmv1alpha1.ManagementBackup) error {
	if scheduledBackup.Status.Reference == nil || scheduledBackup.Status.SchedulePaused { // sanity
		return nil
	}

	veleroSchedule := new(velerov1api.Schedule)
	if err := c.cl.Get(ctx, client.ObjectKey{
		Name:      scheduledBackup.Status.Reference.Name,
		Namespace: scheduledBackup.Status.Reference.Namespace,
	}, veleroSchedule); err != nil {
		return fmt.Errorf("failed to get velero Schedule: %w", err)
	}

	original := veleroSchedule.DeepCopy()

	veleroSchedule.Spec.Paused = true
	if err := c.cl.Patch(ctx, veleroSchedule, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to disable velero schedule: %w", err)
	}

	ctrl.LoggerFrom(ctx).Info("Disabled Velero Schedule")

	return nil
}

func getNextAttemptTime(schedule *velerov1api.Schedule, cronSchedule cron.Schedule) *metav1.Time {
	lastBackupTime := schedule.CreationTimestamp.Time
	if schedule.Status.LastBackup != nil {
		lastBackupTime = schedule.Status.LastBackup.Time
	}

	if schedule.Status.LastSkipped != nil && schedule.Status.LastSkipped.After(lastBackupTime) {
		lastBackupTime = schedule.Status.LastSkipped.Time
	}

	return &metav1.Time{Time: cronSchedule.Next(lastBackupTime)}
}

func ref[T any](v T) *T { return &v }
