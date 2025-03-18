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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1beta2 "github.com/fluxcd/source-controller/api/v1beta2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
)

const conditionReady = "Ready"

// ServiceTemplateReconciler reconciles a *Template object
type ServiceTemplateReconciler struct {
	TemplateReconciler
}

func (r *ServiceTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ServiceTemplate")

	serviceTemplate := new(kcm.ServiceTemplate)
	if err := r.Get(ctx, req.NamespacedName, serviceTemplate); err != nil {
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

	switch {
	case serviceTemplate.Spec.Helm != nil:
		return r.ReconcileTemplateHelm(ctx, serviceTemplate)
	case serviceTemplate.Spec.Kustomize != nil:
		return r.ReconcileTemplateKustomize(ctx, serviceTemplate)
	case serviceTemplate.Spec.Resources != nil:
		return r.ReconcileTemplateResources(ctx, serviceTemplate)
	default:
		return ctrl.Result{}, errors.New("no valid template type specified")
	}
}

// ReconcileTemplateHelm reconciles a ServiceTemplate with a Helm chart
func (r *ServiceTemplateReconciler) ReconcileTemplateHelm(ctx context.Context, template *kcm.ServiceTemplate) (ctrl.Result, error) {
	return r.ReconcileTemplate(ctx, template)
}

func (r *ServiceTemplateReconciler) ReconcileTemplateKustomize(ctx context.Context, template *kcm.ServiceTemplate) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	kustomizeSpec := template.Spec.Kustomize
	var err error

	defer func() {
		if updErr := r.Status().Update(ctx, template); updErr != nil {
			l.Error(updErr, "Failed to update ServiceTemplate status")
			err = errors.Join(err, updErr)
		}
		l.Info("Resources reconciliation finished")
	}()

	switch {
	case kustomizeSpec.LocalSourceRef != nil:
		err = r.reconcileLocalSource(ctx, template)
	case kustomizeSpec.RemoteSourceSpec != nil:
		err = r.reconcileRemoteSource(ctx, template)
	}

	l.Info("Kustomization reconciliation finished")
	return ctrl.Result{}, err
}

func (r *ServiceTemplateReconciler) ReconcileTemplateResources(ctx context.Context, template *kcm.ServiceTemplate) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	resourcesSpec := template.Spec.Resources
	var err error

	defer func() {
		if updErr := r.Status().Update(ctx, template); updErr != nil {
			l.Error(updErr, "Failed to update ServiceTemplate status")
			err = errors.Join(err, updErr)
		}
		l.Info("Resources reconciliation finished")
	}()

	switch {
	case resourcesSpec.LocalSourceRef != nil:
		err = r.reconcileLocalSource(ctx, template)
	case resourcesSpec.RemoteSourceSpec != nil:
		err = r.reconcileRemoteSource(ctx, template)
	}
	return ctrl.Result{}, err
}

func (r *ServiceTemplateReconciler) reconcileLocalSource(ctx context.Context, template *kcm.ServiceTemplate) error {
	ref := template.GetLocalSourceRef()
	if ref == nil {
		return errors.New("local source ref is undefined")
	}

	name, namespace := ref.Name, ref.Namespace
	if namespace == "" {
		namespace = r.SystemNamespace
	}
	key := types.NamespacedName{Name: name, Namespace: namespace}

	var err error

	status := kcm.ServiceTemplateStatus{
		TemplateStatusCommon: kcm.TemplateStatusCommon{
			TemplateValidationStatus: kcm.TemplateValidationStatus{},
			ObservedGeneration:       template.Generation,
		},
	}

	switch ref.Kind {
	case "Secret":
		secret := &corev1.Secret{}
		err = r.Get(ctx, key, secret)
		if err != nil {
			err = fmt.Errorf("failed to get referred Secret %s: %w", key, err)
			break
		}
		status.SourceStatus, err = r.sourceStatusFromLocalObject(secret)
		if err != nil {
			return fmt.Errorf("failed to get source status from Secret %s: %w", key, err)
		}
	case "ConfigMap":
		configMap := &corev1.ConfigMap{}
		err = r.Get(ctx, key, configMap)
		if err != nil {
			err = fmt.Errorf("failed to get referred ConfigMap %s: %w", key, err)
			break
		}
		status.SourceStatus, err = r.sourceStatusFromLocalObject(configMap)
		if err != nil {
			return fmt.Errorf("failed to get source status from ConfigMap %s: %w", key, err)
		}
	case "GitRepository":
		gitRepository := &sourcev1.GitRepository{}
		err = r.Get(ctx, key, gitRepository)
		if err != nil {
			err = fmt.Errorf("failed to get referred GitRepository %s: %w", key, err)
			break
		}
		status.SourceStatus, err = r.sourceStatusFromFluxObject(gitRepository)
		if err != nil {
			return fmt.Errorf("failed to get source status from GitRepository %s: %w", key, err)
		}
		conditions := make([]metav1.Condition, len(gitRepository.Status.Conditions))
		copy(conditions, gitRepository.Status.Conditions)
		status.SourceStatus.Conditions = conditions
	case "Bucket":
		bucket := &sourcev1.Bucket{}
		err = r.Get(ctx, key, bucket)
		if err != nil {
			err = fmt.Errorf("failed to get referred Bucket %s: %w", key, err)
			break
		}
		status.SourceStatus, err = r.sourceStatusFromFluxObject(bucket)
		if err != nil {
			return fmt.Errorf("failed to get source status from Bucket %s: %w", key, err)
		}
		conditions := make([]metav1.Condition, len(bucket.Status.Conditions))
		copy(conditions, bucket.Status.Conditions)
		status.SourceStatus.Conditions = conditions
	case "OCIRepository":
		ociRepository := &sourcev1beta2.OCIRepository{}
		err = r.Get(ctx, key, ociRepository)
		if err != nil {
			err = fmt.Errorf("failed to get referred OCIRepository %s: %w", key, err)
			break
		}
		status.SourceStatus, err = r.sourceStatusFromFluxObject(ociRepository)
		if err != nil {
			return fmt.Errorf("failed to get source status from OCIRepository %s: %w", key, err)
		}
		conditions := make([]metav1.Condition, len(ociRepository.Status.Conditions))
		copy(conditions, ociRepository.Status.Conditions)
		status.SourceStatus.Conditions = conditions
	default:
		err = fmt.Errorf("unsupported source kind %s", ref.Kind)
	}

	if err != nil {
		status.TemplateValidationStatus.Valid = false
		status.TemplateValidationStatus.ValidationError = err.Error()
	} else {
		status.TemplateValidationStatus.Valid = true
	}
	template.Status = status
	return err
}

func (r *ServiceTemplateReconciler) reconcileRemoteSource(ctx context.Context, template *kcm.ServiceTemplate) error {
	ref := template.GetRemoteSourceSpec()
	if ref == nil {
		return errors.New("remote source ref is undefined")
	}

	switch {
	case ref.Git != nil:
		return r.reconcileGitRepository(ctx, template, ref)
	case ref.Bucket != nil:
		return r.reconcileBucket(ctx, template, ref)
	case ref.OCI != nil:
		return r.reconcileOCIRepository(ctx, template, ref)
	}
	return errors.New("unknown remote source definition")
}

func (r *ServiceTemplateReconciler) reconcileGitRepository(
	ctx context.Context,
	template *kcm.ServiceTemplate,
	ref *kcm.RemoteSourceSpec,
) error {
	// we'll create a new GitRepository object for every ServiceTemplate. This is needed because we cannot reuse the
	// same GitRepository objects for multiple ServiceTemplates due to possible difference between Git repository parameters.
	// Since there would be no labels to identify created GitRepository objects, they won't be reconciled by the flux
	// controller. The OwnerReference will be set to the ServiceTemplate object, so that the GitRepository object will be
	// deleted when the ServiceTemplate is deleted.
	repositoryName, err := GenerateSourceName(client.ObjectKeyFromObject(template))
	if err != nil {
		return fmt.Errorf("failed to generate GitRepository name: %w", err)
	}
	gitRepository := &sourcev1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repositoryName,
			Namespace: template.Namespace,
		},
	}
	key := client.ObjectKeyFromObject(gitRepository)
	if err := r.Get(ctx, key, gitRepository); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get GitRepository %s: %w", key, err)
		}
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, gitRepository, func() error {
		gitRepository.SetLabels(map[string]string{
			kcm.KCMManagedLabelKey: kcm.KCMManagedLabelValue,
		})
		gitRepository.Spec = sourcev1.GitRepositorySpec{
			Provider:          ref.Provider,
			Interval:          ref.Interval,
			Timeout:           ref.Timeout,
			Ignore:            ref.Ignore,
			Suspend:           ref.Suspend,
			URL:               ref.Git.URL,
			Reference:         ref.Git.Reference,
			Verification:      ref.Git.Verification,
			RecurseSubmodules: ref.Git.RecurseSubmodules,
			Include:           ref.Git.Include,
		}
		if template.Spec.Authorization != nil {
			gitRepository.Spec.SecretRef = template.Spec.Authorization.SecretRef
			gitRepository.Spec.ProxySecretRef = template.Spec.Authorization.ProxySecretRef
		}
		return controllerutil.SetControllerReference(template, gitRepository, r.Client.Scheme())
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile GitRepository object: %w", err)
	}
	if op == controllerutil.OperationResultCreated || op == controllerutil.OperationResultUpdated {
		ctrl.LoggerFrom(ctx).Info("Successfully mutated GitRepository", "GitRepository", client.ObjectKeyFromObject(gitRepository), "operation_result", op)
	}
	if op == controllerutil.OperationResultNone {
		conditions := make([]metav1.Condition, len(gitRepository.Status.Conditions))
		copy(conditions, gitRepository.Status.Conditions)
		template.Status.SourceStatus = &kcm.SourceStatus{
			Kind:               "GitRepository",
			Name:               gitRepository.Name,
			Namespace:          gitRepository.Namespace,
			Artifact:           gitRepository.Status.Artifact,
			ObservedGeneration: gitRepository.Generation,
			Conditions:         conditions,
		}
		template.Status.Valid = slices.ContainsFunc(gitRepository.Status.Conditions, func(c metav1.Condition) bool {
			return c.Type == conditionReady && c.Status == metav1.ConditionTrue
		})
	}
	return nil
}

func (r *ServiceTemplateReconciler) reconcileBucket(
	ctx context.Context,
	template *kcm.ServiceTemplate,
	ref *kcm.RemoteSourceSpec,
) error {
	// we'll create a new Bucket object for every ServiceTemplate. This is needed because we cannot reuse the
	// same Bucket objects for multiple ServiceTemplates due to possible difference between bucket parameters.
	// Since there would be no labels to identify created Bucket objects, they won't be reconciled by the flux
	// controller. The OwnerReference will be set to the ServiceTemplate object, so that the Bucket object will be
	// deleted when the ServiceTemplate is deleted.
	bucketName, err := GenerateSourceName(client.ObjectKeyFromObject(template))
	if err != nil {
		return fmt.Errorf("failed to generate bucket name: %w", err)
	}
	bucket := &sourcev1.Bucket{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bucketName,
			Namespace: template.Namespace,
		},
	}
	key := client.ObjectKeyFromObject(bucket)
	if err := r.Get(ctx, key, bucket); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get Bucket %s: %w", key, err)
		}
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, bucket, func() error {
		bucket.SetLabels(map[string]string{
			kcm.KCMManagedLabelKey: kcm.KCMManagedLabelValue,
		})
		bucket.Spec = sourcev1.BucketSpec{
			Provider:   ref.Provider,
			BucketName: ref.Bucket.BucketName,
			Endpoint:   ref.Bucket.Endpoint,
			STS:        ref.Bucket.STS,
			Region:     ref.Bucket.Region,
			Prefix:     ref.Bucket.Prefix,
			Interval:   ref.Interval,
			Timeout:    ref.Timeout,
			Ignore:     ref.Ignore,
			Suspend:    ref.Suspend,
		}
		if template.Spec.Authorization != nil {
			bucket.Spec.Insecure = template.Spec.Authorization.Insecure
			bucket.Spec.SecretRef = template.Spec.Authorization.SecretRef
			bucket.Spec.ProxySecretRef = template.Spec.Authorization.ProxySecretRef
			bucket.Spec.CertSecretRef = template.Spec.Authorization.CertSecretRef
		}
		return controllerutil.SetControllerReference(template, bucket, r.Client.Scheme())
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile Bucket object: %w", err)
	}
	if op == controllerutil.OperationResultCreated || op == controllerutil.OperationResultUpdated {
		ctrl.LoggerFrom(ctx).Info("Successfully mutated Bucket", "Bucket", client.ObjectKeyFromObject(bucket), "operation_result", op)
	}
	if op == controllerutil.OperationResultNone {
		conditions := make([]metav1.Condition, len(bucket.Status.Conditions))
		copy(conditions, bucket.Status.Conditions)
		template.Status.SourceStatus = &kcm.SourceStatus{
			Kind:               "Bucket",
			Name:               bucket.Name,
			Namespace:          bucket.Namespace,
			Artifact:           bucket.Status.Artifact,
			ObservedGeneration: bucket.Generation,
			Conditions:         conditions,
		}
		template.Status.Valid = slices.ContainsFunc(bucket.Status.Conditions, func(c metav1.Condition) bool {
			return c.Type == conditionReady && c.Status == metav1.ConditionTrue
		})
	}
	return nil
}

func (r *ServiceTemplateReconciler) reconcileOCIRepository(
	ctx context.Context,
	template *kcm.ServiceTemplate,
	ref *kcm.RemoteSourceSpec,
) error {
	// we'll create a new OCIRepository object for every ServiceTemplate. This is needed because we cannot reuse the
	// same OCIRepository objects for multiple ServiceTemplates due to possible difference between OCI repository parameters.
	// Since there would be no labels to identify created OCIRepository objects, they won't be reconciled by the flux
	// controller. The OwnerReference will be set to the ServiceTemplate object, so that the OCIRepository object will be
	// deleted when the ServiceTemplate is deleted.
	ociRepositoryName, err := GenerateSourceName(client.ObjectKeyFromObject(template))
	if err != nil {
		return fmt.Errorf("failed to generate OCIRepository name: %w", err)
	}
	ociRepository := &sourcev1beta2.OCIRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ociRepositoryName,
			Namespace: template.Namespace,
		},
	}
	key := client.ObjectKeyFromObject(ociRepository)
	if err := r.Get(ctx, key, ociRepository); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get OCIRepository %s: %w", key, err)
		}
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, ociRepository, func() error {
		ociRepository.SetLabels(map[string]string{
			kcm.KCMManagedLabelKey: kcm.KCMManagedLabelValue,
		})
		ociRepository.Spec = sourcev1beta2.OCIRepositorySpec{
			URL:                ref.OCI.URL,
			Reference:          ref.OCI.Reference,
			LayerSelector:      ref.OCI.LayerSelector,
			Provider:           ref.Provider,
			Verify:             ref.OCI.Verify,
			ServiceAccountName: ref.OCI.ServiceAccountName,
			Interval:           ref.Interval,
			Timeout:            ref.Timeout,
			Ignore:             ref.Ignore,
			Suspend:            ref.Suspend,
		}
		if template.Spec.Authorization != nil {
			ociRepository.Spec.Insecure = template.Spec.Authorization.Insecure
			ociRepository.Spec.SecretRef = template.Spec.Authorization.SecretRef
			ociRepository.Spec.ProxySecretRef = template.Spec.Authorization.ProxySecretRef
			ociRepository.Spec.CertSecretRef = template.Spec.Authorization.CertSecretRef
		}
		return controllerutil.SetControllerReference(template, ociRepository, r.Client.Scheme())
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile OCIRepository object: %w", err)
	}
	if op == controllerutil.OperationResultCreated || op == controllerutil.OperationResultUpdated {
		ctrl.LoggerFrom(ctx).Info("Successfully mutated OCIRepository", "OCIRepository", client.ObjectKeyFromObject(ociRepository), "operation_result", op)
	}
	if op == controllerutil.OperationResultNone {
		conditions := make([]metav1.Condition, len(ociRepository.Status.Conditions))
		copy(conditions, ociRepository.Status.Conditions)
		template.Status.SourceStatus = &kcm.SourceStatus{
			Kind:               "OCIRepository",
			Name:               ociRepository.Name,
			Namespace:          ociRepository.Namespace,
			Artifact:           ociRepository.Status.Artifact,
			ObservedGeneration: ociRepository.Generation,
			Conditions:         conditions,
		}
		template.Status.Valid = slices.ContainsFunc(ociRepository.Status.Conditions, func(c metav1.Condition) bool {
			return c.Type == conditionReady && c.Status == metav1.ConditionTrue
		})
	}
	return nil
}

// GenerateSourceName creates a deterministic name for a source resource
// using the pattern {parent-name}-{first-8-symbols-of-hash}
func GenerateSourceName(templateNamespacedName types.NamespacedName) (string, error) {
	// Create SHA-256 hash of the template namespaced name
	hasher := sha256.New()
	_, err := hasher.Write([]byte(templateNamespacedName.String()))
	if err != nil {
		return "", fmt.Errorf("failed to hash template namespaced name: %w", err)
	}
	hashBytes := hasher.Sum(nil)
	hash := hex.EncodeToString(hashBytes)

	// Use first 8 characters of the hash
	shortHash := hash[:8]
	sourceName := fmt.Sprintf("%s-%s", templateNamespacedName.Name, shortHash)

	// Ensure the name doesn't exceed 63 characters (Kubernetes name limit)
	if len(sourceName) > 253 {
		// If we need to truncate, keep the hash part intact
		// and truncate the templateNamespacedName name portion
		maxParentNameLen := 253 - 9 // 8 for hash + 1 for hyphen
		truncatedParentName := templateNamespacedName.Name
		if len(truncatedParentName) > maxParentNameLen {
			truncatedParentName = truncatedParentName[:maxParentNameLen]
		}
		sourceName = fmt.Sprintf("%s-%s", truncatedParentName, shortHash)
	}

	return sourceName, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.defaultRequeueTime = 1 * time.Minute

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcm.ServiceTemplate{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&sourcev1beta2.OCIRepository{}, handler.EnqueueRequestsFromMapFunc(getServiceTemplateForEventSource)).
		Watches(&sourcev1.GitRepository{}, handler.EnqueueRequestsFromMapFunc(getServiceTemplateForEventSource)).
		Watches(&sourcev1.Bucket{}, handler.EnqueueRequestsFromMapFunc(getServiceTemplateForEventSource)).
		Complete(r)
}

func getServiceTemplateForEventSource(_ context.Context, eventSource client.Object) []ctrl.Request {
	ownerReference := metav1.GetControllerOf(eventSource)
	if ownerReference == nil {
		return nil
	}
	if ownerReference.Kind != "ServiceTemplate" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: types.NamespacedName{
		Namespace: eventSource.GetNamespace(),
		Name:      ownerReference.Name,
	}}}
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

type sourceAndObject interface {
	client.Object
	sourcev1.Source
}

func (r *ServiceTemplateReconciler) sourceStatusFromFluxObject(obj sourceAndObject) (*kcm.SourceStatus, error) {
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
