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

package controller

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	hmcv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/controller/backup"
)

// BackupReconciler reconciles a Backup object
type BackupReconciler struct {
	client.Client

	kc *rest.Config

	image           string
	systemNamespace string
	features        []string

	requeueAfter time.Duration
}

func (r *BackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	backupInstance := new(hmcv1alpha1.Backup)
	err := r.Client.Get(ctx, req.NamespacedName, backupInstance)
	if ierr := client.IgnoreNotFound(err); ierr != nil {
		l.Error(ierr, "unable to fetch Backup")
		return ctrl.Result{}, ierr
	}

	bcfg := backup.NewConfig(r.Client, r.kc,
		backup.WithFeatures(r.features...),
		backup.WithRequeueAfter(r.requeueAfter),
		backup.WithVeleroImage(r.image),
		backup.WithVeleroSystemNamespace(r.systemNamespace),
	)

	if apierrors.IsNotFound(err) {
		// if non-scheduled backup is not found(deleted), then just skip the error
		// if scheduled backup is not found, then it either does not exist yet
		// and we should create it, or it has been removed;
		// if the latter is the case, we either should re-create it once again
		// or do nothing if mgmt backup is disabled
		mgmt := new(hmcv1alpha1.Management)
		if err := r.Client.Get(ctx, req.NamespacedName, mgmt); err != nil {
			l.Error(err, "unable to fetch Management")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}

		if !mgmt.Spec.Backup.Enabled {
			l.Info("Management backup is disabled, nothing to do")
			return ctrl.Result{}, nil
		}

		l.Info("Reconciling velero stack")
		installRes, err := bcfg.ReconcileVeleroInstallation(ctx)
		if err != nil {
			l.Error(err, "velero installation")
			return ctrl.Result{}, err
		}
		if installRes.Requeue || installRes.RequeueAfter > 0 {
			return installRes, nil
		}

		// required during creation
		backupInstance.Name = req.Name
		backupInstance.Namespace = req.Namespace
	}

	btype, err := bcfg.GetBackupType(ctx, backupInstance, req.Name)
	if err != nil {
		l.Error(err, "failed to determine backup type")
		return ctrl.Result{}, err
	}

	switch btype {
	case backup.TypeNone:
		l.Info("There are nothing to reconcile, management does not exists")
		// TODO: do we need to reconcile/delete/pause schedules in this case?
		return ctrl.Result{}, nil
	case backup.TypeBackup:
		return ctrl.Result{}, bcfg.ReconcileBackup(ctx, backupInstance)
	case backup.TypeSchedule:
		return ctrl.Result{}, bcfg.ReconcileScheduledBackup(ctx, backupInstance)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.kc = mgr.GetConfig()

	const reqDuration = "BACKUP_CTRL_REQUEUE_DURATION"
	r.features = strings.Split(strings.ReplaceAll(os.Getenv("BACKUP_FEATURES"), ", ", ","), ",")
	r.systemNamespace = os.Getenv("BACKUP_SYSTEM_NAMESPACE")
	r.image = os.Getenv("BACKUP_BASIC_IMAGE")
	d, err := time.ParseDuration(os.Getenv(reqDuration))
	if err != nil {
		return fmt.Errorf("failed to parse env %s duration: %w", reqDuration, err)
	}
	r.requeueAfter = d

	return ctrl.NewControllerManagedBy(mgr).
		For(&hmcv1alpha1.Backup{}).
		Watches(&hmcv1alpha1.Management{}, handler.EnqueueRequestsFromMapFunc(func(_ context.Context, o client.Object) []ctrl.Request {
			return []ctrl.Request{{NamespacedName: client.ObjectKeyFromObject(o)}}
		}), builder.WithPredicates( // watch mgmt.spec.backup to manage the (only) scheduled Backup
			predicate.Funcs{
				GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
				DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
				CreateFunc: func(tce event.TypedCreateEvent[client.Object]) bool {
					mgmt, ok := tce.Object.(*hmcv1alpha1.Management)
					if !ok {
						return false
					}

					return mgmt.Spec.Backup.Enabled
				},
				UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
					oldMgmt, ok := tue.ObjectOld.(*hmcv1alpha1.Management)
					if !ok {
						return false
					}

					newMgmt, ok := tue.ObjectNew.(*hmcv1alpha1.Management)
					if !ok {
						return false
					}

					return (newMgmt.Spec.Backup.Enabled != oldMgmt.Spec.Backup.Enabled ||
						newMgmt.Spec.Backup.Schedule != oldMgmt.Spec.Backup.Schedule)
				},
			},
		)).
		Complete(r)
}
