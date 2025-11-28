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
	"sort"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
	"github.com/K0rdent/kcm/internal/registry"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	labelsutil "github.com/K0rdent/kcm/internal/util/labels"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
)

// ManagementReconciler reconciles a Management object
type ManagementReconciler struct {
	Client                        client.Client
	Manager                       manager.Manager
	Config                        *rest.Config
	DynamicClient                 *dynamic.DynamicClient
	SystemNamespace               string
	GlobalRegistry                string
	GlobalK0sURL                  string
	K0sURLCertSecretName          string // Name of a Secret with K0s Download URL Root CA with ca.crt key; to be passed to the ClusterDeploymentReconciler
	RegistryCertSecretName        string // Name of a Secret with Registry Root CA with ca.crt key; used by ManagementReconciler and ClusterDeploymentReconciler
	RegistryCredentialsSecretName string // Name of a Secret with Registry Credentials (username/password); used by ManagementReconciler

	DefaultHelmTimeout time.Duration
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

	if updated, err := labelsutil.AddKCMComponentLabel(ctx, r.Client, management); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{}, err
	}

	if changed, err := kubeutil.SetPredeclaredSecretsCondition(ctx, r.Client, management, record.Warnf, r.SystemNamespace, r.RegistryCertSecretName); err != nil { // if changed and NO error we will eventually update the status
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

	// Cleanup only management components (without `k0rdent.mirantis.com/region` label)
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      kcmv1.KCMRegionLabelKey,
				Operator: metav1.LabelSelectorOpDoesNotExist,
			},
		},
	}
	if err := components.Cleanup(ctx, r.Client, management, labelSelector, r.SystemNamespace); err != nil {
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

	if err := r.ensureStateManagementProvider(ctx, management); err != nil {
		r.warnf(management, "EnsureStateManagementProviderFailed", "failed to ensure StateManagementProvider is created: %v", err)
		l.Error(err, "failed to ensure StateManagementProvider is created")
	}

	// Handle registry credentials
	registryCredsSecretName, err := r.reconcileRegistryCredentials(ctx, management)
	if err != nil {
		r.warnf(management, "ReconcileRegistryCredentialsFailed", "failed to reconcile registry credentials: %v", err)
		l.Error(err, "failed to reconcile registry credentials")
		return ctrl.Result{}, err
	}

	opts := components.ReconcileComponentsOpts{
		DefaultHelmTimeout:            r.DefaultHelmTimeout,
		Namespace:                     r.SystemNamespace,
		GlobalRegistry:                r.GlobalRegistry,
		RegistryCertSecretName:        r.RegistryCertSecretName,
		RegistryCredentialsSecretName: registryCredsSecretName,
	}

	requeue, errs := components.Reconcile(ctx, r.Client, r.Client, management, r.Config, release, opts)

	management.Status.ObservedGeneration = management.Generation
	management.Status.Release = management.Spec.Release

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
	incompContracts, err := validationutil.GetIncompatibleContracts(ctx, r.Client, release, management)
	if len(incompContracts) == 0 && err == nil { // if NO error
		return true, nil
	}

	isNotFoundOrNotReady := errors.Is(err, validationutil.ErrProviderIsNotReady) || apierrors.IsNotFound(err)
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

	currentNamespace := kubeutil.CurrentNamespace()

	l.Info("Provider has been successfully installed, so setting up controller for ClusterDeployment")
	if err = (&ClusterDeploymentReconciler{
		SystemNamespace:        currentNamespace,
		IsDisabledValidationWH: r.IsDisabledValidationWH,
		GlobalRegistry:         r.GlobalRegistry,
		GlobalK0sURL:           r.GlobalK0sURL,
		K0sURLCertSecretName:   r.K0sURLCertSecretName,
		RegistryCertSecretName: r.RegistryCertSecretName,
		DefaultHelmTimeout:     r.DefaultHelmTimeout,
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

func (r *ManagementReconciler) ensureStateManagementProvider(ctx context.Context, mgmt *kcmv1.Management) error {
	l := ctrl.LoggerFrom(ctx).WithValues("provider_name", kcmv1.ProviderSveltosName)

	if _, defined := mgmt.Status.Components[kcmv1.ProviderSveltosName]; !defined {
		return nil
	}

	l.Info("Ensuring built-in StateManagementProvider for ProjectSveltos is created")
	currentNamespace := kubeutil.CurrentNamespace()
	currentKcmName := os.Getenv("KCM_NAME")

	const (
		appsAPIGroupVersion = "apps/v1"
		deploymentKind      = "Deployment"

		projectSveltosDeploymentName      = "addon-controller"
		projectSveltosDeploymentNamespace = "projectsveltos"
		profilesResource                  = "profiles"
		clustersummariesResource          = "clustersummaries"
	)
	var (
		projectSveltosAPIGroup   = addoncontrollerv1beta1.GroupVersion.Group
		projectSveltosAPIVersion = addoncontrollerv1beta1.GroupVersion.Version
	)

	stateManagementProvider := &kcmv1.StateManagementProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name: kubeutil.DefaultStateManagementProvider,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: kcmv1.GroupVersion.String(),
					Kind:       mgmt.Kind,
					Name:       mgmt.Name,
					UID:        mgmt.UID,
				},
			},
		},
		Spec: kcmv1.StateManagementProviderSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					kubeutil.DefaultStateManagementProviderSelectorKey: kubeutil.DefaultStateManagementProviderSelectorValue,
				},
			},
			Adapter: kcmv1.ResourceReference{
				APIVersion: appsAPIGroupVersion,
				Kind:       deploymentKind,
				Name:       currentKcmName,
				Namespace:  currentNamespace,
				ReadinessRule: `self.status.availableReplicas == self.status.replicas &&
self.status.availableReplicas == self.status.updatedReplicas &&
self.status.availableReplicas == self.status.readyReplicas`,
			},
			Provisioner: []kcmv1.ResourceReference{
				{
					APIVersion: appsAPIGroupVersion,
					Kind:       deploymentKind,
					Name:       projectSveltosDeploymentName,
					Namespace:  projectSveltosDeploymentNamespace,
					ReadinessRule: `self.status.availableReplicas == self.status.replicas &&
self.status.availableReplicas == self.status.updatedReplicas &&
self.status.availableReplicas == self.status.readyReplicas`,
				},
			},
			ProvisionerCRDs: []kcmv1.ProvisionerCRD{
				{
					Group:   projectSveltosAPIGroup,
					Version: projectSveltosAPIVersion,
					Resources: []string{
						profilesResource,
						clustersummariesResource,
					},
				},
			},
			Suspend: false,
		},
	}

	err := r.Client.Get(ctx, client.ObjectKeyFromObject(stateManagementProvider), stateManagementProvider)
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get %s StateManagementProvider object: %w", stateManagementProvider.GetName(), err)
	}
	if err == nil {
		return nil
	}

	if err = r.Client.Create(ctx, stateManagementProvider); err != nil {
		err = fmt.Errorf("failed to create %s StateManagementProvider object: %w", stateManagementProvider.GetName(), err)
		r.warnf(mgmt, "StateManagementProviderCreateFailed", err.Error())
		return err
	}

	l.Info("Successfully created StateManagementProvider object")
	r.eventf(mgmt, "StateManagementProviderCreated", "Created %s StateManagementProvider object", stateManagementProvider.GetName())
	return nil
}

func (r *ManagementReconciler) delete(ctx context.Context, management *kcmv1.Management) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	if r.IsDisabledValidationWH {
		err := validationutil.ManagementDeletionAllowed(ctx, r.Client)
		if err != nil {
			r.warnf(management, "ManagementDeletionFailed", err.Error())
			return ctrl.Result{}, err
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
	if err := kubeutil.EnsureDeleteAllOf(ctx, r.Client, gvk, opts); err != nil {
		l.Error(err, "Not all HelmReleases owned by KCM are removed")
		return true, err
	}
	return false, nil
}

func (r *ManagementReconciler) removeHelmCharts(ctx context.Context, opts *client.ListOptions) (requeue bool, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring all HelmCharts owned by KCM are removed")
	gvk := sourcev1.GroupVersion.WithKind(sourcev1.HelmChartKind)
	if err := kubeutil.EnsureDeleteAllOf(ctx, r.Client, gvk, opts); err != nil {
		l.Error(err, "Not all HelmCharts owned by KCM are removed")
		return true, err
	}
	return false, nil
}

func (r *ManagementReconciler) removeHelmRepositories(ctx context.Context, opts *client.ListOptions) (requeue bool, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring all HelmRepositories owned by KCM are removed")
	gvk := sourcev1.GroupVersion.WithKind(sourcev1.HelmRepositoryKind)
	if err := kubeutil.EnsureDeleteAllOf(ctx, r.Client, gvk, opts); err != nil {
		l.Error(err, "Not all HelmRepositories owned by KCM are removed")
		return true, err
	}
	return false, nil
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

// reconcileRegistryCredentials handles creation and distribution of registry credentials.
// Returns the name of the registry credentials secret, or empty string if no credentials are configured.
func (r *ManagementReconciler) reconcileRegistryCredentials(ctx context.Context, management *kcmv1.Management) (string, error) {
	l := ctrl.LoggerFrom(ctx).WithName("registry-credentials")

	// Check if registry credentials are configured
	var registryConfig *kcmv1.RegistryConfig
	if management.Spec.RegistryConfig != nil && management.Spec.RegistryConfig.Credentials != nil {
		registryConfig = management.Spec.RegistryConfig
	}

	// If no registry config in Management spec, check if credentials secret is provided via controller flag
	if registryConfig == nil && r.RegistryCredentialsSecretName != "" {
		l.V(1).Info("Using registry credentials from controller configuration", "secretName", r.RegistryCredentialsSecretName)
		return r.RegistryCredentialsSecretName, nil
	}

	// No credentials configured
	if registryConfig == nil || registryConfig.Credentials == nil {
		l.V(1).Info("No registry credentials configured")
		return "", nil
	}

	registryURL := registryConfig.Registry
	if registryURL == "" {
		registryURL = r.GlobalRegistry
	}

	if registryURL == "" {
		l.V(1).Info("No registry URL configured, skipping registry credential reconciliation")
		return "", nil
	}

	secretManager := registry.NewSecretManager(r.Client)

	// Get credentials from spec
	username, password, err := secretManager.GetCredentialsFromSpec(ctx, registryConfig.Credentials, r.SystemNamespace)
	if err != nil {
		return "", fmt.Errorf("failed to get registry credentials: %w", err)
	}

	// Generate secret name
	secretName := registry.GenerateSecretName(registryURL)

	// Create or update the dockerconfigjson secret in the system namespace
	err = secretManager.CreateOrUpdateDockerConfigSecret(
		ctx,
		secretName,
		r.SystemNamespace,
		registryURL,
		username,
		password,
		management,
		map[string]string{
			kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
		},
	)
	if err != nil {
		return "", fmt.Errorf("failed to create/update registry secret: %w", err)
	}

	l.Info("Successfully reconciled registry credentials", "secretName", secretName)
	r.eventf(management, "RegistryCredentialsReconciled", "Registry credentials secret %s/%s created/updated", r.SystemNamespace, secretName)

	// Propagate the secret to other namespaces
	propagator := registry.NewPropagator(r.Client)
	targetNamespaces, err := propagator.GetNamespacesRequiringCredentials(ctx, r.SystemNamespace)
	if err != nil {
		l.Error(err, "Failed to get namespaces requiring credentials, will retry")
		// Don't fail the reconciliation, just log the error
	} else {
		err = propagator.PropagateSecret(ctx, r.SystemNamespace, secretName, targetNamespaces, management)
		if err != nil {
			l.Error(err, "Failed to propagate registry secret to all namespaces")
			// Don't fail the reconciliation, components in system namespace will still work
		}
	}

	return secretName, nil
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
			RateLimiter: ratelimitutil.DefaultFastSlow(),
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
