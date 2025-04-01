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

	fluxv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	fluxconditions "github.com/fluxcd/pkg/runtime/conditions"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"helm.sh/helm/v3/pkg/chartutil"
	helmreleasepkg "helm.sh/helm/v3/pkg/release"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	capioperatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha2"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/certmanager"
	"github.com/K0rdent/kcm/internal/helm"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
)

// ManagementReconciler reconciles a Management object
type ManagementReconciler struct {
	Client          client.Client
	Manager         manager.Manager
	Config          *rest.Config
	DynamicClient   *dynamic.DynamicClient
	SystemNamespace string

	defaultRequeueTime time.Duration

	CreateAccessManagement bool

	sveltosDependentControllersStarted bool
}

func (r *ManagementReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling Management")

	management := &kcm.Management{}
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
		return r.Delete(ctx, management)
	}

	return r.Update(ctx, management)
}

func (r *ManagementReconciler) Update(ctx context.Context, management *kcm.Management) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	if controllerutil.AddFinalizer(management, kcm.ManagementFinalizer) {
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

	if err := r.cleanupRemovedComponents(ctx, management); err != nil {
		l.Error(err, "failed to cleanup removed components")
		return ctrl.Result{}, err
	}

	requeueAutoUpgradeBackups, err := r.ensureUpgradeBackup(ctx, management)
	if err != nil {
		l.Error(err, "failed to ensure release backups before upgrades")
		return ctrl.Result{}, err
	}
	if requeueAutoUpgradeBackups {
		const requeueAfter = 1 * time.Minute
		l.Info("Still creating or waiting for backups to be completed before the upgrade", "current_release", management.Status.Release, "new_release", management.Spec.Release, "requeue_after", requeueAfter)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	if err := r.ensureAccessManagement(ctx, management); err != nil {
		l.Error(err, "failed to ensure AccessManagement is created")
		return ctrl.Result{}, err
	}

	if err := r.enableAdditionalComponents(ctx, management); err != nil { // TODO (zerospiel): i wonder, do we need to reflect these changes and changes from the `wrappedComponents` in the spec?
		l.Error(err, "failed to enable additional KCM components")
		return ctrl.Result{}, err
	}

	components, err := getWrappedComponents(ctx, r.Client, management)
	if err != nil {
		l.Error(err, "failed to wrap KCM components")
		return ctrl.Result{}, err
	}

	var (
		errs error

		statusAccumulator = &mgmtStatusAccumulator{
			providers:              kcm.Providers{"infrastructure-internal"},
			components:             make(map[string]kcm.ComponentStatus),
			compatibilityContracts: make(map[string]kcm.CompatibilityContracts),
		}

		requeue bool
	)

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
		template := new(kcm.ProviderTemplate)
		if err := r.Client.Get(ctx, client.ObjectKey{Name: component.Template}, template); err != nil {
			errMsg := fmt.Sprintf("Failed to get ProviderTemplate %s: %s", component.Template, err)
			updateComponentsStatus(statusAccumulator, component, nil, errMsg)
			errs = errors.Join(errs, errors.New(errMsg))

			continue
		}

		if !template.Status.Valid {
			errMsg := fmt.Sprintf("Template %s is not marked as valid", component.Template)
			updateComponentsStatus(statusAccumulator, component, nil, errMsg)
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

		if _, _, err := helm.ReconcileHelmRelease(ctx, r.Client, component.helmReleaseName, r.SystemNamespace, hrReconcileOpts); err != nil {
			errMsg := fmt.Sprintf("Failed to reconcile HelmRelease %s/%s: %s", r.SystemNamespace, component.helmReleaseName, err)
			updateComponentsStatus(statusAccumulator, component, nil, errMsg)
			errs = errors.Join(errs, errors.New(errMsg))

			continue
		}

		if err := r.checkProviderStatus(ctx, component); err != nil {
			l.Info("Provider is not yet ready", "template", component.Template, "err", err)
			requeue = true
			updateComponentsStatus(statusAccumulator, component, nil, err.Error())
			continue
		}

		updateComponentsStatus(statusAccumulator, component, template, "")
	}

	management.Status.AvailableProviders = statusAccumulator.providers
	management.Status.CAPIContracts = statusAccumulator.compatibilityContracts
	management.Status.Components = statusAccumulator.components
	management.Status.ObservedGeneration = management.Generation
	management.Status.Release = management.Spec.Release

	shouldRequeue, err := r.startDependentControllers(ctx, management)
	if err != nil {
		return ctrl.Result{}, err
	}
	if shouldRequeue {
		requeue = true
	}

	setReadyCondition(management)

	if err := r.Client.Status().Update(ctx, management); err != nil {
		errs = errors.Join(errs, fmt.Errorf("failed to update status for Management %s: %w", management.Name, err))
	}

	if errs != nil {
		l.Error(errs, "Multiple errors during Management reconciliation")
		return ctrl.Result{}, errs
	}
	if requeue {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	return ctrl.Result{}, nil
}

// startDependentControllers starts controllers that cannot be started
// at process startup because of some dependency like CRDs being present.
func (r *ManagementReconciler) startDependentControllers(ctx context.Context, management *kcm.Management) (requue bool, err error) {
	if r.sveltosDependentControllersStarted {
		// Only need to start controllers once.
		return false, nil
	}

	l := ctrl.LoggerFrom(ctx).WithValues("provider_name", kcm.ProviderSveltosName)
	if !management.Status.Components[kcm.ProviderSveltosName].Success {
		l.Info("Waiting for provider to be ready to setup contollers dependent on it")
		return true, nil
	}

	currentNamespace := utils.CurrentNamespace()

	l.Info("Provider has been successfully installed, so setting up controller for ClusterDeployment")
	if err = (&ClusterDeploymentReconciler{
		DynamicClient:   r.DynamicClient,
		SystemNamespace: currentNamespace,
	}).SetupWithManager(r.Manager); err != nil {
		return false, fmt.Errorf("failed to setup controller for ClusterDeployment: %w", err)
	}
	l.Info("Setup for ClusterDeployment controller successful")

	l.Info("Provider has been successfully installed, so setting up controller for MultiClusterService")
	if err = (&MultiClusterServiceReconciler{
		SystemNamespace: currentNamespace,
	}).SetupWithManager(r.Manager); err != nil {
		return false, fmt.Errorf("failed to setup controller for MultiClusterService: %w", err)
	}
	l.Info("Setup for MultiClusterService controller successful")

	r.sveltosDependentControllersStarted = true
	return false, nil
}

func (r *ManagementReconciler) cleanupRemovedComponents(ctx context.Context, management *kcm.Management) error {
	var (
		errs error
		l    = ctrl.LoggerFrom(ctx)
	)

	managedHelmReleases := new(fluxv2.HelmReleaseList)
	if err := r.Client.List(ctx, managedHelmReleases,
		client.MatchingLabels{kcm.KCMManagedLabelKey: kcm.KCMManagedLabelValue},
		client.InNamespace(r.SystemNamespace), // all helmreleases are being installed only in the system namespace
	); err != nil {
		return fmt.Errorf("failed to list %s: %w", fluxv2.GroupVersion.WithKind(fluxv2.HelmReleaseKind), err)
	}

	releasesList := &metav1.PartialObjectMetadataList{}
	if len(managedHelmReleases.Items) > 0 {
		releasesList.SetGroupVersionKind(kcm.GroupVersion.WithKind(kcm.ReleaseKind))
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

		if componentName == kcm.CoreCAPIName ||
			componentName == kcm.CoreKCMName ||
			slices.ContainsFunc(releasesList.Items, func(r metav1.PartialObjectMetadata) bool {
				return componentName == utils.TemplatesChartFromReleaseName(r.Name)
			}) ||
			slices.ContainsFunc(management.Spec.Providers, func(newComp kcm.Provider) bool { return componentName == newComp.Name }) {
			continue
		}

		l.Info("Found component to remove", "component_name", componentName)

		if err := r.Client.Delete(ctx, &hr); client.IgnoreNotFound(err) != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to delete %s: %w", client.ObjectKeyFromObject(&hr), err))
			continue
		}
		l.Info("Removed HelmRelease", "reference", client.ObjectKeyFromObject(&hr).String())
	}

	return errs
}

func (r *ManagementReconciler) ensureAccessManagement(ctx context.Context, mgmt *kcm.Management) error {
	if !r.CreateAccessManagement {
		return nil
	}

	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring AccessManagement is created")

	amObj := &kcm.AccessManagement{
		ObjectMeta: metav1.ObjectMeta{
			Name: kcm.AccessManagementName,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: kcm.GroupVersion.String(),
					Kind:       mgmt.Kind,
					Name:       mgmt.Name,
					UID:        mgmt.UID,
				},
			},
		},
	}

	err := r.Client.Get(ctx, client.ObjectKey{Name: kcm.AccessManagementName}, amObj)

	if err == nil {
		return nil
	}

	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get %s AccessManagement object: %w", kcm.AccessManagementName, err)
	}

	if err := r.Client.Create(ctx, amObj); err != nil {
		return fmt.Errorf("failed to create %s AccessManagement object: %w", kcm.AccessManagementName, err)
	}

	l.Info("Successfully created AccessManagement object")

	return nil
}

// checkProviderStatus checks the status of a provider associated with a given
// ProviderTemplate name. Since there's no way to determine resource Kind from
// the given template iterate over all possible provider types.
func (r *ManagementReconciler) checkProviderStatus(ctx context.Context, component component) error {
	helmReleaseName := component.helmReleaseName
	hr := &fluxv2.HelmRelease{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: r.SystemNamespace, Name: helmReleaseName}, hr); err != nil {
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
		errs          error
		providerFound bool

		ldebug = ctrl.LoggerFrom(ctx).V(1)
	)
	for _, gpl := range []genericProviderList{
		&capioperatorv1.CoreProviderList{},
		&capioperatorv1.InfrastructureProviderList{},
		&capioperatorv1.BootstrapProviderList{},
		&capioperatorv1.ControlPlaneProviderList{},
	} {
		if err := r.Client.List(ctx, gpl, client.MatchingLabels{kcm.FluxHelmChartNameKey: hr.Status.History.Latest().Name}); meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			ldebug.Info("capi operator providers are not found", "list_type", fmt.Sprintf("%T", gpl))
			continue
		} else if err != nil {
			return fmt.Errorf("failed to list providers: %w", err)
		}

		items := gpl.GetItems()
		if len(items) == 0 { // sanity
			continue
		}

		providerFound = true

		if err := checkProviderReadiness(items); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	if !providerFound {
		return errors.New("waiting for Cluster API Provider objects to be created")
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

func (r *ManagementReconciler) Delete(ctx context.Context, management *kcm.Management) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	listOpts := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{kcm.KCMManagedLabelKey: kcm.KCMManagedLabelValue}),
	}
	requeue, err := r.removeHelmReleases(ctx, kcm.CoreKCMName, listOpts)
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

	// Removing finalizer in the end of cleanup
	l.Info("Removing Management finalizer")
	if controllerutil.RemoveFinalizer(management, kcm.ManagementFinalizer) {
		return ctrl.Result{}, r.Client.Update(ctx, management)
	}
	return ctrl.Result{}, nil
}

func (r *ManagementReconciler) removeHelmReleases(ctx context.Context, kcmReleaseName string, opts *client.ListOptions) (requeue bool, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Suspending KCM Helm Release reconciles")
	kcmRelease := &fluxv2.HelmRelease{}
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
	gvk := fluxv2.GroupVersion.WithKind(fluxv2.HelmReleaseKind)
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
	kcm.Component

	helmReleaseName string
	targetNamespace string
	installSettings *fluxv2.Install
	// helm release dependencies
	dependsOn      []fluxmeta.NamespacedObjectReference
	isCAPIProvider bool
}

func applyKCMDefaults(config *apiextensionsv1.JSON) (*apiextensionsv1.JSON, error) {
	values := chartutil.Values{}
	if config != nil && config.Raw != nil {
		err := json.Unmarshal(config.Raw, &values)
		if err != nil {
			return nil, err
		}
	}

	// Those are only needed for the initial installation
	enforcedValues := map[string]any{
		"controller": map[string]any{
			"createManagement":       false,
			"createAccessManagement": false,
			"createRelease":          false,
		},
	}

	chartutil.CoalesceTables(values, enforcedValues)
	raw, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return &apiextensionsv1.JSON{Raw: raw}, nil
}

func getWrappedComponents(ctx context.Context, cl client.Client, mgmt *kcm.Management) ([]component, error) {
	release := &kcm.Release{}
	if err := cl.Get(ctx, client.ObjectKey{Name: mgmt.Spec.Release}, release); err != nil {
		return nil, fmt.Errorf("failed to get Release %s: %w", mgmt.Spec.Release, err)
	}

	components := make([]component, 0, len(mgmt.Spec.Providers)+2)

	kcmComponent := kcm.Component{}
	capiComponent := kcm.Component{}
	if mgmt.Spec.Core != nil {
		kcmComponent = mgmt.Spec.Core.KCM
		capiComponent = mgmt.Spec.Core.CAPI
	}

	kcmComp := component{Component: kcmComponent, helmReleaseName: kcm.CoreKCMName}
	if kcmComp.Template == "" {
		kcmComp.Template = release.Spec.KCM.Template
	}
	kcmConfig, err := applyKCMDefaults(kcmComp.Config)
	if err != nil {
		return nil, err
	}
	kcmComp.Config = kcmConfig
	components = append(components, kcmComp)

	capiComp := component{
		Component: capiComponent,
		installSettings: &fluxv2.Install{
			Remediation: &fluxv2.InstallRemediation{
				Retries:              1,
				RemediateLastFailure: utils.PtrTo(true),
			},
		},
		helmReleaseName: kcm.CoreCAPIName,
		dependsOn:       []fluxmeta.NamespacedObjectReference{{Name: kcm.CoreKCMName}},
		isCAPIProvider:  true,
	}
	if capiComp.Template == "" {
		capiComp.Template = release.Spec.CAPI.Template
	}
	components = append(components, capiComp)

	const sveltosTargetNamespace = "projectsveltos"

	for _, p := range mgmt.Spec.Providers {
		c := component{
			Component: p.Component, helmReleaseName: p.Name,
			dependsOn: []fluxmeta.NamespacedObjectReference{{Name: kcm.CoreCAPIName}}, isCAPIProvider: true,
		}
		// Try to find corresponding provider in the Release object
		if c.Template == "" {
			c.Template = release.ProviderTemplate(p.Name)
		}

		if p.Name == kcm.ProviderSveltosName {
			c.isCAPIProvider = false
			c.targetNamespace = sveltosTargetNamespace
			c.installSettings = &fluxv2.Install{
				CreateNamespace: true,
			}
		}

		components = append(components, c)
	}

	return components, nil
}

// enableAdditionalComponents enables the admission controller and cluster api operator
// once the cert manager is ready
func (r *ManagementReconciler) enableAdditionalComponents(ctx context.Context, mgmt *kcm.Management) error {
	l := ctrl.LoggerFrom(ctx)

	config := make(map[string]any)

	if mgmt.Spec.Core == nil {
		mgmt.Spec.Core = new(kcm.Core)
	}
	if mgmt.Spec.Core.KCM.Config != nil {
		if err := json.Unmarshal(mgmt.Spec.Core.KCM.Config.Raw, &config); err != nil {
			return fmt.Errorf("failed to unmarshal KCM config into map[string]any: %w", err)
		}
	}

	admissionWebhookValues := make(map[string]any)
	if config["admissionWebhook"] != nil {
		v, ok := config["admissionWebhook"].(map[string]any)
		if !ok {
			return fmt.Errorf("failed to cast 'admissionWebhook' (type %T) to map[string]any", config["admissionWebhook"])
		}

		admissionWebhookValues = v
	}

	capiOperatorValues := make(map[string]any)
	if config["cluster-api-operator"] != nil {
		v, ok := config["cluster-api-operator"].(map[string]any)
		if !ok {
			return fmt.Errorf("failed to cast 'cluster-api-operator' (type %T) to map[string]any", config["cluster-api-operator"])
		}

		capiOperatorValues = v
	}

	if config["velero"] != nil {
		v, ok := config["velero"].(map[string]any)
		if !ok {
			return fmt.Errorf("failed to cast 'velero' (type %T) to map[string]any", config["velero"])
		}

		config["velero"] = v
	}

	if r.Config != nil {
		if err := certmanager.VerifyAPI(ctx, r.Config, r.SystemNamespace); err != nil {
			return fmt.Errorf("failed to check in the cert-manager API is installed: %w", err)
		}

		// Enable KCM webhook only if it was not explicitly disabled in the config to
		// support installation without webhook
		{
			enabledV, found := admissionWebhookValues["enabled"]
			enabledValue, castedOk := enabledV.(bool)
			if !found || !castedOk || enabledValue {
				l.Info("Cert manager is installed, enabling the KCM admission webhook")
				admissionWebhookValues["enabled"] = true
			}
		}
	}

	config["admissionWebhook"] = admissionWebhookValues

	// Enable KCM capi operator only if it was not explicitly disabled in the config to
	// support installation with existing cluster api operator
	{
		enabledV, enabledExists := capiOperatorValues["enabled"]
		enabledValue, castedOk := enabledV.(bool)
		if !enabledExists || !castedOk || enabledValue {
			l.Info("Enabling cluster API operator")
			capiOperatorValues["enabled"] = true
		}
	}
	config["cluster-api-operator"] = capiOperatorValues

	updatedConfig, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal KCM config: %w", err)
	}

	mgmt.Spec.Core.KCM.Config = &apiextensionsv1.JSON{Raw: updatedConfig}

	return nil
}

func (r *ManagementReconciler) ensureUpgradeBackup(ctx context.Context, mgmt *kcm.Management) (requeue bool, _ error) {
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

	autoUpgradeBackups := new(kcm.ManagementBackupList)
	if err := r.Client.List(ctx, autoUpgradeBackups, client.MatchingFields{kcm.ManagementBackupAutoUpgradeIndexKey: "true"}); err != nil {
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
		mb := new(kcm.ManagementBackup)
		err := r.Client.Get(ctx, client.ObjectKey{Name: name}, mb)
		isNotFoundErr := apierrors.IsNotFound(err)
		if err != nil && !isNotFoundErr {
			return false, fmt.Errorf("failed to get ManagementBackup %s: %w", name, err)
		}

		// have to create
		if isNotFoundErr {
			mb = &kcm.ManagementBackup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: kcm.GroupVersion.String(),
					Kind:       "ManagementBackup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
					// TODO: generilize the label?
					Labels: map[string]string{"k0rdent.mirantis.com/release-backup": mgmt.Status.Release},
				},
				Spec: kcm.ManagementBackupSpec{
					StorageLocation: location,
				},
			}

			if err := r.Client.Create(ctx, mb); err != nil {
				return false, fmt.Errorf("failed to create a single ManagementBackup %s: %w", name, err)
			}

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

type mgmtStatusAccumulator struct {
	components             map[string]kcm.ComponentStatus
	compatibilityContracts map[string]kcm.CompatibilityContracts
	providers              kcm.Providers
}

func updateComponentsStatus(
	stAcc *mgmtStatusAccumulator,
	comp component,
	template *kcm.ProviderTemplate,
	err string,
) {
	if stAcc == nil {
		return
	}

	stAcc.components[comp.helmReleaseName] = kcm.ComponentStatus{
		Error:    err,
		Success:  err == "",
		Template: comp.Component.Template,
	}

	if err == "" && template != nil {
		stAcc.providers = append(stAcc.providers, template.Status.Providers...)
		slices.Sort(stAcc.providers)
		stAcc.providers = slices.Compact(stAcc.providers)

		for _, v := range template.Status.Providers {
			stAcc.compatibilityContracts[v] = template.Status.CAPIContracts
		}
	}
}

// setReadyCondition updates the Management resource's "Ready" condition based on whether
// all components are healthy.
func setReadyCondition(management *kcm.Management) {
	var failing []string
	for name, comp := range management.Status.Components {
		if !comp.Success {
			failing = append(failing, name)
		}
	}

	readyCond := metav1.Condition{
		Type:               kcm.ReadyCondition,
		ObservedGeneration: management.Generation,
		Status:             metav1.ConditionTrue,
		Reason:             kcm.AllComponentsHealthyReason,
		Message:            "All components are successfully installed.",
	}
	sort.Strings(failing)
	if len(failing) > 0 {
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = kcm.NotAllComponentsHealthyReason
		readyCond.Message = fmt.Sprintf("Components not ready: %v", failing)
	}

	meta.SetStatusCondition(&management.Status.Conditions, readyCond)
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

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcm.Management{}).
		Complete(r)
}
