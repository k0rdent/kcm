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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
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
	case serviceTemplate.HelmChartSpec() != nil:
		fallthrough
	case serviceTemplate.HelmChartRef() != nil:
		l.V(1).Info("reconciling helm chart")
		return r.ReconcileTemplate(ctx, serviceTemplate)
	case serviceTemplate.LocalSourceRef() != nil:
		l.V(1).Info("reconciling local source")
		return ctrl.Result{}, r.reconcileLocalSource(ctx, serviceTemplate)
	case serviceTemplate.RemoteSourceSpec() != nil:
		l.V(1).Info("reconciling remote source")
		return ctrl.Result{}, r.reconcileRemoteSource(ctx, serviceTemplate)
	default:
		return ctrl.Result{}, fmt.Errorf("invalid ServiceTemplate")
	}
}

// reconcileLocalSource reconciles local source defined in ServiceTemplate
func (r *ServiceTemplateReconciler) reconcileLocalSource(ctx context.Context, template *kcm.ServiceTemplate) (err error) {
	ref := template.LocalSourceRef()
	if ref == nil {
		return errors.New("local source ref is undefined")
	}

	key := client.ObjectKey{Namespace: template.Namespace, Name: ref.Name}

	status := kcm.ServiceTemplateStatus{
		TemplateStatusCommon: kcm.TemplateStatusCommon{
			TemplateValidationStatus: kcm.TemplateValidationStatus{},
			ObservedGeneration:       template.Generation,
		},
	}

	defer func() {
		if err != nil {
			status.TemplateValidationStatus.Valid = false
			status.TemplateValidationStatus.ValidationError = err.Error()
		} else {
			switch status.SourceStatus.Kind {
			case sourcev1.GitRepositoryKind, sourcev1.BucketKind, sourcev1beta2.OCIRepositoryKind:
				status.TemplateValidationStatus.Valid = slices.ContainsFunc(status.SourceStatus.Conditions, func(c metav1.Condition) bool {
					return c.Type == kcm.ReadyCondition && c.Status == metav1.ConditionTrue
				})
			default:
				status.TemplateValidationStatus.Valid = true
			}
			if !status.TemplateValidationStatus.Valid {
				status.TemplateValidationStatus.ValidationError = sourceNotReadyMessage
			}
		}
		template.Status = status
	}()

	switch ref.Kind {
	case "Secret":
		secret := &corev1.Secret{}
		err = r.Get(ctx, key, secret)
		if err != nil {
			return fmt.Errorf("failed to get referred Secret %s: %w", key, err)
		}
		status.SourceStatus, err = r.sourceStatusFromLocalObject(secret)
		if err != nil {
			return fmt.Errorf("failed to get source status from Secret %s: %w", key, err)
		}
	case "ConfigMap":
		configMap := &corev1.ConfigMap{}
		err = r.Get(ctx, key, configMap)
		if err != nil {
			return fmt.Errorf("failed to get referred ConfigMap %s: %w", key, err)
		}
		status.SourceStatus, err = r.sourceStatusFromLocalObject(configMap)
		if err != nil {
			return fmt.Errorf("failed to get source status from ConfigMap %s: %w", key, err)
		}
	case sourcev1.GitRepositoryKind:
		gitRepository := &sourcev1.GitRepository{}
		err = r.Get(ctx, key, gitRepository)
		if err != nil {
			return fmt.Errorf("failed to get referred GitRepository %s: %w", key, err)
		}
		status.SourceStatus, err = r.sourceStatusFromFluxObject(gitRepository)
		if err != nil {
			return fmt.Errorf("failed to get source status from GitRepository %s: %w", key, err)
		}
		conditions := make([]metav1.Condition, len(gitRepository.Status.Conditions))
		copy(conditions, gitRepository.Status.Conditions)
		status.SourceStatus.Conditions = conditions
	case sourcev1.BucketKind:
		bucket := &sourcev1.Bucket{}
		err = r.Get(ctx, key, bucket)
		if err != nil {
			return fmt.Errorf("failed to get referred Bucket %s: %w", key, err)
		}
		status.SourceStatus, err = r.sourceStatusFromFluxObject(bucket)
		if err != nil {
			return fmt.Errorf("failed to get source status from Bucket %s: %w", key, err)
		}
		conditions := make([]metav1.Condition, len(bucket.Status.Conditions))
		copy(conditions, bucket.Status.Conditions)
		status.SourceStatus.Conditions = conditions
	case sourcev1beta2.OCIRepositoryKind:
		ociRepository := &sourcev1beta2.OCIRepository{}
		err = r.Get(ctx, key, ociRepository)
		if err != nil {
			return fmt.Errorf("failed to get referred OCIRepository %s: %w", key, err)
		}
		status.SourceStatus, err = r.sourceStatusFromFluxObject(ociRepository)
		if err != nil {
			return fmt.Errorf("failed to get source status from OCIRepository %s: %w", key, err)
		}
		conditions := make([]metav1.Condition, len(ociRepository.Status.Conditions))
		copy(conditions, ociRepository.Status.Conditions)
		status.SourceStatus.Conditions = conditions
	default:
		return fmt.Errorf("unsupported source kind %s", ref.Kind)
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
		return controllerutil.SetControllerReference(template, remoteSourceObject, r.Client.Scheme())
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile OCIRepository object: %w", err)
	}

	if op == controllerutil.OperationResultCreated || op == controllerutil.OperationResultUpdated {
		l.Info("Successfully mutated remote source object", "kind", kind, "namespaced_name", client.ObjectKeyFromObject(remoteSourceObject), "operation_result", op)
	}
	if op == controllerutil.OperationResultNone {
		l.Info("Remote source object is up-to-date", "kind", kind, "namespaced_name", client.ObjectKeyFromObject(remoteSourceObject))
		var (
			sourceStatus *kcm.SourceStatus
			conditions   []metav1.Condition
		)
		switch source := remoteSourceObject.(type) {
		case *sourcev1.GitRepository:
			sourceStatus, err = r.sourceStatusFromFluxObject(source)
			conditions = make([]metav1.Condition, len(source.Status.Conditions))
			copy(conditions, source.Status.Conditions)
		case *sourcev1.Bucket:
			sourceStatus, err = r.sourceStatusFromFluxObject(source)
			conditions = make([]metav1.Condition, len(source.Status.Conditions))
			copy(conditions, source.Status.Conditions)
		case *sourcev1beta2.OCIRepository:
			sourceStatus, err = r.sourceStatusFromFluxObject(source)
			conditions = make([]metav1.Condition, len(source.Status.Conditions))
			copy(conditions, source.Status.Conditions)
		default:
			return fmt.Errorf("unsupported remote source type: %T", source)
		}
		template.Status.SourceStatus = sourceStatus
		template.Status.SourceStatus.Conditions = conditions
		template.Status.Valid = slices.ContainsFunc(conditions, func(c metav1.Condition) bool {
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

func (r *ServiceTemplateReconciler) sourceStatusFromLocalObject(obj client.Object) (*kcm.SourceStatus, error) {
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

func (r *ServiceTemplateReconciler) sourceStatusFromFluxObject(obj interface {
	client.Object
	sourcev1.Source
},
) (*kcm.SourceStatus, error) {
	gvk, err := apiutil.GVKForObject(obj, r.Scheme())
	if err != nil {
		return nil, err
	}
	return &kcm.SourceStatus{
		Kind:               gvk.Kind,
		Name:               obj.GetName(),
		Namespace:          obj.GetNamespace(),
		ObservedGeneration: obj.GetGeneration(),
		Artifact:           obj.GetArtifact(),
	}, nil
}
