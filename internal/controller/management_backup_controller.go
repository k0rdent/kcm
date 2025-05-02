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
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/controller/backup"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
)

// ManagementBackupReconciler reconciles a ManagementBackup object
type ManagementBackupReconciler struct {
	client.Client

	internal *backup.Reconciler

	SystemNamespace string
}

func (r *ManagementBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	management := new(kcmv1.Management)
	if err := r.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, management); err != nil {
		l.Error(err, "unable to fetch Management")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping ManagementBackup reconciliation")
		return ctrl.Result{}, nil
	}

	mgmtBackup := new(kcmv1.ManagementBackup)
	if err := r.Get(ctx, req.NamespacedName, mgmtBackup); err != nil {
		l.Error(err, "unable to fetch ManagementBackup")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	res, err := r.internal.ReconcileBackup(ctx, mgmtBackup)
	if err != nil {
		l.Error(err, "failed to reconcile managementbackups")
	}
	return res, err
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

	r.internal = backup.NewReconciler(r.Client, r.SystemNamespace)

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		Named("mgmtbackup_controller").
		For(&kcmv1.ManagementBackup{}).
		WatchesRawSource(source.Channel(runner.GetEventChannel(), &handler.EnqueueRequestForObject{})).
		Complete(r)
}
