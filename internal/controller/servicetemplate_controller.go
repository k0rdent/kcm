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
	"slices"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1beta2 "github.com/fluxcd/source-controller/api/v1beta2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcm "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
)

const sourceNotReadyMessage = "Source is not ready"

// ServiceTemplateReconciler reconciles a ServiceTemplate object
type ServiceTemplateReconciler struct {
	TemplateReconciler
}

func (r *ServiceTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ServiceTemplate")

	serviceTemplate := new(kcm.ServiceTemplate)
	if err = r.Get(ctx, req.NamespacedName, serviceTemplate); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ServiceTemplate not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		l.Error(err, "Failed to get ServiceTemplate")
		return ctrl.Result{}, err
	}

	management, err := r.getManagement(ctx, serviceTemplate)
	if err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("Management is not created yet, retrying")
			return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
		}
		return ctrl.Result{}, err
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping ServiceTemplate reconciliation")
		return ctrl.Result{}, nil
	}

	if updated, err := utils.AddKCMComponentLabel(ctx, r.Client, serviceTemplate); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{Requeue: true}, err // generation has not changed, need explicit requeue
	}

	defer func() {
		if updErr := r.Status().Update(ctx, serviceTemplate); updErr != nil {
			err = errors.Join(err, updErr)
		}
		l.Info("Reconciliation complete")
	}()

	switch {
	case serviceTemplate.HelmChartSpec() != nil, serviceTemplate.HelmChartRef() != nil:
		l.V(1).Info("reconciling helm chart")
		return r.ReconcileTemplate(ctx, serviceTemplate)
	case serviceTemplate.LocalSourceRef() != nil:
		l.V(1).Info("reconciling local source")
		return ctrl.Result{}, r.reconcileLocalSource(ctx, serviceTemplate)
	case serviceTemplate.RemoteSourceSpec() != nil:
		l.V(1).Info("reconciling remote source")
		return ctrl.Result{}, r.reconcileRemoteSource(ctx, serviceTemplate)
	default:
		return ctrl.Result{}, errors.New("invalid ServiceTemplate")
	}
}

// reconcileLocalSource reconciles local source defined in ServiceTemplate
func (r *ServiceTemplateReconciler) reconcileLocalSource(ctx context.Context, template *kcm.ServiceTemplate) (err error) {
	ref := template.LocalSourceRef()
	if ref == nil {
		return errors.New("local source ref is undefined")
	}

	status := kcm.ServiceTemplateStatus{
		TemplateStatusCommon: kcm.TemplateStatusCommon{
			TemplateValidationStatus: kcm.TemplateValidationStatus{},
			ObservedGeneration:       template.Generation,
		},
	}

	defer func() {
		if err != nil {
			status.Valid = false
			status.ValidationError = err.Error()
		} else {
			switch status.SourceStatus.Kind {
			case sourcev1.GitRepositoryKind, sourcev1.BucketKind, sourcev1beta2.OCIRepositoryKind:
				status.Valid = slices.ContainsFunc(status.SourceStatus.Conditions, func(c metav1.Condition) bool {
					return c.Type == kcm.ReadyCondition && c.Status == metav1.ConditionTrue
				})
			default:
				status.Valid = true
			}
			if !status.Valid {
				status.ValidationError = sourceNotReadyMessage
			}
		}
		template.Status = status
	}()

	localSource, kind := template.LocalSourceObject()
	key := client.ObjectKeyFromObject(localSource)
	if err = r.Get(ctx, key, localSource); err != nil {
		return fmt.Errorf("failed to get referred %s %s: %w", kind, key, err)
	}

	if status.SourceStatus, err = r.sourceStatusFromObject(localSource); err != nil {
		return fmt.Errorf("failed to get common source status from %s %s: %w", kind, key, err)
	}
	switch kind {
	case sourcev1.GitRepositoryKind, sourcev1.BucketKind, sourcev1beta2.OCIRepositoryKind:
		if err = r.sourceStatusFromFluxObject(localSource, status.SourceStatus); err != nil {
			return fmt.Errorf("failed to get source status from %s %s: %w", kind, key, err)
		}
	}
	return err
}

// reconcileRemoteSource reconciles remote source defined in ServiceTemplate
func (r *ServiceTemplateReconciler) reconcileRemoteSource(ctx context.Context, template *kcm.ServiceTemplate) error {
	l := ctrl.LoggerFrom(ctx)
	remoteSourceObject, kind := template.RemoteSourceObject()
	if remoteSourceObject == nil {
		return errors.New("remote source object is undefined")
	}

	l.Info("Reconciling remote source", "kind", kind)
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, remoteSourceObject, func() error {
		return controllerutil.SetControllerReference(template, remoteSourceObject, r.Scheme())
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile remote source object: %w", err)
	}

	if op == controllerutil.OperationResultCreated || op == controllerutil.OperationResultUpdated {
		l.Info("Successfully mutated remote source object", "kind", kind, "namespaced_name", client.ObjectKeyFromObject(remoteSourceObject), "operation_result", op)
	}
	if op == controllerutil.OperationResultNone {
		l.Info("Remote source object is up-to-date", "kind", kind, "namespaced_name", client.ObjectKeyFromObject(remoteSourceObject))
		var sourceStatus *kcm.SourceStatus
		if sourceStatus, err = r.sourceStatusFromObject(remoteSourceObject); err != nil {
			return fmt.Errorf("failed to get common source status from %s %s: %w", kind, client.ObjectKeyFromObject(remoteSourceObject), err)
		}
		if err = r.sourceStatusFromFluxObject(remoteSourceObject, sourceStatus); err != nil {
			return fmt.Errorf("failed to get source status from %s %s: %w", kind, client.ObjectKeyFromObject(remoteSourceObject), err)
		}
		template.Status.SourceStatus = sourceStatus
		template.Status.Valid = slices.ContainsFunc(sourceStatus.Conditions, func(c metav1.Condition) bool {
			return c.Type == kcm.ReadyCondition && c.Status == metav1.ConditionTrue
		})
		if template.Status.Valid {
			template.Status.ValidationError = ""
		} else {
			template.Status.ValidationError = sourceNotReadyMessage
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.defaultRequeueTime = 1 * time.Minute

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcm.ServiceTemplate{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&sourcev1beta2.OCIRepository{}).
		Owns(&sourcev1.GitRepository{}).
		Owns(&sourcev1.Bucket{}).
		Complete(r)
}

// sourceStatusFromObject extracts the common fields from local or remote source defined for
// kcmv1.ServiceTemplate and returns kcmv1.SourceStatus and an error.
func (r *ServiceTemplateReconciler) sourceStatusFromObject(obj client.Object) (*kcm.SourceStatus, error) {
	gvk, err := apiutil.GVKForObject(obj, r.Scheme())
	if err != nil {
		return nil, err
	}
	return &kcm.SourceStatus{
		Kind:               gvk.Kind,
		Name:               obj.GetName(),
		Namespace:          obj.GetNamespace(),
		ObservedGeneration: obj.GetGeneration(),
	}, nil
}

// sourceStatusFromFluxObject extracts the artifact and conditions info from flux source
// defined for kcmv1.ServiceTemplate and mutates provided kcmv1.SourceStatus. Returns
// an error if the passed object is not a flux source object.
func (*ServiceTemplateReconciler) sourceStatusFromFluxObject(obj client.Object, status *kcm.SourceStatus) error {
	var (
		artifact   *sourcev1.Artifact
		conditions []metav1.Condition
	)
	switch source := obj.(type) {
	case *sourcev1.GitRepository:
		artifact = source.GetArtifact()
		conditions = make([]metav1.Condition, len(source.Status.Conditions))
		copy(conditions, source.Status.Conditions)
	case *sourcev1.Bucket:
		artifact = source.GetArtifact()
		conditions = make([]metav1.Condition, len(source.Status.Conditions))
		copy(conditions, source.Status.Conditions)
	case *sourcev1beta2.OCIRepository:
		artifact = source.GetArtifact()
		conditions = make([]metav1.Condition, len(source.Status.Conditions))
		copy(conditions, source.Status.Conditions)
	default:
		return fmt.Errorf("unsupported source type: %T", source)
	}
	status.Artifact = artifact
	status.Conditions = conditions
	return nil
}
