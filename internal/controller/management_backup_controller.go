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
	"errors"
	"fmt"
	"time"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kcmv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/controller/backup"
)

// ManagementBackupReconciler reconciles a ManagementBackup object
type ManagementBackupReconciler struct {
	client.Client

	internal *backup.Reconciler

	SystemNamespace string
}

func (r *ManagementBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	mgmtBackup := new(kcmv1alpha1.ManagementBackup)
	if err := r.Client.Get(ctx, req.NamespacedName, mgmtBackup); client.IgnoreNotFound(err) != nil {
		l.Error(err, "unable to fetch ManagementBackup")
		return ctrl.Result{}, err
	}

	mgmt, err := r.internal.GetManagement(ctx)
	if err != nil && !errors.Is(err, backup.ErrNoManagementExists) { // error during list
		return ctrl.Result{}, err
	}

	// TODO: NOTE: should we allow to continue with onetime backups if backup feature is disabled?
	if errors.Is(err, backup.ErrNoManagementExists) || !mgmt.Spec.Backup.Enabled {
		if mgmtBackup.IsSchedule() && !mgmtBackup.Status.Paused {
			mgmtBackup.Status.Paused = true
			if err := r.Client.Status().Update(ctx, mgmtBackup); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to mark ManagementBackup status as paused: %w", err)
			}
		}

		l.V(1).Info("No Management object exists or backup is disabled, nothing to do")
		return ctrl.Result{}, nil
	}

	if mgmtBackup.CreationTimestamp.IsZero() || mgmtBackup.UID == "" { // mgmt exists at this point
		requestEqualsMgmt := mgmt != nil && req.Name == mgmt.Name
		if !requestEqualsMgmt { // single backup
			l.V(1).Info("ManagementBackup object has not been found, nothing to do")
			return ctrl.Result{}, nil
		}

		// required during creation
		mgmtBackup.Name = req.Name
	}

	return r.internal.ReconcileBackup(ctx, mgmtBackup, mgmt)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagementBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	const scheduleSyncTime = 1 * time.Minute
	runner := backup.NewRunner(
		backup.WithClient(mgr.GetClient()),
		backup.WithInterval(scheduleSyncTime),
	)
	if err := mgr.Add(runner); err != nil {
		return fmt.Errorf("unable to add periodic runner: %w", err)
	}

	r.internal = backup.NewReconciler(r.Client, mgr.GetScheme(), r.SystemNamespace)

	return ctrl.NewControllerManagedBy(mgr).
		For(&kcmv1alpha1.ManagementBackup{}).
		Named("mgmtbackup_controller").
		WatchesRawSource(source.Channel(runner.GetEventChannel(), &handler.EnqueueRequestForObject{})).
		Watches(&velerov1.Backup{}, handler.Funcs{
			GenericFunc: nil,
			DeleteFunc:  nil,
			CreateFunc:  nil,
			UpdateFunc: func(_ context.Context, tue event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
				oldO, ok := tue.ObjectOld.(*velerov1.Backup)
				if !ok {
					return
				}

				newO, ok := tue.ObjectNew.(*velerov1.Backup)
				if !ok {
					return
				}

				if oldO.Status.Phase == "" || newO.Status.Phase == oldO.Status.Phase {
					return
				}

				name := newO.Labels[backup.ScheduleMgmtNameLabel]
				if name == "" {
					name = newO.Name
				}

				q.Add(ctrl.Request{NamespacedName: client.ObjectKey{Name: name}})
			},
		}).
		Watches(&kcmv1alpha1.Management{}, handler.Funcs{
			GenericFunc: nil,
			DeleteFunc: func(_ context.Context, tde event.TypedDeleteEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
				q.Add(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tde.Object)}) // disable schedule on mgmt absence
			},
			CreateFunc: func(_ context.Context, tce event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
				mgmt, ok := tce.Object.(*kcmv1alpha1.Management)
				if !ok || !mgmt.Spec.Backup.Enabled {
					return
				}

				q.Add(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tce.Object)})
			},
			UpdateFunc: func(_ context.Context, tue event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
				oldMgmt, ok := tue.ObjectOld.(*kcmv1alpha1.Management)
				if !ok {
					return
				}

				newMgmt, ok := tue.ObjectNew.(*kcmv1alpha1.Management)
				if !ok {
					return
				}

				if newMgmt.Spec.Backup.Enabled == oldMgmt.Spec.Backup.Enabled &&
					newMgmt.Spec.Backup.Schedule == oldMgmt.Spec.Backup.Schedule {
					return
				}

				q.Add(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(newMgmt)})
			},
		}).
		Complete(r)
}
