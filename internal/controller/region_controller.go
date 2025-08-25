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

package controller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
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
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
	schemeutil "github.com/K0rdent/kcm/internal/utils/scheme"
)

// RegionReconciler reconciles a Region object
type RegionReconciler struct {
	MgmtClient             client.Client
	Manager                manager.Manager
	Config                 *rest.Config
	DynamicClient          *dynamic.DynamicClient
	SystemNamespace        string
	GlobalRegistry         string
	RegistryCertSecretName string // Name of a Secret with Registry Root CA with ca.crt key; used by RegionReconciler

	DefaultHelmTimeout time.Duration
	defaultRequeueTime time.Duration

	CreateAccessManagement bool
	IsDisabledValidationWH bool // is webhook disabled set via the controller flags
}

func (r *RegionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	if !region.DeletionTimestamp.IsZero() {
		l.Info("Deleting Region")
		return r.delete(ctx, region)
	}

	return r.update(ctx, region)
}

func (r *RegionReconciler) update(ctx context.Context, region *kcmv1.Region) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	if controllerutil.AddFinalizer(region, kcmv1.RegionFinalizer) {
		if err := r.MgmtClient.Update(ctx, region); err != nil {
			l.Error(err, "failed to update Region finalizers")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	updated, err := utils.AddKCMComponentLabel(ctx, r.MgmtClient, region)
	if updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{}, err
	}

	defer func() {
		err = errors.Join(err, r.updateStatus(ctx, region))
	}()

	var kubeConfigRef *fluxmeta.SecretKeyReference

	if region.Spec.KubeConfig != nil {
		kubeConfigRef = region.Spec.KubeConfig
	}
	if kubeConfigRef == nil {
		return ctrl.Result{}, errors.New("kubeConfig should be defined")
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

	opts := components.ReconcileComponentsOpts{
		DefaultHelmTimeout:     r.DefaultHelmTimeout,
		Namespace:              r.SystemNamespace,
		GlobalRegistry:         r.GlobalRegistry,
		RegistryCertSecretName: r.RegistryCertSecretName,
		KubeConfigRef:          kubeConfigRef,

		CreateNamespace: true,
		Labels: map[string]string{
			kcmv1.KCMRegionLabelKey: region.Name,
		},
	}

	rgnlClient, restConfig, err := r.getRegionClients(ctx, region)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get rest config for the %s region: %w", region.Name, err)
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

	r.setReadyCondition(region)
	return ctrl.Result{}, nil
}

func (r *RegionReconciler) getRegionClients(ctx context.Context, region *kcmv1.Region) (client.Client, *rest.Config, error) {
	kubeConfigBytes, err := r.getKubeConfig(ctx, region)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get kubeconfig data: %w", err)
	}
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build rest config from the kubeconfig data: %w", err)
	}

	scheme := runtime.NewScheme()
	schemeutil.RegisterRegional(scheme)
	cl, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create client: %w", err)
	}
	return cl, restConfig, nil
}

func (r *RegionReconciler) getKubeConfig(ctx context.Context, region *kcmv1.Region) ([]byte, error) {
	if region.Spec.KubeConfig == nil && region.Spec.ClusterDeployment == nil {
		return nil, errors.New("either spec.kubeConfig or spec.clusterDeployment must be set")
	}
	if region.Spec.KubeConfig != nil && region.Spec.ClusterDeployment != nil {
		return nil, errors.New("only one of spec.kubeConfig and spec.clusterDeployment is allowed")
	}

	secret := new(corev1.Secret)
	var (
		secretObj     client.ObjectKey
		kubeconfigKey string
	)

	// Currently, only spec.KubeConfig is supported.
	// TODO: Add support for spec.ClusterDeployment reference.
	if region.Spec.KubeConfig != nil {
		secretObj = client.ObjectKey{Namespace: r.SystemNamespace, Name: region.Spec.KubeConfig.Name}
		kubeconfigKey = region.Spec.KubeConfig.Key
	}
	if err := r.MgmtClient.Get(ctx, secretObj, secret); err != nil {
		return nil, fmt.Errorf("failed to get Secret with kubeconfig: %w", err)
	}

	kubeconfigBytes, ok := secret.Data[kubeconfigKey]
	if !ok {
		return nil, fmt.Errorf("kubeconfig from Secret %s is empty", secretObj)
	}
	return kubeconfigBytes, nil
}

// setReadyCondition updates the Region resource's "Ready" condition based on whether
// all components are healthy.
func (r *RegionReconciler) setReadyCondition(region *kcmv1.Region) {
	var failing []string
	for name, comp := range region.Status.Components {
		if !comp.Success {
			failing = append(failing, name)
		}
	}

	readyCond := metav1.Condition{
		Type:               kcmv1.ReadyCondition,
		ObservedGeneration: region.Generation,
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
	if meta.SetStatusCondition(&region.Status.Conditions, readyCond) && readyCond.Status == metav1.ConditionTrue {
		r.eventf(region, "RegionIsReady", "Regional KCM components are ready")
	}
}

func (r *RegionReconciler) delete(ctx context.Context, region *kcmv1.Region) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	r.eventf(region, "RemovingRegion", "Removing KCM regional components")

	var err error
	defer func() {
		err = errors.Join(err, r.updateStatus(ctx, region))
	}()

	requeue, err := r.removeHelmReleases(ctx, region)
	if err != nil {
		return ctrl.Result{}, err
	}
	if requeue {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	r.eventf(region, "RemovedRegion", "Region has been removed")
	l.Info("Removing Region finalizer")
	if controllerutil.RemoveFinalizer(region, kcmv1.RegionFinalizer) {
		return ctrl.Result{}, r.MgmtClient.Update(ctx, region)
	}
	return ctrl.Result{}, nil
}

func (r *RegionReconciler) removeHelmReleases(ctx context.Context, region *kcmv1.Region) (bool, error) {
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
		if dependents[hr.Name] == nil {
			dependents[hr.Name] = make(map[string]struct{})
		}
		for _, dep := range hr.Spec.DependsOn {
			if dependents[dep.Name] == nil {
				dependents[dep.Name] = make(map[string]struct{})
			}
			dependents[dep.Name][hr.Name] = struct{}{}
		}
	}

	// Try to delete HelmReleases that no one depends on
	var errs error
	for _, hr := range hrList.Items {
		if len(dependents[hr.Name]) > 0 {
			l.V(1).Info("Skipping HelmRelease with dependents", "name", hr.Name, "dependents", dependents[hr.Name])
			continue
		}

		l.V(1).Info("Deleting HelmRelease", "name", hr.Name)
		r.eventf(region, "RemovingComponent", "Removing %s HelmRelease", hr.Name)

		hrCopy := &helmcontrollerv2.HelmRelease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hr.Name,
				Namespace: r.SystemNamespace,
			},
		}

		if err := r.MgmtClient.Delete(ctx, hrCopy); client.IgnoreNotFound(err) != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to delete %s: %w", client.ObjectKeyFromObject(hrCopy), err))
			continue
		}

		l.V(1).Info("Deleted HelmRelease", "name", hr.Name)
		r.eventf(region, "ComponentRemoved", "Removed %s HelmRelease", hr.Name)
	}

	if errs != nil {
		return false, errs
	}

	// If there are still HelmReleases left, requeue until cleanup is complete
	if len(hrList.Items) > 0 {
		l.Info("Waiting for all HelmReleases to be deleted before removing finalizer")
		return true, errs
	}
	return false, nil
}

func (r *RegionReconciler) updateStatus(ctx context.Context, region *kcmv1.Region) error {
	if err := r.MgmtClient.Status().Update(ctx, region); err != nil {
		return fmt.Errorf("failed to update status for Region %s: %w", region.Name, err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RegionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.defaultRequeueTime = 10 * time.Second

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcmv1.Region{}).
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
		}),
		).
		Complete(r)
}

func (*RegionReconciler) eventf(region *kcmv1.Region, reason, message string, args ...any) {
	record.Eventf(region, region.Generation, reason, message, args...)
}

func (*RegionReconciler) warnf(region *kcmv1.Region, reason, message string, args ...any) {
	record.Warnf(region, region.Generation, reason, message, args...)
}
