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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	fluxconditions "github.com/fluxcd/pkg/runtime/conditions"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"helm.sh/helm/v3/pkg/chartutil"
	helmreleasepkg "helm.sh/helm/v3/pkg/release"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	capioperatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha2"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
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
	"github.com/K0rdent/kcm/internal/certmanager"
	"github.com/K0rdent/kcm/internal/helm"
	"github.com/K0rdent/kcm/internal/record"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/pointer"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
	"github.com/K0rdent/kcm/internal/utils/validation"
)

// ManagementReconciler reconciles a Management object
type ManagementReconciler struct {
	Client                 client.Client
	Manager                manager.Manager
	Config                 *rest.Config
	DynamicClient          *dynamic.DynamicClient
	SystemNamespace        string
	GlobalRegistry         string
	GlobalK0sURL           string
	K0sURLCertSecretName   string // Name of a Secret with K0s Download URL Root CA with ca.crt key; to be passed to the ClusterDeploymentReconciler
	RegistryCertSecretName string // Name of a Secret with Registry Root CA with ca.crt key; used by ManagementReconciler and ClusterDeploymentReconciler

	defaultRequeueTime time.Duration

	CreateAccessManagement bool
	IsDisabledValidationWH bool // is webhook disabled set via the controller flags

	sveltosDependentControllersStarted bool
}

func (r *ManagementReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling Management")

	management := &kcmv1.Management{}
	if err := r.Client.Get(ctx, req.NamespacedName, management); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("Management not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		l.Error(err, "Failed to get Management")
		return ctrl.Result{}, err
	}

	if !management.DeletionTimestamp.IsZero() {
		l.Info("Deleting Management")
		return r.delete(ctx, management)
	}

	return r.update(ctx, management)
}

func (r *ManagementReconciler) update(ctx context.Context, management *kcmv1.Management) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	if controllerutil.AddFinalizer(management, kcmv1.ManagementFinalizer) {
		if err := r.Client.Update(ctx, management); err != nil {
			l.Error(err, "failed to update Management finalizers")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if updated, err := utils.AddKCMComponentLabel(ctx, r.Client, management); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{}, err
	}

	if changed, err := utils.SetPredeclaredSecretsCondition(ctx, r.Client, management, record.Warnf, r.SystemNamespace, r.RegistryCertSecretName); err != nil { // if changed and NO error we will eventually update the status
		l.Error(err, "failed to check if given Secrets exist")
		if changed {
			return ctrl.Result{}, r.updateStatus(ctx, management)
		}
		return ctrl.Result{}, err
	}

	release, err := r.getRelease(ctx, management)
	if err != nil && !r.IsDisabledValidationWH {
		r.warnf(management, "ReleaseGetFailed", "failed to get release: %v", err)
		l.Error(err, "failed to get Release")
		return ctrl.Result{}, err
	}

	if r.IsDisabledValidationWH {
		valid, err := r.validateManagement(ctx, management, release)
		if !valid {
			return ctrl.Result{}, err
		}
	}

	if err := r.cleanupRemovedComponents(ctx, management); err != nil {
		r.warnf(management, "ComponentsCleanupFailed", "failed to cleanup removed components: %v", err)
		l.Error(err, "failed to cleanup removed components")
		return ctrl.Result{}, err
	}

	requeueAutoUpgradeBackups, err := r.ensureUpgradeBackup(ctx, management)
	if err != nil {
		r.warnf(management, "EnsureReleaseBackupsFailed", "failed to ensure release backups before upgrades: %v", err)
		l.Error(err, "failed to ensure release backups before upgrades")
		return ctrl.Result{}, err
	}
	if requeueAutoUpgradeBackups {
		const requeueAfter = 1 * time.Minute
		l.Info("Still creating or waiting for backups to be completed before the upgrade", "current_release", management.Status.Release, "new_release", management.Spec.Release, "requeue_after", requeueAfter)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	if err := r.ensureAccessManagement(ctx, management); err != nil {
		r.warnf(management, "EnsureAccessManagementFailed", "failed to ensure AccessManagement is created: %v", err)
		l.Error(err, "failed to ensure AccessManagement is created")
		return ctrl.Result{}, err
	}

	requeue, errs := r.reconcileManagementComponents(ctx, management, release)

	shouldRequeue, err := r.startDependentControllers(ctx, management)
	if err != nil {
		r.warnf(management, "ControllersStartFailed", "Failed to start dependent controllers: %v", err)
		return ctrl.Result{}, err
	}
	if shouldRequeue {
		requeue = true
	}

	r.setReadyCondition(management)

	errs = errors.Join(errs, r.updateStatus(ctx, management))
	if errs != nil {
		l.Error(errs, "Multiple errors during Management reconciliation")
		return ctrl.Result{}, errs
	}
	if requeue {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ManagementReconciler) reconcileManagementComponents(ctx context.Context, management *kcmv1.Management, release *kcmv1.Release) (bool, error) {
	l := ctrl.LoggerFrom(ctx)

	var (
		errs error

		statusAccumulator = &mgmtStatusAccumulator{
			providers:              kcmv1.Providers{"infrastructure-internal"},
			components:             make(map[string]kcmv1.ComponentStatus),
			compatibilityContracts: make(map[string]kcmv1.CompatibilityContracts),
		}
		requeue bool
	)

	components, err := r.getWrappedComponents(ctx, management, release)
	if err != nil {
		l.Error(err, "failed to wrap KCM components")
		return requeue, err
	}

	for _, component := range components {
		l.V(1).Info("reconciling components", "component", component)
		var notReadyDeps []string
		for _, dep := range component.dependsOn {
			if !statusAccumulator.components[dep.Name].Success {
				notReadyDeps = append(notReadyDeps, dep.Name)
			}
		}
		if len(notReadyDeps) > 0 {
			errMsg := "Some dependencies are not ready yet. Waiting for " + strings.Join(notReadyDeps, ", ")
			l.Info(errMsg, "template", component.Template)
			updateComponentsStatus(statusAccumulator, component, nil, errMsg)
			requeue = true
			continue
		}
		template := new(kcmv1.ProviderTemplate)
		if err := r.Client.Get(ctx, client.ObjectKey{Name: component.Template}, template); err != nil {
			errMsg := fmt.Sprintf("Failed to get ProviderTemplate %s: %s", component.Template, err)
			updateComponentsStatus(statusAccumulator, component, nil, errMsg)
			errs = errors.Join(errs, errors.New(errMsg))

			continue
		}

		if !template.Status.Valid {
			errMsg := fmt.Sprintf("Template %s is not marked as valid", component.Template)
			updateComponentsStatus(statusAccumulator, component, template, errMsg)
			errs = errors.Join(errs, errors.New(errMsg))

			continue
		}

		hrReconcileOpts := helm.ReconcileHelmReleaseOpts{
			Values:          component.Config,
			ChartRef:        template.Status.ChartRef,
			DependsOn:       component.dependsOn,
			TargetNamespace: component.targetNamespace,
			Install:         component.installSettings,
		}
		if template.Spec.Helm.ChartSpec != nil {
			hrReconcileOpts.ReconcileInterval = &template.Spec.Helm.ChartSpec.Interval.Duration
		}

		_, operation, err := helm.ReconcileHelmRelease(ctx, r.Client, component.helmReleaseName, r.SystemNamespace, hrReconcileOpts)
		if err != nil {
			errMsg := fmt.Sprintf("Failed to reconcile HelmRelease %s/%s: %v", r.SystemNamespace, component.helmReleaseName, err)
			r.warnf(management, "HelmReleaseReconcileFailed", errMsg)
			updateComponentsStatus(statusAccumulator, component, template, errMsg)
			errs = errors.Join(errs, errors.New(errMsg))

			continue
		}
		if operation == controllerutil.OperationResultCreated {
			r.eventf(management, "HelmReleaseCreated", "Successfully created %s/%s HelmRelease", r.SystemNamespace, component.helmReleaseName)
		}
		if operation == controllerutil.OperationResultUpdated {
			r.eventf(management, "HelmReleaseUpdated", "Successfully updated %s/%s HelmRelease", r.SystemNamespace, component.helmReleaseName)
		}

		if err := r.checkProviderStatus(ctx, component); err != nil {
			l.Info("Provider is not yet ready", "template", component.Template, "err", err)
			requeue = true
			updateComponentsStatus(statusAccumulator, component, template, err.Error())
			continue
		}

		updateComponentsStatus(statusAccumulator, component, template, "")
	}

	management.Status.AvailableProviders = statusAccumulator.providers
	management.Status.CAPIContracts = statusAccumulator.compatibilityContracts
	management.Status.Components = statusAccumulator.components
	management.Status.ObservedGeneration = management.Generation
	management.Status.Release = management.Spec.Release

	return requeue, errs
}

func (r *ManagementReconciler) validateManagement(ctx context.Context, management *kcmv1.Management, release *kcmv1.Release) (valid bool, _ error) {
	if release == nil {
		return false, errors.New("unexpected nil Release reference")
	}

	l := ctrl.LoggerFrom(ctx)

	l.V(1).Info("Validating Release readiness")
	releaseFound := !release.CreationTimestamp.IsZero()
	if !releaseFound || !release.Status.Ready {
		reason, relErrMsg := kcmv1.ReleaseIsNotReadyReason, fmt.Sprintf("Release %s is not ready", management.Spec.Release)
		if !releaseFound {
			reason, relErrMsg = kcmv1.ReleaseIsNotFoundReason, fmt.Sprintf("Release %s is not found", management.Spec.Release)
		}

		r.warnf(management, reason, relErrMsg)
		l.Error(errors.New(relErrMsg), "Will not retrigger until Release exists and valid")
		meta.SetStatusCondition(&management.Status.Conditions, metav1.Condition{
			Type:               kcmv1.ReadyCondition,
			ObservedGeneration: management.Generation,
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            relErrMsg,
		})

		return false, r.updateStatus(ctx, management)
	}

	l.V(1).Info("Validating providers CAPI contracts compatibility")
	incompContracts, err := validation.GetIncompatibleContracts(ctx, r.Client, release, management)
	if len(incompContracts) == 0 && err == nil { // if NO error
		return true, nil
	}

	isNotFoundOrNotReady := errors.Is(err, validation.ErrProviderIsNotReady) || apierrors.IsNotFound(err)
	if err != nil && !isNotFoundOrNotReady {
		l.Error(err, "failed to get incompatible contracts")
		return false, fmt.Errorf("failed to get incompatible contracts: %w", err)
	}

	errMsg := incompContracts
	if isNotFoundOrNotReady {
		errMsg = err.Error()
	}

	r.warnf(management, "IncompatibleContracts", errMsg)
	l.Error(errors.New(errMsg), "Will not retrigger this error")
	meta.SetStatusCondition(&management.Status.Conditions, metav1.Condition{
		Type:               kcmv1.ReadyCondition,
		ObservedGeneration: management.Generation,
		Status:             metav1.ConditionFalse,
		Reason:             kcmv1.HasIncompatibleContractsReason,
		Message:            errMsg,
	})

	return false, r.updateStatus(ctx, management)
}

// startDependentControllers starts controllers that cannot be started
// at process startup because of some dependency like CRDs being present.
func (r *ManagementReconciler) startDependentControllers(ctx context.Context, management *kcmv1.Management) (requeue bool, err error) {
	if r.sveltosDependentControllersStarted {
		// Only need to start controllers once.
		return false, nil
	}

	l := ctrl.LoggerFrom(ctx).WithValues("provider_name", kcmv1.ProviderSveltosName)
	if !management.Status.Components[kcmv1.ProviderSveltosName].Success {
		msg := "Waiting for provider to be ready to setup controllers dependent on it"
		r.eventf(management, "WaitingForSveltosReadiness", msg)
		l.Info(msg)
		return true, nil
	}

	currentNamespace := utils.CurrentNamespace()

	l.Info("Provider has been successfully installed, so setting up controller for ClusterDeployment")
	if err = (&ClusterDeploymentReconciler{
		DynamicClient:          r.DynamicClient,
		SystemNamespace:        currentNamespace,
		IsDisabledValidationWH: r.IsDisabledValidationWH,
		GlobalRegistry:         r.GlobalRegistry,
		GlobalK0sURL:           r.GlobalK0sURL,
		K0sURLCertSecretName:   r.K0sURLCertSecretName,
		RegistryCertSecretName: r.RegistryCertSecretName,
	}).SetupWithManager(r.Manager); err != nil {
		return false, fmt.Errorf("failed to setup controller for ClusterDeployment: %w", err)
	}
	r.eventf(management, "ClusterDeploymentControllerEnabled", "Sveltos is ready. Enabling ClusterDeployment controller")
	l.Info("Setup for ClusterDeployment controller successful")

	l.Info("Provider has been successfully installed, so setting up controller for MultiClusterService")
	if err = (&MultiClusterServiceReconciler{
		SystemNamespace:        currentNamespace,
		IsDisabledValidationWH: r.IsDisabledValidationWH,
	}).SetupWithManager(r.Manager); err != nil {
		return false, fmt.Errorf("failed to setup controller for MultiClusterService: %w", err)
	}
	r.eventf(management, "MultiClusterServiceControllerEnabled", "Sveltos is ready. Enabling MultiClusterService controller")
	l.Info("Setup for MultiClusterService controller successful")

	r.sveltosDependentControllersStarted = true
	return false, nil
}

func (r *ManagementReconciler) cleanupRemovedComponents(ctx context.Context, management *kcmv1.Management) error {
	var (
		errs error
		l    = ctrl.LoggerFrom(ctx)
	)

	managedHelmReleases := new(helmcontrollerv2.HelmReleaseList)
	if err := r.Client.List(ctx, managedHelmReleases,
		client.MatchingLabels{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue},
		client.InNamespace(r.SystemNamespace), // all helmreleases are being installed only in the system namespace
	); err != nil {
		return fmt.Errorf("failed to list %s: %w", helmcontrollerv2.GroupVersion.WithKind(helmcontrollerv2.HelmReleaseKind), err)
	}

	releasesList := &metav1.PartialObjectMetadataList{}
	if len(managedHelmReleases.Items) > 0 {
		releasesList.SetGroupVersionKind(kcmv1.GroupVersion.WithKind(kcmv1.ReleaseKind))
		if err := r.Client.List(ctx, releasesList); err != nil {
			return fmt.Errorf("failed to list releases: %w", err)
		}
	}

	for _, hr := range managedHelmReleases.Items {
		// do not remove non-management related components (#703)
		if len(hr.OwnerReferences) > 0 {
			continue
		}

		componentName := hr.Name // providers(components) names map 1-1 to the helmreleases names

		if componentName == kcmv1.CoreCAPIName ||
			componentName == kcmv1.CoreKCMName ||
			slices.ContainsFunc(releasesList.Items, func(r metav1.PartialObjectMetadata) bool {
				return componentName == utils.TemplatesChartFromReleaseName(r.Name)
			}) ||
			slices.ContainsFunc(management.Spec.Providers, func(newComp kcmv1.Provider) bool { return componentName == newComp.Name }) {
			continue
		}

		l.V(1).Info("Found component to remove", "component_name", componentName)
		r.eventf(management, "ComponentRemoved", "The %s component was removed from the Management: removing HelmRelease", componentName)

		if err := r.Client.Delete(ctx, &hr); client.IgnoreNotFound(err) != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to delete %s: %w", client.ObjectKeyFromObject(&hr), err))
			continue
		}
		l.V(1).Info("Removed HelmRelease", "reference", client.ObjectKeyFromObject(&hr).String())
	}

	return errs
}

func (r *ManagementReconciler) ensureAccessManagement(ctx context.Context, mgmt *kcmv1.Management) error {
	if !r.CreateAccessManagement {
		return nil
	}

	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring AccessManagement is created")

	amObj := &kcmv1.AccessManagement{
		ObjectMeta: metav1.ObjectMeta{
			Name: kcmv1.AccessManagementName,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: kcmv1.GroupVersion.String(),
					Kind:       mgmt.Kind,
					Name:       mgmt.Name,
					UID:        mgmt.UID,
				},
			},
		},
	}

	err := r.Client.Get(ctx, client.ObjectKey{Name: kcmv1.AccessManagementName}, amObj)

	if err == nil {
		return nil
	}

	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get %s AccessManagement object: %w", kcmv1.AccessManagementName, err)
	}

	if err := r.Client.Create(ctx, amObj); err != nil {
		err = fmt.Errorf("failed to create %s AccessManagement object: %w", kcmv1.AccessManagementName, err)
		r.warnf(mgmt, "AccessManagementCreateFailed", err.Error())
		return err
	}

	l.Info("Successfully created AccessManagement object")
	r.eventf(mgmt, "AccessManagementCreated", "Created %s AccessManagement object", kcmv1.AccessManagementName)

	return nil
}

// checkProviderStatus checks the status of a provider associated with a given
// ProviderTemplate name. Since there's no way to determine resource Kind from
// the given template iterate over all possible provider types.
func (r *ManagementReconciler) checkProviderStatus(ctx context.Context, component component) error {
	helmReleaseName := component.helmReleaseName
	hr := &helmcontrollerv2.HelmRelease{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: helmReleaseName}, hr); err != nil {
		return fmt.Errorf("failed to check provider status: %w", err)
	}

	hrReadyCondition := fluxconditions.Get(hr, fluxmeta.ReadyCondition)
	if hrReadyCondition == nil || hrReadyCondition.ObservedGeneration != hr.Generation {
		return fmt.Errorf("HelmRelease %s/%s Ready condition is not updated yet", r.SystemNamespace, helmReleaseName)
	}
	if hr.Status.ObservedGeneration != hr.Generation {
		return fmt.Errorf("HelmRelease %s/%s has not observed new values yet", r.SystemNamespace, helmReleaseName)
	}
	if !fluxconditions.IsReady(hr) {
		return fmt.Errorf("HelmRelease %s/%s is not yet ready: %s", r.SystemNamespace, helmReleaseName, hrReadyCondition.Message)
	}

	// mostly for sanity check
	latestSnapshot := hr.Status.History.Latest()
	if latestSnapshot == nil {
		return fmt.Errorf("HelmRelease %s/%s has empty deployment history in the status", r.SystemNamespace, helmReleaseName)
	}
	if latestSnapshot.Status != helmreleasepkg.StatusDeployed.String() {
		return fmt.Errorf("HelmRelease %s/%s is not yet deployed, actual status is %s", r.SystemNamespace, helmReleaseName, latestSnapshot.Status)
	}
	if latestSnapshot.ConfigDigest != hr.Status.LastAttemptedConfigDigest {
		return fmt.Errorf("HelmRelease %s/%s is not yet reconciled the latest values", r.SystemNamespace, helmReleaseName)
	}

	if !component.isCAPIProvider {
		return nil
	}

	type genericProviderList interface {
		client.ObjectList
		capioperatorv1.GenericProviderList
	}

	var (
		errs error

		ldebug = ctrl.LoggerFrom(ctx).V(1)
	)
	for _, gpl := range []genericProviderList{
		&capioperatorv1.CoreProviderList{},
		&capioperatorv1.InfrastructureProviderList{},
		&capioperatorv1.BootstrapProviderList{},
		&capioperatorv1.ControlPlaneProviderList{},
		&capioperatorv1.IPAMProviderList{},
	} {
		if err := r.Client.List(ctx, gpl, client.MatchingLabels{kcmv1.FluxHelmChartNameKey: hr.Status.History.Latest().Name}); meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			ldebug.Info("capi operator providers are not found", "list_type", fmt.Sprintf("%T", gpl))
			continue
		} else if err != nil {
			return fmt.Errorf("failed to list providers: %w", err)
		}

		items := gpl.GetItems()
		if len(items) == 0 { // sanity
			continue
		}

		if err := checkProviderReadiness(items); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	return errs
}

func checkProviderReadiness(items []capioperatorv1.GenericProvider) error {
	var errMessages []string
	for _, gp := range items {
		if gp.GetGeneration() != gp.GetStatus().ObservedGeneration {
			errMessages = append(errMessages, "status is not updated yet")
			continue
		}
		if gp.GetSpec().Version != "" && (gp.GetStatus().InstalledVersion != nil && gp.GetSpec().Version != *gp.GetStatus().InstalledVersion) {
			errMessages = append(errMessages, fmt.Sprintf("expected version %s, actual %s", gp.GetSpec().Version, *gp.GetStatus().InstalledVersion))
			continue
		}
		if !isProviderReady(gp) {
			errMessages = append(errMessages, getFalseConditions(gp)...)
		}
	}
	if len(errMessages) == 0 {
		return nil
	}
	return fmt.Errorf("%s is not yet ready: %s", items[0].GetObjectKind().GroupVersionKind().Kind, strings.Join(errMessages, ", "))
}

func isProviderReady(gp capioperatorv1.GenericProvider) bool {
	return slices.ContainsFunc(gp.GetStatus().Conditions, func(c clusterapiv1.Condition) bool {
		return c.Type == clusterapiv1.ReadyCondition && c.Status == corev1.ConditionTrue
	})
}

func getFalseConditions(gp capioperatorv1.GenericProvider) []string {
	conditions := gp.GetStatus().Conditions
	messages := make([]string, 0, len(conditions))
	for _, cond := range conditions {
		if cond.Status == corev1.ConditionTrue {
			continue
		}
		msg := fmt.Sprintf("condition %s is in status %s", cond.Type, cond.Status)
		if cond.Message != "" {
			msg += ": " + cond.Message
		}
		messages = append(messages, msg)
	}
	return messages
}

func (r *ManagementReconciler) delete(ctx context.Context, management *kcmv1.Management) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	if r.IsDisabledValidationWH {
		clusterDeployments := new(kcmv1.ClusterDeploymentList)
		if err := r.Client.List(ctx, clusterDeployments, client.Limit(1)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to list ClusterDeployments: %w", err)
		}

		if len(clusterDeployments.Items) > 0 {
			return ctrl.Result{}, errors.New("the Management object can't be removed if ClusterDeployment objects still exist")
		}
	}

	listOpts := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue}),
	}
	r.eventf(management, "RemovingManagement", "Removing KCM management components")

	requeue, err := r.removeHelmReleases(ctx, kcmv1.CoreKCMName, listOpts)
	if err != nil || requeue {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, err
	}
	requeue, err = r.removeHelmCharts(ctx, listOpts)
	if err != nil || requeue {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, err
	}
	requeue, err = r.removeHelmRepositories(ctx, listOpts)
	if err != nil || requeue {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, err
	}

	r.eventf(management, "RemovedManagement", "All KCM management components were removed")

	// Removing finalizer in the end of cleanup
	l.Info("Removing Management finalizer")
	if controllerutil.RemoveFinalizer(management, kcmv1.ManagementFinalizer) {
		return ctrl.Result{}, r.Client.Update(ctx, management)
	}
	return ctrl.Result{}, nil
}

func (r *ManagementReconciler) removeHelmReleases(ctx context.Context, kcmReleaseName string, opts *client.ListOptions) (requeue bool, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Suspending KCM Helm Release reconciles")
	kcmRelease := &helmcontrollerv2.HelmRelease{}
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: kcmReleaseName}, kcmRelease)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	if err == nil && !kcmRelease.Spec.Suspend {
		kcmRelease.Spec.Suspend = true
		if err := r.Client.Update(ctx, kcmRelease); err != nil {
			return false, err
		}
	}
	l.Info("Ensuring all HelmReleases owned by KCM are removed")
	gvk := helmcontrollerv2.GroupVersion.WithKind(helmcontrollerv2.HelmReleaseKind)
	if err := utils.EnsureDeleteAllOf(ctx, r.Client, gvk, opts); err != nil {
		l.Error(err, "Not all HelmReleases owned by KCM are removed")
		return true, err
	}
	return false, nil
}

func (r *ManagementReconciler) removeHelmCharts(ctx context.Context, opts *client.ListOptions) (requeue bool, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring all HelmCharts owned by KCM are removed")
	gvk := sourcev1.GroupVersion.WithKind(sourcev1.HelmChartKind)
	if err := utils.EnsureDeleteAllOf(ctx, r.Client, gvk, opts); err != nil {
		l.Error(err, "Not all HelmCharts owned by KCM are removed")
		return true, err
	}
	return false, nil
}

func (r *ManagementReconciler) removeHelmRepositories(ctx context.Context, opts *client.ListOptions) (requeue bool, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring all HelmRepositories owned by KCM are removed")
	gvk := sourcev1.GroupVersion.WithKind(sourcev1.HelmRepositoryKind)
	if err := utils.EnsureDeleteAllOf(ctx, r.Client, gvk, opts); err != nil {
		l.Error(err, "Not all HelmRepositories owned by KCM are removed")
		return true, err
	}
	return false, nil
}

type component struct {
	kcmv1.Component

	helmReleaseName string
	targetNamespace string
	installSettings *helmcontrollerv2.Install
	// helm release dependencies
	dependsOn      []fluxmeta.NamespacedObjectReference
	isCAPIProvider bool
}

func (r *ManagementReconciler) getComponentValues(ctx context.Context, name string, config *apiextv1.JSON) (*apiextv1.JSON, error) {
	l := ctrl.LoggerFrom(ctx)

	currentValues := chartutil.Values{}
	if config != nil && config.Raw != nil {
		if err := json.Unmarshal(config.Raw, &currentValues); err != nil {
			return nil, err
		}
	}

	componentValues := chartutil.Values{}

	switch name {
	case kcmv1.CoreKCMName:
		// Those are only needed for the initial installation
		componentValues = map[string]any{
			"controller": map[string]any{
				"createManagement":       false,
				"createAccessManagement": false,
				"createRelease":          false,
			},
		}

		capiOperatorValues := make(map[string]any)
		if r.Config != nil {
			if err := certmanager.VerifyAPI(ctx, r.Config, r.SystemNamespace); err != nil {
				return nil, fmt.Errorf("failed to check if cert-manager API is installed: %w", err)
			}
			l.Info("Cert manager is installed, enabling additional components")
			componentValues["admissionWebhook"] = map[string]any{"enabled": true}
			componentValues["velero"] = map[string]any{"enabled": true}
			capiOperatorValues = map[string]any{"enabled": true}
		}

		if r.RegistryCertSecretName != "" {
			v := make(map[string]any)
			if currentValues != nil {
				if raw, ok := currentValues["cluster-api-operator"]; ok {
					var castOk bool
					if v, castOk = raw.(map[string]any); !castOk {
						return nil, fmt.Errorf("failed to cast 'cluster-api-operator' (type %T) to map[string]any", raw)
					}
				}
			}

			capiOperatorValues = chartutil.CoalesceTables(capiOperatorValues, processCAPIOperatorCertVolumeMounts(v, r.RegistryCertSecretName))
		}
		componentValues["cluster-api-operator"] = capiOperatorValues

	case kcmv1.ProviderSveltosName:
		componentValues = map[string]any{
			"projectsveltos": map[string]any{
				"registerMgmtClusterJob": map[string]any{
					"registerMgmtCluster": map[string]any{
						"args": []string{
							"--labels=" + kcmv1.K0rdentManagementClusterLabelKey + "=" + kcmv1.K0rdentManagementClusterLabelValue,
						},
					},
				},
			},
		}
	}

	if r.GlobalRegistry != "" {
		globalValues := map[string]any{
			"global": map[string]any{
				"registry": r.GlobalRegistry,
			},
		}
		componentValues = chartutil.CoalesceTables(componentValues, globalValues)
	}

	var merged chartutil.Values
	// for projectsveltos, we want new values to override values provided in Management spec
	if name == kcmv1.ProviderSveltosName {
		merged = chartutil.CoalesceTables(componentValues, currentValues)
	} else {
		merged = chartutil.CoalesceTables(currentValues, componentValues)
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal values for %s component: %w", name, err)
	}
	return &apiextv1.JSON{Raw: raw}, nil
}

func (r *ManagementReconciler) getWrappedComponents(ctx context.Context, mgmt *kcmv1.Management, release *kcmv1.Release) ([]component, error) {
	components := make([]component, 0, len(mgmt.Spec.Providers)+2)

	kcmComponent := kcmv1.Component{}
	capiComponent := kcmv1.Component{}
	if mgmt.Spec.Core != nil {
		kcmComponent = mgmt.Spec.Core.KCM
		capiComponent = mgmt.Spec.Core.CAPI
	}

	kcmComp := component{Component: kcmComponent, helmReleaseName: kcmv1.CoreKCMName}
	if kcmComp.Template == "" {
		kcmComp.Template = release.Spec.KCM.Template
	}
	kcmConfig, err := r.getComponentValues(ctx, kcmv1.CoreKCMName, kcmComp.Config)
	if err != nil {
		return nil, err
	}
	kcmComp.Config = kcmConfig
	components = append(components, kcmComp)

	capiComp := component{
		Component: capiComponent,
		installSettings: &helmcontrollerv2.Install{
			Remediation: &helmcontrollerv2.InstallRemediation{
				Retries:              1,
				RemediateLastFailure: pointer.To(true),
			},
		},
		helmReleaseName: kcmv1.CoreCAPIName,
		dependsOn:       []fluxmeta.NamespacedObjectReference{{Name: kcmv1.CoreKCMName}},
		isCAPIProvider:  true,
	}
	if capiComp.Template == "" {
		capiComp.Template = release.Spec.CAPI.Template
	}

	capiConfig, err := r.getComponentValues(ctx, kcmv1.CoreCAPIName, capiComp.Config)
	if err != nil {
		return nil, err
	}
	capiComp.Config = capiConfig

	components = append(components, capiComp)

	const sveltosTargetNamespace = "projectsveltos"

	for _, p := range mgmt.Spec.Providers {
		c := component{
			Component: p.Component, helmReleaseName: p.Name,
			dependsOn: []fluxmeta.NamespacedObjectReference{{Name: kcmv1.CoreCAPIName}}, isCAPIProvider: true,
		}
		// Try to find corresponding provider in the Release object
		if c.Template == "" {
			c.Template = release.ProviderTemplate(p.Name)
		}

		if p.Name == kcmv1.ProviderSveltosName {
			c.isCAPIProvider = false
			c.targetNamespace = sveltosTargetNamespace
			c.installSettings = &helmcontrollerv2.Install{
				CreateNamespace: true,
			}
		}

		config, err := r.getComponentValues(ctx, p.Name, c.Config)
		if err != nil {
			return nil, err
		}
		c.Config = config

		components = append(components, c)
	}

	return components, nil
}

func processCAPIOperatorCertVolumeMounts(capiOperatorValues map[string]any, registryCertSecret string) map[string]any {
	// explicitly add the webhook service cert volume to ensure it's present,
	// since helm does not merge custom array values with the default ones
	webhookCertVolume := map[string]any{
		"name": "cert",
		"secret": map[string]any{
			"defaultMode": 420,
			"secretName":  "capi-operator-webhook-service-cert",
		},
	}
	volumeName := "registry-cert"
	registryCertVolume := map[string]any{
		"name": volumeName,
		"secret": map[string]any{
			"defaultMode": 420,
			"secretName":  registryCertSecret,
			"items": []any{
				map[string]any{
					"key":  "ca.crt",
					"path": "registry-ca.pem",
				},
			},
		},
	}

	if capiOperatorValues == nil {
		capiOperatorValues = make(map[string]any)
	}
	certVolumes := []any{webhookCertVolume, registryCertVolume}
	if existing, ok := capiOperatorValues["volumes"].([]any); ok {
		capiOperatorValues["volumes"] = append(existing, certVolumes...)
	} else {
		capiOperatorValues["volumes"] = certVolumes
	}

	// explicitly add the webhook service cert volume mount to ensure it's present,
	// since helm does not merge custom array values with the default ones
	webhookCertMount := map[string]any{
		"mountPath": "/tmp/k8s-webhook-server/serving-certs",
		"name":      "cert",
	}
	registryCertMount := map[string]any{
		"mountPath": "/etc/ssl/certs/registry-ca.pem",
		"name":      volumeName,
		"subPath":   "registry-ca.pem",
	}
	managerMounts := []any{webhookCertMount, registryCertMount}

	vmRaw, ok := capiOperatorValues["volumeMounts"].(map[string]any)
	if !ok {
		vmRaw = make(map[string]any)
	}
	if mgr, ok := vmRaw["manager"].([]any); ok {
		vmRaw["manager"] = append(mgr, managerMounts...)
	} else {
		vmRaw["manager"] = managerMounts
	}
	capiOperatorValues["volumeMounts"] = vmRaw

	return capiOperatorValues
}

func (r *ManagementReconciler) ensureUpgradeBackup(ctx context.Context, mgmt *kcmv1.Management) (requeue bool, _ error) {
	if mgmt.Status.Release == "" {
		return false, nil
	}
	if mgmt.Spec.Release == mgmt.Status.Release {
		return false, nil
	}

	// check if velero is enabled but with real objects
	deploys := new(appsv1.DeploymentList)
	if err := r.Client.List(ctx, deploys,
		client.MatchingLabels{"component": "velero"},
		client.Limit(1)); err != nil {
		return false, fmt.Errorf("failed to list Deployments to find velero: %w", err)
	}

	if len(deploys.Items) == 0 {
		return false, nil // velero is not enabled, nothing to do
	}

	autoUpgradeBackups := new(kcmv1.ManagementBackupList)
	if err := r.Client.List(ctx, autoUpgradeBackups, client.MatchingFields{kcmv1.ManagementBackupAutoUpgradeIndexKey: "true"}); err != nil {
		return false, fmt.Errorf("failed to list ManagementBackup with schedule set: %w", err)
	}

	if len(autoUpgradeBackups.Items) == 0 {
		return false, nil // no autoupgrades, nothing to do
	}

	singleName2Location := make(map[string]string, len(autoUpgradeBackups.Items))
	for _, v := range autoUpgradeBackups.Items {
		// TODO: check for name length?
		singleName2Location[v.Name+"-"+mgmt.Status.Release] = v.Spec.StorageLocation
	}

	requeue = false
	for name, location := range singleName2Location {
		mb := new(kcmv1.ManagementBackup)
		err := r.Client.Get(ctx, client.ObjectKey{Name: name}, mb)
		isNotFoundErr := apierrors.IsNotFound(err)
		if err != nil && !isNotFoundErr {
			return false, fmt.Errorf("failed to get ManagementBackup %s: %w", name, err)
		}

		// have to create
		if isNotFoundErr {
			mb = &kcmv1.ManagementBackup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: kcmv1.GroupVersion.String(),
					Kind:       "ManagementBackup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
					// TODO: generilize the label?
					Labels: map[string]string{"k0rdent.mirantis.com/release-backup": mgmt.Status.Release},
				},
				Spec: kcmv1.ManagementBackupSpec{
					StorageLocation: location,
				},
			}

			if err := r.Client.Create(ctx, mb); err != nil {
				return false, fmt.Errorf("failed to create a single ManagementBackup %s: %w", name, err)
			}
			r.eventf(mgmt, "CreatedManagementBackup", "created ManagementBackup %s", mb.Name)

			// a fresh backup is not completed, so the next statement will set requeue
		}

		//
		if !mb.IsCompleted() {
			requeue = true // let us continue with creation of others if any, then requeue
			continue
		}
	}

	return requeue, nil
}

func (r *ManagementReconciler) updateStatus(ctx context.Context, mgmt *kcmv1.Management) error {
	if err := r.Client.Status().Update(ctx, mgmt); err != nil {
		return fmt.Errorf("failed to update status for Management %s: %w", mgmt.Name, err)
	}
	return nil
}

type mgmtStatusAccumulator struct {
	components             map[string]kcmv1.ComponentStatus
	compatibilityContracts map[string]kcmv1.CompatibilityContracts
	providers              kcmv1.Providers
}

func updateComponentsStatus(
	stAcc *mgmtStatusAccumulator,
	comp component,
	template *kcmv1.ProviderTemplate,
	err string,
) {
	if stAcc == nil {
		return
	}

	componentStatus := kcmv1.ComponentStatus{
		Error:    err,
		Success:  err == "",
		Template: comp.Template,
	}

	if template != nil {
		componentStatus.ExposedProviders = template.Status.Providers
		if err == "" {
			stAcc.providers = append(stAcc.providers, template.Status.Providers...)
			slices.Sort(stAcc.providers)
			stAcc.providers = slices.Compact(stAcc.providers)
			for _, v := range template.Status.Providers {
				stAcc.compatibilityContracts[v] = template.Status.CAPIContracts
			}
		}
	}
	stAcc.components[comp.helmReleaseName] = componentStatus
}

// setReadyCondition updates the Management resource's "Ready" condition based on whether
// all components are healthy.
func (r *ManagementReconciler) setReadyCondition(management *kcmv1.Management) {
	var failing []string
	for name, comp := range management.Status.Components {
		if !comp.Success {
			failing = append(failing, name)
		}
	}

	readyCond := metav1.Condition{
		Type:               kcmv1.ReadyCondition,
		ObservedGeneration: management.Generation,
		Status:             metav1.ConditionTrue,
		Reason:             kcmv1.AllComponentsHealthyReason,
		Message:            "All components are successfully installed",
	}
	sort.Strings(failing)
	if len(failing) > 0 {
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = kcmv1.NotAllComponentsHealthyReason
		readyCond.Message = fmt.Sprintf("Components not ready: %v", failing)
	}
	if meta.SetStatusCondition(&management.Status.Conditions, readyCond) && readyCond.Status == metav1.ConditionTrue {
		r.eventf(management, "ManagementIsReady", "Management KCM components are ready")
	}
}

func (r *ManagementReconciler) getRelease(ctx context.Context, mgmt *kcmv1.Management) (release *kcmv1.Release, _ error) {
	release = new(kcmv1.Release)
	return release, r.Client.Get(ctx, client.ObjectKey{Name: mgmt.Spec.Release}, release)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagementReconciler) SetupWithManager(mgr ctrl.Manager) error {
	dc, err := dynamic.NewForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	r.Manager = mgr
	r.Client = mgr.GetClient()
	r.Config = mgr.GetConfig()
	r.DynamicClient = dc

	r.defaultRequeueTime = 10 * time.Second

	managedController := ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcmv1.Management{})

	if r.IsDisabledValidationWH {
		setupLog := mgr.GetLogger().WithName("management_ctrl_setup")

		managedController.Watches(&kcmv1.Release{}, handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []ctrl.Request {
			return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: kcmv1.ManagementName}}}
		}), builder.WithPredicates(predicate.Funcs{
			GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
			UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
				ro, ok := tue.ObjectOld.(*kcmv1.Release)
				if !ok {
					return false
				}

				rn, ok := tue.ObjectNew.(*kcmv1.Release)
				if !ok {
					return false
				}

				return ro.Status.Ready != rn.Status.Ready // any change in readiness must trigger event
			},
		}))
		setupLog.Info("Validations are disabled, watcher for Release objects is set")

		managedController.Watches(&kcmv1.ProviderTemplate{}, handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []ctrl.Request {
			return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: kcmv1.ManagementName}}}
		}), builder.WithPredicates(predicate.Funcs{
			GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
			DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
			UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
				pto, ok := tue.ObjectOld.(*kcmv1.ProviderTemplate)
				if !ok {
					return false
				}

				ptn, ok := tue.ObjectNew.(*kcmv1.ProviderTemplate)
				if !ok {
					return false
				}

				return ptn.Status.Valid && !pto.Status.Valid
			},
		}))
		setupLog.Info("Validations are disabled, watcher for ProviderTemplate objects is set")
	}

	return managedController.Complete(r)
}

func (*ManagementReconciler) eventf(mgmt *kcmv1.Management, reason, message string, args ...any) {
	record.Eventf(mgmt, mgmt.Generation, reason, message, args...)
}

func (*ManagementReconciler) warnf(mgmt *kcmv1.Management, reason, message string, args ...any) {
	record.Warnf(mgmt, mgmt.Generation, reason, message, args...)
}
