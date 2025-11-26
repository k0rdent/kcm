// Copyright 2025
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

package region

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/controller/components"
	"github.com/K0rdent/kcm/internal/record"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	labelsutil "github.com/K0rdent/kcm/internal/util/labels"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
	schemeutil "github.com/K0rdent/kcm/internal/util/scheme"
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
)

// Reconciler reconciles a Region object
type Reconciler struct {
	MgmtClient                    client.Client
	Manager                       manager.Manager
	Config                        *rest.Config
	DynamicClient                 *dynamic.DynamicClient
	SystemNamespace               string
	GlobalRegistry                string
	RegistryCertSecretName        string // Name of a Secret with Registry Root CA with ca.crt key; used by RegionReconciler
	RegistryCredentialsSecretName string // Name of a Secret with Registry Credentials (username/password)

	IsDisabledValidationWH bool // is webhook disabled set via the controller flags

	DefaultHelmTimeout time.Duration
	defaultRequeueTime time.Duration
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling Region")

	region := &kcmv1.Region{}
	if err := r.MgmtClient.Get(ctx, req.NamespacedName, region); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("Region not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		l.Error(err, "Failed to get Region")
		return ctrl.Result{}, err
	}

	if r.IsDisabledValidationWH && region.DeletionTimestamp.IsZero() {
		if err := validationutil.RegionClusterReference(ctx, r.MgmtClient, r.SystemNamespace, region); err != nil {
			r.warnf(region, "RegionConfigurationError", "Invalid Region configuration: %v", err)
			r.setReadyCondition(region, err)
			// invalid configuration, to need to requeue
			return ctrl.Result{}, nil
		}
	}

	rgnlClient, restCfg, err := kubeutil.GetRegionalClient(ctx, r.MgmtClient, r.SystemNamespace, region, schemeutil.GetRegionalScheme)
	if err != nil {
		err := fmt.Errorf("failed to get clients for the %s region: %w", region.Name, err)
		r.setReadyCondition(region, err)
		return ctrl.Result{}, errors.Join(err, r.updateStatus(ctx, region))
	}

	if !region.DeletionTimestamp.IsZero() {
		l.Info("Deleting Region")
		return r.delete(ctx, rgnlClient, region)
	}

	return r.update(ctx, rgnlClient, restCfg, region)
}

func (r *Reconciler) update(ctx context.Context, rgnlClient client.Client, restConfig *rest.Config, region *kcmv1.Region) (result ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)

	if controllerutil.AddFinalizer(region, kcmv1.RegionFinalizer) {
		if err := r.MgmtClient.Update(ctx, region); err != nil {
			l.Error(err, "failed to update Region finalizers")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	if updated, err := labelsutil.AddKCMComponentLabel(ctx, r.MgmtClient, region); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, err
	}

	defer func() {
		r.setReadyCondition(region, err)
		err = errors.Join(err, r.updateStatus(ctx, region))
	}()

	if r.IsDisabledValidationWH {
		if err := validationutil.RegionClusterReference(ctx, r.MgmtClient, r.SystemNamespace, region); err != nil {
			r.warnf(region, "RegionConfigurationError", "invalid Region configuration: %v", err)
			// invalid configuration, to need to requeue
			return ctrl.Result{}, nil
		}
	}

	mgmt := &kcmv1.Management{}
	if err := r.MgmtClient.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, mgmt); err != nil {
		l.Error(err, "Failed to get Management")
		return ctrl.Result{}, err
	}

	release := &kcmv1.Release{}
	if err := r.MgmtClient.Get(ctx, client.ObjectKey{Name: mgmt.Spec.Release}, release); err != nil {
		l.Error(err, "Failed to get Release")
		return ctrl.Result{}, err
	}

	// Cleanup only components that belong to this region (with `k0rdent.mirantis.com/region: <regionName>` label)
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      kcmv1.KCMRegionLabelKey,
				Values:   []string{region.Name},
				Operator: metav1.LabelSelectorOpIn,
			},
		},
	}
	if err := components.Cleanup(ctx, r.MgmtClient, region, labelSelector, r.SystemNamespace); err != nil {
		r.warnf(region, "ComponentsCleanupFailed", "failed to cleanup removed components: %v", err)
		l.Error(err, "failed to cleanup removed components")
		return ctrl.Result{}, err
	}

	kubeconfigRef, err := kubeutil.GetRegionalKubeconfigSecretRef(region)
	if err != nil {
		l.Error(err, "failed to get kubeconfig reference")
		return ctrl.Result{}, err
	}

	overridenKubeconfigRef, err := r.copyRegionalKubeConfigSecret(ctx, region, kubeconfigRef)
	if err != nil {
		l.Error(err, "failed to copy kubeconfig secret")
		r.warnf(region, "KubeConfigSecretCopyFailed", "Failed to copy kubeconfig secret: %s", err)
		return ctrl.Result{}, err
	}

	if err := r.handleCertificateSecret(ctx, r.MgmtClient, rgnlClient, region); err != nil {
		l.Error(err, "failed to handle certificate secrets")
		r.warnf(region, "CertificateSecretsSetupFailed", "Failed to handle certificate secrets: %s", err)
		return ctrl.Result{}, err
	}

	opts := components.ReconcileComponentsOpts{
		DefaultHelmTimeout:            r.DefaultHelmTimeout,
		Namespace:                     r.SystemNamespace,
		GlobalRegistry:                r.GlobalRegistry,
		RegistryCertSecretName:        r.RegistryCertSecretName,
		RegistryCredentialsSecretName: r.RegistryCredentialsSecretName,

		KubeConfigRef: overridenKubeconfigRef,

		CreateNamespace: true,
		Labels: map[string]string{
			kcmv1.KCMRegionLabelKey: region.Name,
		},
	}

	requeue, err := components.Reconcile(ctx, r.MgmtClient, rgnlClient, region, restConfig, release, opts)
	if err != nil {
		l.Error(err, "failed to reconcile KCM Regional components")
		r.warnf(region, "RegionComponentsInstallationFailed", "Failed to install KCM components on the regional cluster: %w", err.Error())
		return ctrl.Result{}, err
	}

	if requeue {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	region.Status.ObservedGeneration = region.Generation

	return ctrl.Result{}, nil
}

// copyRegionalKubeConfigSecret copies the Regional cluster kubeconfig to the system namespace
// when the Region uses the ClusterDeployment as its source.
//
// If kubeconfig reference is given, it is
// expected that the referenced Secret is ALREADY in the system namespace.
//
// To deploy the regional cluster components into the Regional cluster in the system namespace, its kubeconfig must
// be present in the system namespace.
func (r *Reconciler) copyRegionalKubeConfigSecret(ctx context.Context, region *kcmv1.Region, sourceKubeConfigSecretRef *fluxmeta.SecretKeyReference) (*fluxmeta.SecretKeyReference, error) {
	// nothing to copy when the region does not have clusterDeployment reference defined (the kubeconfig reference expected to already be in the system namespace)
	// or the namespace equals the system namespace.
	if region == nil || region.Spec.ClusterDeployment == nil || region.Spec.ClusterDeployment.Namespace == r.SystemNamespace {
		return sourceKubeConfigSecretRef, nil
	}

	if sourceKubeConfigSecretRef == nil {
		return nil, errors.New("source kubeconfig secret reference is nil")
	}

	nameOverride := region.Spec.ClusterDeployment.Namespace + "." + sourceKubeConfigSecretRef.Name

	if err := kubeutil.CopySecret(
		ctx,
		r.MgmtClient,
		r.MgmtClient,
		client.ObjectKey{Namespace: region.Spec.ClusterDeployment.Namespace, Name: sourceKubeConfigSecretRef.Name},
		r.SystemNamespace,
		nameOverride,
		region,
		nil,
	); err != nil {
		return nil, fmt.Errorf("failed to copy regional cluster kubeconfig secret to the system namespace: %w", err)
	}
	return &fluxmeta.SecretKeyReference{Name: nameOverride, Key: sourceKubeConfigSecretRef.Key}, nil
}

func (r *Reconciler) handleCertificateSecret(ctx context.Context, mgmtClient, rgnClient client.Client, region *kcmv1.Region) error {
	secretsToHandle := []string{}
	
	if r.RegistryCertSecretName != "" {
		secretsToHandle = append(secretsToHandle, r.RegistryCertSecretName)
	}
	
	if r.RegistryCredentialsSecretName != "" {
		secretsToHandle = append(secretsToHandle, r.RegistryCredentialsSecretName)
	}
	
	if len(secretsToHandle) == 0 {
		return nil
	}

	l := ctrl.LoggerFrom(ctx).WithName("handle-secrets")

	l.V(1).Info("Copying certificate and credential secrets from the management to the regional cluster")
	for _, secretName := range secretsToHandle {
		if err := kubeutil.CopySecret(
			ctx,
			mgmtClient,
			rgnClient,
			client.ObjectKey{Namespace: r.SystemNamespace, Name: secretName},
			r.SystemNamespace,
			"",
			nil,
			map[string]string{kcmv1.KCMRegionLabelKey: region.Name},
		); err != nil {
			l.Error(err, "failed to copy Secret for the regional cluster")
			return err
		}
	}

	return nil
}

// setReadyCondition updates the Region resource's "Ready" condition based on whether
// all components are healthy.
func (r *Reconciler) setReadyCondition(region *kcmv1.Region, err error) {
	readyCond := metav1.Condition{
		Type:               kcmv1.ReadyCondition,
		ObservedGeneration: region.Generation,
		Status:             metav1.ConditionTrue,
		Reason:             kcmv1.AllComponentsHealthyReason,
		Message:            "All components are successfully installed",
	}
	if err != nil {
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = kcmv1.RegionConfigurationErrorReason
		readyCond.Message = err.Error()
		if meta.SetStatusCondition(&region.Status.Conditions, readyCond) {
			r.warnf(region, "RegionIsNotReady", err.Error())
		}
	}
	var failing []string
	for name, comp := range region.Status.Components {
		if !comp.Success {
			failing = append(failing, name)
		}
	}

	sort.Strings(failing)
	if len(failing) > 0 {
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = kcmv1.NotAllComponentsHealthyReason
		readyCond.Message = fmt.Sprintf("Components not ready: %v", failing)
	}
	if meta.SetStatusCondition(&region.Status.Conditions, readyCond) && readyCond.Status == metav1.ConditionTrue {
		r.eventf(region, "RegionIsReady", "Regional KCM components are ready")
	}
}

func (r *Reconciler) delete(ctx context.Context, rgnClient client.Client, region *kcmv1.Region) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	r.eventf(region, "RemovingRegion", "Removing KCM regional components")

	var err error
	defer func() {
		err = errors.Join(err, r.updateStatus(ctx, region))
	}()

	if r.IsDisabledValidationWH {
		if err = validationutil.RegionDeletionAllowed(ctx, r.MgmtClient, region); err != nil {
			r.warnf(region, "RegionDeletionFailed", "Failed to delete region: %v", err)
			return ctrl.Result{}, err
		}
	}

	requeue, err := r.removeHelmReleases(ctx, region)
	if err != nil {
		return ctrl.Result{}, err
	}
	if requeue {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	err = rgnClient.DeleteAllOf(ctx, &corev1.Secret{}, client.InNamespace(r.SystemNamespace), client.MatchingLabels{kcmv1.KCMRegionLabelKey: region.Name})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to delete all secrets managed by %s region: %w", region.Name, err)
	}

	r.eventf(region, "RemovedRegion", "Region has been removed")
	l.Info("Removing Region finalizer")
	if controllerutil.RemoveFinalizer(region, kcmv1.RegionFinalizer) {
		return ctrl.Result{}, r.MgmtClient.Update(ctx, region)
	}
	return ctrl.Result{}, nil
}

func (r *Reconciler) removeHelmReleases(ctx context.Context, region *kcmv1.Region) (bool, error) {
	l := ctrl.LoggerFrom(ctx)

	// List managed HelmReleases for this region
	var hrList helmcontrollerv2.HelmReleaseList
	listOpts := []client.ListOption{
		client.MatchingLabels{kcmv1.KCMRegionLabelKey: region.Name},
		client.InNamespace(r.SystemNamespace),
	}
	if err := r.MgmtClient.List(ctx, &hrList, listOpts...); err != nil {
		return false, fmt.Errorf("failed to list %s: %w", helmcontrollerv2.GroupVersion.WithKind(helmcontrollerv2.HelmReleaseKind), err)
	}

	// We should ensure the removal order according to helm release dependencies
	dependents := make(map[string]map[string]struct{})
	for _, hr := range hrList.Items {
		for _, dep := range hr.Spec.DependsOn {
			if dependents[dep.Name] == nil {
				dependents[dep.Name] = make(map[string]struct{})
			}
			dependents[dep.Name][hr.Name] = struct{}{}
		}
	}

	// Try to delete HelmReleases that no one depends on
	var errs error
	hrNames := make([]string, 0, len(hrList.Items))
	for _, hr := range hrList.Items {
		hrNames = append(hrNames, hr.Name)
		if len(dependents[hr.Name]) > 0 {
			l.V(1).Info("Skipping HelmRelease with dependents", "name", hr.Name, "dependents", dependents[hr.Name])
			continue
		}

		l.V(1).Info("Deleting HelmRelease", "name", hr.Name)
		r.eventf(region, "RemovingComponent", "Removing %s HelmRelease", hr.Name)

		if err := r.MgmtClient.Delete(ctx, &hr); client.IgnoreNotFound(err) != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to delete %s: %w", client.ObjectKeyFromObject(&hr), err))
			continue
		}

		l.V(1).Info("Deleted HelmRelease", "name", hr.Name)
		r.eventf(region, "ComponentRemoved", "Removed %s HelmRelease", hr.Name)
	}

	if errs != nil {
		return false, errs
	}

	// If there are still HelmReleases left, requeue until cleanup is complete
	slices.Sort(hrNames)
	if len(hrNames) > 0 {
		l.Info("Waiting for all HelmReleases to be deleted before removing finalizer")
		meta.SetStatusCondition(&region.Status.Conditions, metav1.Condition{
			Type:               kcmv1.ReadyCondition,
			ObservedGeneration: region.Generation,
			Status:             metav1.ConditionFalse,
			Reason:             kcmv1.NotAllComponentsHealthyReason,
			Message:            fmt.Sprintf("Waiting for all HelmReleases to be deleted: %s", hrNames),
		})
		return true, nil
	}
	return false, nil
}

func (r *Reconciler) updateStatus(ctx context.Context, region *kcmv1.Region) error {
	if err := r.MgmtClient.Status().Update(ctx, region); err != nil {
		return fmt.Errorf("failed to update status for Region %s: %w", region.Name, err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.defaultRequeueTime = 10 * time.Second

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.Region{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&kcmv1.Management{}, handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []ctrl.Request {
			return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: kcmv1.ManagementName}}}
		}), builder.WithPredicates(predicate.Funcs{
			GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
			DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
			UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
				oldO, ok := tue.ObjectOld.(*kcmv1.Management)
				if !ok {
					return false
				}

				newO, ok := tue.ObjectNew.(*kcmv1.Management)
				if !ok {
					return false
				}
				return oldO.Spec.Release != newO.Spec.Release
			},
		})).
		Complete(r)
}

func (*Reconciler) eventf(region *kcmv1.Region, reason, message string, args ...any) {
	record.Eventf(region, region.Generation, reason, message, args...)
}

func (*Reconciler) warnf(region *kcmv1.Region, reason, message string, args ...any) {
	record.Warnf(region, region.Generation, reason, message, args...)
}
