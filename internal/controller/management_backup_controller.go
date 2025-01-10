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
	"os"
	"slices"
	"time"

	velerov1api "github.com/zerospiel/velero/pkg/apis/velero/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	hmcv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/controller/backup"
)

// ManagementBackupReconciler reconciles a ManagementBackup object
type ManagementBackupReconciler struct {
	client.Client

	config *backup.Config
}

func (r *ManagementBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	backupInstance := new(hmcv1alpha1.ManagementBackup)
	err := r.Client.Get(ctx, req.NamespacedName, backupInstance)
	if ierr := client.IgnoreNotFound(err); ierr != nil {
		l.Error(ierr, "unable to fetch ManagementBackup")
		return ctrl.Result{}, ierr
	}

	instanceIsNotFound := apierrors.IsNotFound(err)

	mgmt, err := r.config.GetManagement(ctx)
	if err != nil && !errors.Is(err, backup.ErrNoManagementExists) { // error during list
		return ctrl.Result{}, err
	}

	btype := backup.GetType(backupInstance)
	if errors.Is(err, backup.ErrNoManagementExists) {
		// no mgmt, if backup is not found then nothing to do
		if instanceIsNotFound {
			l.Info("No Management object exists, ManagementBackup object has not been found, nothing to do")
			return ctrl.Result{}, nil
		}

		// backup exists, disable if schedule and active, otherwise proceed with reconciliation (status updates)
		if btype == backup.TypeSchedule {
			if err := r.config.DisableSchedule(ctx, backupInstance); err != nil {
				l.Error(err, "failed to disable scheduled ManagementBackup")
				return ctrl.Result{}, err
			}
		}
	}

	requestEqualsMgmt := mgmt != nil && req.Name == mgmt.Name && req.Namespace == mgmt.Namespace
	if instanceIsNotFound { // mgmt exists
		if !requestEqualsMgmt { // oneshot backup
			l.Info("ManagementBackup object has not been found, nothing to do")
			return ctrl.Result{}, nil
		}

		btype = backup.TypeSchedule

		// required during creation
		backupInstance.Name = req.Name
		backupInstance.Namespace = req.Namespace
	}

	// if requestEqualsMgmt {
	// 	l.Info("Reconciling velero stack parts")
	// 	installRes, err := r.config.ReconcileVeleroInstallation(ctx, mgmt)
	// 	if err != nil {
	// 		l.Error(err, "velero stack installation")
	// 		return ctrl.Result{}, err
	// 	}

	// 	if !installRes.IsZero() {
	// 		return installRes, nil
	// 	}
	// }

	if btype == backup.TypeNone {
		if requestEqualsMgmt {
			btype = backup.TypeSchedule
		} else {
			btype = backup.TypeBackup
		}
	}

	switch btype {
	case backup.TypeBackup:
		return r.config.ReconcileBackup(ctx, backupInstance)
	case backup.TypeSchedule:
		return r.config.ReconcileScheduledBackup(ctx, backupInstance, mgmt.GetBackupSchedule())
	case backup.TypeNone:
		fallthrough
	default:
		return ctrl.Result{}, nil
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagementBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var err error
	r.config, err = parseEnvsToConfig(r.Client, mgr)
	if err != nil {
		return fmt.Errorf("failed to parse envs: %w", err)
	}

	// NOTE: without installed CRDs it is impossible to initialize informers
	// and the uncached client is required because it this point the manager
	// still has not started the cache yet
	// uncachedCl, err := client.New(mgr.GetConfig(), client.Options{Cache: nil})
	// if err != nil {
	// 	return fmt.Errorf("failed to create uncached client: %w", err)
	// }

	// if err := r.config.InstallVeleroCRDs(uncachedCl); err != nil {
	// 	return fmt.Errorf("failed to install velero CRDs: %w", err)
	// }

	getManagementNameIfEnabled := func(ctx context.Context) ctrl.Request {
		mgmt, err := r.config.GetManagement(ctx)
		if err != nil || !mgmt.Spec.Backup.Enabled {
			return ctrl.Request{}
		}

		return ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mgmt)}
	}

	enqueueIfManagementEnabled := func(req ctrl.Request) []ctrl.Request {
		if req.Name == "" && req.Namespace == "" {
			return nil
		}
		return []ctrl.Request{req}
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&hmcv1alpha1.ManagementBackup{}).
		Owns(&velerov1api.Backup{},
			builder.WithPredicates(
				predicate.Funcs{
					GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
					DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
				},
			),
			builder.MatchEveryOwner,
		).
		Owns(&velerov1api.Schedule{}, builder.WithPredicates(
			predicate.Funcs{
				GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
				DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
			},
		)).
		// Watches(&velerov1api.BackupStorageLocation{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []ctrl.Request {
		// 	return enqueueIfManagementEnabled(getManagementNameIfEnabled(ctx))
		// }), builder.WithPredicates(
		// 	predicate.Funcs{
		// 		GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
		// 		DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
		// 		CreateFunc:  func(event.TypedCreateEvent[client.Object]) bool { return true },
		// 		UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
		// 			oldBSL, ok := tue.ObjectOld.(*velerov1api.BackupStorageLocation)
		// 			if !ok {
		// 				return false
		// 			}

		// 			newBSL, ok := tue.ObjectNew.(*velerov1api.BackupStorageLocation)
		// 			if !ok {
		// 				return false
		// 			}

		// 			return newBSL.Spec.Provider != oldBSL.Spec.Provider
		// 		},
		// 	},
		// )).
		Watches(&hmcv1alpha1.Management{}, handler.Funcs{
			GenericFunc: nil,
			DeleteFunc: func(_ context.Context, tde event.TypedDeleteEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
				q.Add(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tde.Object)}) // disable schedule on mgmt absence
			},
			CreateFunc: func(_ context.Context, tce event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
				mgmt, ok := tce.Object.(*hmcv1alpha1.Management)
				if !ok || !mgmt.Spec.Backup.Enabled {
					return
				}

				q.Add(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tce.Object)})
			},
			UpdateFunc: func(_ context.Context, tue event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
				oldMgmt, ok := tue.ObjectOld.(*hmcv1alpha1.Management)
				if !ok {
					return
				}

				newMgmt, ok := tue.ObjectNew.(*hmcv1alpha1.Management)
				if !ok {
					return
				}

				if newMgmt.Spec.Backup.Enabled == oldMgmt.Spec.Backup.Enabled &&
					newMgmt.Spec.Backup.Schedule == oldMgmt.Spec.Backup.Schedule {
					return
				}

				q.Add(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tue.ObjectNew)})
			},
		}).
		// Watches(&appsv1.Deployment{}, handler.Funcs{
		// 	GenericFunc: nil,
		// 	DeleteFunc:  nil,
		// 	CreateFunc:  nil,
		// 	UpdateFunc: func(ctx context.Context, tue event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
		// 		if tue.ObjectNew.GetNamespace() != r.config.GetVeleroSystemNamespace() || tue.ObjectNew.GetName() != backup.VeleroName {
		// 			return
		// 		}

		// 		q.Add(getManagementNameIfEnabled(ctx))
		// 	},
		// }).
		Watches(&hmcv1alpha1.ClusterDeployment{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []ctrl.Request {
			return enqueueIfManagementEnabled(getManagementNameIfEnabled(ctx))
		}), builder.WithPredicates(
			predicate.Funcs{
				GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
				CreateFunc:  func(event.TypedCreateEvent[client.Object]) bool { return true },
				DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return true },
				UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
					oldObj, ok := tue.ObjectOld.(*hmcv1alpha1.ClusterDeployment)
					if !ok {
						return false
					}

					newObj, ok := tue.ObjectNew.(*hmcv1alpha1.ClusterDeployment)
					if !ok {
						return false
					}

					return newObj.Spec.Template != oldObj.Spec.Template
				},
			},
		)).
		Watches(&hmcv1alpha1.ClusterTemplate{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []ctrl.Request {
			return enqueueIfManagementEnabled(getManagementNameIfEnabled(ctx))
		}), builder.WithPredicates(
			predicate.Funcs{
				GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
				CreateFunc:  func(event.TypedCreateEvent[client.Object]) bool { return true },
				DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return true },
				UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
					oldObj, ok := tue.ObjectOld.(*hmcv1alpha1.ClusterTemplate)
					if !ok {
						return false
					}

					newObj, ok := tue.ObjectNew.(*hmcv1alpha1.ClusterTemplate)
					if !ok {
						return false
					}

					return !slices.Equal(oldObj.Status.Providers, newObj.Status.Providers)
				},
			},
		)).
		Complete(r)
}

func parseEnvsToConfig(cl client.Client, mgr interface {
	GetScheme() *runtime.Scheme
	GetConfig() *rest.Config
},
) (*backup.Config, error) {
	const (
		// installationReqDurationEnv = "BACKUP_CTRL_INSTALL_READINESS_REQUEUE_DURATION"
		reqDurationEnv = "BACKUP_CTRL_REQUEUE_DURATION"
	)
	// installationRequeueAfter, err := time.ParseDuration(os.Getenv(installationReqDurationEnv))
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to parse env %s duration: %w", installationReqDurationEnv, err)
	// }

	objectsRequeueAfter, err := time.ParseDuration(os.Getenv(reqDurationEnv))
	if err != nil {
		return nil, fmt.Errorf("failed to parse env %s duration: %w", reqDurationEnv, err)
	}

	return backup.NewConfig(cl, mgr.GetConfig(), mgr.GetScheme(),
		// backup.WithFeatures(strings.Split(strings.ReplaceAll(os.Getenv("BACKUP_FEATURES"), ", ", ","), ",")...),
		// backup.WithInstallationRequeueAfter(installationRequeueAfter),
		backup.WithObjectsRequeueAfter(objectsRequeueAfter),
		// backup.WithVeleroImage(os.Getenv("BACKUP_BASIC_IMAGE")),
		// backup.WithVeleroSystemNamespace(os.Getenv("BACKUP_SYSTEM_NAMESPACE")),
		// backup.WithPluginImages(strings.Split(strings.ReplaceAll(os.Getenv("BACKUP_PLUGIN_IMAGES"), ", ", ","), ",")...),
	), nil
}
