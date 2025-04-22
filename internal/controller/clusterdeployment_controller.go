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
	"strings"
	"time"

	hcv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	fluxconditions "github.com/fluxcd/pkg/runtime/conditions"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sveltosv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/helm"
	"github.com/K0rdent/kcm/internal/metrics"
	providersloader "github.com/K0rdent/kcm/internal/providers"
	"github.com/K0rdent/kcm/internal/record"
	"github.com/K0rdent/kcm/internal/sveltos"
	"github.com/K0rdent/kcm/internal/telemetry"
	"github.com/K0rdent/kcm/internal/utils"
	conditionsutil "github.com/K0rdent/kcm/internal/utils/conditions"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
	"github.com/K0rdent/kcm/internal/utils/validation"
)

var (
	errClusterNotFound         = errors.New("cluster is not found")
	errClusterTemplateNotFound = errors.New("cluster template is not found")
)

type helmActor interface {
	DownloadChartFromArtifact(ctx context.Context, artifact *sourcev1.Artifact) (*chart.Chart, error)
	InitializeConfiguration(clusterDeployment *kcm.ClusterDeployment, log action.DebugLog) (*action.Configuration, error)
	EnsureReleaseWithValues(ctx context.Context, actionConfig *action.Configuration, hcChart *chart.Chart, clusterDeployment *kcm.ClusterDeployment) error
}

// ClusterDeploymentReconciler reconciles a ClusterDeployment object
type ClusterDeploymentReconciler struct {
	Client client.Client
	helmActor
	Config          *rest.Config
	DynamicClient   *dynamic.DynamicClient
	SystemNamespace string

	defaultRequeueTime time.Duration

	IsDisabledValidationWH bool // is webhook disabled set via the controller flags
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ClusterDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ClusterDeployment")

	clusterDeployment := &kcm.ClusterDeployment{}
	if err := r.Client.Get(ctx, req.NamespacedName, clusterDeployment); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ClusterDeployment not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		l.Error(err, "Failed to get ClusterDeployment")
		return ctrl.Result{}, err
	}

	if !clusterDeployment.DeletionTimestamp.IsZero() {
		l.Info("Deleting ClusterDeployment")
		return r.reconcileDelete(ctx, clusterDeployment)
	}

	management := &kcm.Management{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: kcm.ManagementName}, management); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Management: %w", err)
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping ClusterDeployment reconciliation")
		return ctrl.Result{}, nil
	}

	if clusterDeployment.Status.ObservedGeneration == 0 {
		mgmt := &kcm.Management{}
		mgmtRef := client.ObjectKey{Name: kcm.ManagementName}
		if err := r.Client.Get(ctx, mgmtRef, mgmt); err != nil {
			l.Error(err, "Failed to get Management object")
			return ctrl.Result{}, err
		}
		if err := telemetry.TrackClusterDeploymentCreate(string(mgmt.UID), string(clusterDeployment.UID), clusterDeployment.Spec.Template, clusterDeployment.Spec.DryRun); err != nil {
			l.Error(err, "Failed to track ClusterDeployment creation")
		}
	}

	return r.reconcileUpdate(ctx, clusterDeployment)
}

func (r *ClusterDeploymentReconciler) reconcileUpdate(ctx context.Context, cd *kcm.ClusterDeployment) (_ ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)

	if controllerutil.AddFinalizer(cd, kcm.ClusterDeploymentFinalizer) {
		if err := r.Client.Update(ctx, cd); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update clusterDeployment %s/%s: %w", cd.Namespace, cd.Name, err)
		}
		return ctrl.Result{}, nil
	}

	if updated, err := utils.AddKCMComponentLabel(ctx, r.Client, cd); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{}, err
	}

	if len(cd.Status.Conditions) == 0 {
		cd.InitConditions()
	}

	clusterTpl := &kcm.ClusterTemplate{}
	defer func() {
		err = errors.Join(err, r.updateStatus(ctx, cd, clusterTpl))
	}()

	if err = r.Client.Get(ctx, client.ObjectKey{Name: cd.Spec.Template, Namespace: cd.Namespace}, clusterTpl); err != nil {
		l.Error(err, "failed to get ClusterTemplate")
		err = fmt.Errorf("failed to get ClusterTemplate %s/%s: %w", cd.Namespace, cd.Spec.Template, err)
		if r.setCondition(cd, kcm.TemplateReadyCondition, err) {
			r.warnf(cd, "ClusterTemplateError", err.Error())
		}
		if r.IsDisabledValidationWH {
			l.Error(err, "failed to get ClusterTemplate, will not retrigger")
			return ctrl.Result{}, nil // no retrigger
		}
		l.Error(err, "failed to get ClusterTemplate")
		return ctrl.Result{}, err
	}

	clusterRes, clusterErr := r.updateCluster(ctx, cd, clusterTpl)
	servicesRes, servicesErr := r.updateServices(ctx, cd)

	if err = errors.Join(clusterErr, servicesErr); err != nil {
		return ctrl.Result{}, err
	}
	if !clusterRes.IsZero() {
		return clusterRes, nil
	}
	if !servicesRes.IsZero() {
		return servicesRes, nil
	}

	return ctrl.Result{}, nil
}

func (r *ClusterDeploymentReconciler) updateCluster(ctx context.Context, cd *kcm.ClusterDeployment, clusterTpl *kcm.ClusterTemplate) (ctrl.Result, error) {
	if clusterTpl == nil {
		return ctrl.Result{}, errors.New("cluster template cannot be nil")
	}

	l := ctrl.LoggerFrom(ctx)

	r.initClusterConditions(cd)

	if !clusterTpl.Status.Valid {
		errMsg := fmt.Sprintf("ClusterTemplate %s is not marked as valid", client.ObjectKeyFromObject(clusterTpl))
		if clusterTpl.Status.ValidationError != "" {
			errMsg += ": " + clusterTpl.Status.ValidationError
		}
		err := errors.New(errMsg)
		if r.setCondition(cd, kcm.TemplateReadyCondition, err) {
			r.warnf(cd, "InvalidClusterTemplate", errMsg)
		}
		if r.IsDisabledValidationWH {
			l.Error(err, "template is not valid, will not retrigger this error")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	r.setCondition(cd, kcm.TemplateReadyCondition, nil)
	// template is ok, propagate data from it
	cd.Status.KubernetesVersion = clusterTpl.Status.KubernetesVersion

	var cred *kcm.Credential
	if r.IsDisabledValidationWH {
		l.Info("Validating ClusterTemplate K8s compatibility")
		compErr := validation.ClusterTemplateK8sCompatibility(ctx, r.Client, clusterTpl, cd)
		if compErr != nil {
			compErr = fmt.Errorf("failed to validate ClusterTemplate K8s compatibility: %w", compErr)
		}
		r.setCondition(cd, kcm.TemplateReadyCondition, compErr)

		l.Info("Validating Credential")
		var credErr error
		if cred, credErr = validation.ClusterDeployCredential(ctx, r.Client, cd, clusterTpl); credErr != nil {
			credErr = fmt.Errorf("failed to validate Credential: %w", credErr)
		}
		r.setCondition(cd, kcm.CredentialReadyCondition, credErr)

		if merr := errors.Join(compErr, credErr); merr != nil {
			l.Error(merr, "failed to validate ClusterDeployment, will not retrigger this error")
			return ctrl.Result{}, nil
		}
	}

	err := r.validateConfig(ctx, cd, clusterTpl)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to validate ClusterDeployment configuration: %w", err)
	}

	if !r.IsDisabledValidationWH {
		cred = new(kcm.Credential)
		if err := r.Client.Get(ctx, client.ObjectKey{Name: cd.Spec.Credential, Namespace: cd.Namespace}, cred); err != nil {
			err = fmt.Errorf("failed to get Credential %s/%s: %w", cd.Namespace, cd.Spec.Credential, err)
			if r.setCondition(cd, kcm.CredentialReadyCondition, err) {
				r.warnf(cd, "CredentialError", err.Error())
			}
			return ctrl.Result{}, err
		}

		if !cred.Status.Ready {
			if r.setCondition(cd, kcm.CredentialReadyCondition, fmt.Errorf("the Credential %s is not ready", client.ObjectKeyFromObject(cred))) {
				r.warnf(cd, "CredentialNotReady", "Credential %s/%s is not ready", cd.Namespace, cd.Spec.Credential)
			}
		} else {
			r.setCondition(cd, kcm.CredentialReadyCondition, nil)
		}
	}

	if cd.Spec.DryRun {
		r.eventf(cd, "DryRunEnabled", "DryRun mode is enabled. Remove spec.dryRun to proceed with the deployment")
		return ctrl.Result{}, nil
	}

	if err := cd.AddHelmValues(func(values map[string]any) error {
		values["clusterIdentity"] = map[string]any{
			"apiVersion": cred.Spec.IdentityRef.APIVersion,
			"kind":       cred.Spec.IdentityRef.Kind,
			"name":       cred.Spec.IdentityRef.Name,
			"namespace":  cred.Spec.IdentityRef.Namespace,
		}

		if _, ok := values["clusterLabels"]; !ok {
			// Use the ManagedCluster's own labels if not defined.
			values["clusterLabels"] = cd.GetObjectMeta().GetLabels()
		}

		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}

	hrReconcileOpts := helm.ReconcileHelmReleaseOpts{
		Values: cd.Spec.Config,
		OwnerReference: &metav1.OwnerReference{
			APIVersion: kcm.GroupVersion.String(),
			Kind:       kcm.ClusterDeploymentKind,
			Name:       cd.Name,
			UID:        cd.UID,
		},
		ChartRef: clusterTpl.Status.ChartRef,
	}
	if clusterTpl.Spec.Helm.ChartSpec != nil {
		hrReconcileOpts.ReconcileInterval = &clusterTpl.Spec.Helm.ChartSpec.Interval.Duration
	}

	hr, operation, err := helm.ReconcileHelmRelease(ctx, r.Client, cd.Name, cd.Namespace, hrReconcileOpts)
	if err != nil {
		err = fmt.Errorf("failed to reconcile HelmRelease: %w", err)
		if r.setCondition(cd, kcm.HelmReleaseReadyCondition, err) {
			r.warnf(cd, "HelmReleaseReconcileFailed", err.Error())
		}
		return ctrl.Result{}, err
	}
	if operation == controllerutil.OperationResultCreated {
		r.eventf(cd, "HelmReleaseCreated", "Successfully created HelmRelease %s/%s", cd.Namespace, cd.Name)
	}
	if operation == controllerutil.OperationResultUpdated {
		r.eventf(cd, "HelmReleaseUpdated", "Successfully updated HelmRelease %s/%s", cd.Namespace, cd.Name)
	}

	hrReadyCondition := fluxconditions.Get(hr, fluxmeta.ReadyCondition)
	if hrReadyCondition != nil {
		if apimeta.SetStatusCondition(cd.GetConditions(), metav1.Condition{
			Type:    kcm.HelmReleaseReadyCondition,
			Status:  hrReadyCondition.Status,
			Reason:  hrReadyCondition.Reason,
			Message: hrReadyCondition.Message,
		}) {
			r.eventf(cd, "HelmReleaseIsReady", "HelmRelease %s/%s is ready", cd.Namespace, cd.Name)
		}
	}

	requeue, err := r.aggregateConditions(ctx, cd)
	if err != nil {
		if requeue {
			return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, err
		}

		return ctrl.Result{}, err
	}

	if requeue || !fluxconditions.IsReady(hr) {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ClusterDeploymentReconciler) validateConfig(ctx context.Context, cd *kcm.ClusterDeployment, clusterTpl *kcm.ClusterTemplate) error {
	helmChartArtifact, err := r.getSourceArtifact(ctx, clusterTpl.Status.ChartRef)
	if err != nil {
		err = fmt.Errorf("failed to get HelmChart Artifact: %w", err)
		if r.setCondition(cd, kcm.HelmChartReadyCondition, err) {
			r.warnf(cd, "InvalidSource", err.Error())
		}
		return err
	}

	l := ctrl.LoggerFrom(ctx)
	l.Info("Downloading Helm chart")
	hcChart, err := r.DownloadChartFromArtifact(ctx, helmChartArtifact)
	if err != nil {
		err = fmt.Errorf("failed to download HelmChart from Artifact %s: %w", helmChartArtifact.URL, err)
		if r.setCondition(cd, kcm.HelmChartReadyCondition, err) {
			r.warnf(cd, "HelmChartDownloadFailed", err.Error())
		}
		return err
	}

	l.Info("Initializing Helm client")
	actionConfig, err := r.InitializeConfiguration(cd, l.WithName("helm-actor").V(1).Info)
	if err != nil {
		return err
	}

	l.Info("Validating Helm chart with provided values")
	if err := r.EnsureReleaseWithValues(ctx, actionConfig, hcChart, cd); err != nil {
		err = fmt.Errorf("failed to validate template with provided configuration: %w", err)
		if r.setCondition(cd, kcm.HelmChartReadyCondition, err) {
			r.warnf(cd, "ValidationError", "Invalid configuration provided: %s", err)
		}
		return err
	}

	r.setCondition(cd, kcm.HelmChartReadyCondition, nil)
	return nil
}

func (*ClusterDeploymentReconciler) initClusterConditions(cd *kcm.ClusterDeployment) (changed bool) {
	for _, typ := range [4]string{kcm.CredentialReadyCondition, kcm.HelmReleaseReadyCondition, kcm.HelmChartReadyCondition, kcm.TemplateReadyCondition} {
		if apimeta.SetStatusCondition(&cd.Status.Conditions, metav1.Condition{
			Type:               typ,
			Status:             metav1.ConditionUnknown,
			Reason:             kcm.ProgressingReason,
			ObservedGeneration: cd.Generation,
		}) {
			changed = true
		}
	}

	return changed
}

func (r *ClusterDeploymentReconciler) updateSveltosClusterCondition(ctx context.Context, clusterDeployment *kcm.ClusterDeployment) (bool, error) {
	sveltosClusters := &libsveltosv1beta1.SveltosClusterList{}

	if err := r.Client.List(ctx, sveltosClusters, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{kcm.FluxHelmChartNameKey: clusterDeployment.Name}),
	}); err != nil {
		return true, fmt.Errorf("failed to get sveltos cluster status: %w", err)
	}

	for _, sveltosCluster := range sveltosClusters.Items {
		sveltosCondition := metav1.Condition{
			Status: metav1.ConditionUnknown,
			Type:   kcm.SveltosClusterReadyCondition,
		}

		if sveltosCluster.Status.ConnectionStatus == libsveltosv1beta1.ConnectionHealthy {
			sveltosCondition.Status = metav1.ConditionTrue
			sveltosCondition.Message = "sveltos cluster is healthy"
			sveltosCondition.Reason = kcm.SucceededReason
		} else {
			sveltosCondition.Status = metav1.ConditionFalse
			sveltosCondition.Reason = kcm.FailedReason
			if sveltosCluster.Status.FailureMessage != nil {
				sveltosCondition.Message = *sveltosCluster.Status.FailureMessage
			}
		}
		apimeta.SetStatusCondition(clusterDeployment.GetConditions(), sveltosCondition)
	}

	return false, nil
}

func (r *ClusterDeploymentReconciler) aggregateConditions(ctx context.Context, cd *kcm.ClusterDeployment) (bool, error) {
	var (
		requeue bool
		errs    error
	)
	for _, updateConditions := range []func(context.Context, *kcm.ClusterDeployment) (bool, error){
		r.updateSveltosClusterCondition,
		r.aggregateCapiConditions,
	} {
		needRequeue, err := updateConditions(ctx, cd)
		if needRequeue {
			requeue = true
		}
		errs = errors.Join(errs, err)
	}
	return requeue, errs
}

func (r *ClusterDeploymentReconciler) aggregateCapiConditions(ctx context.Context, cd *kcm.ClusterDeployment) (requeue bool, _ error) {
	clusters := &clusterapiv1.ClusterList{}
	if err := r.Client.List(ctx, clusters, client.MatchingLabels{kcm.FluxHelmChartNameKey: cd.Name}, client.Limit(1)); err != nil {
		return false, fmt.Errorf("failed to list clusters for ClusterDeployment %s: %w", client.ObjectKeyFromObject(cd), err)
	}
	if len(clusters.Items) == 0 {
		return false, nil
	}
	cluster := &clusters.Items[0]

	capiCondition, err := conditionsutil.GetCAPIClusterSummaryCondition(cd, cluster)
	if err != nil {
		return true, fmt.Errorf("failed to get condition summary from Cluster %s: %w", client.ObjectKeyFromObject(cluster), err)
	}

	if apimeta.SetStatusCondition(cd.GetConditions(), *capiCondition) {
		if capiCondition.Status == metav1.ConditionTrue {
			r.eventf(cd, "CAPIClusterIsReady", "Cluster has been provisioned")
			return false, nil
		}
		if cd.DeletionTimestamp.IsZero() {
			r.eventf(cd, "CAPIClusterIsProvisioning", "Cluster is provisioning")
		} else {
			r.eventf(cd, "CAPIClusterIsDeleting", "Cluster is deleting")
		}
	}
	return capiCondition.Status != metav1.ConditionTrue, nil
}

func getProjectTemplateResourceRefs(mc *kcm.ClusterDeployment, cred *kcm.Credential) []sveltosv1beta1.TemplateResourceRef {
	if !mc.Spec.PropagateCredentials || cred.Spec.IdentityRef == nil {
		return nil
	}

	refs := []sveltosv1beta1.TemplateResourceRef{
		{
			Resource:   *cred.Spec.IdentityRef,
			Identifier: "InfrastructureProviderIdentity",
		},
	}

	if !strings.EqualFold(cred.Spec.IdentityRef.Kind, "Secret") {
		refs = append(refs, sveltosv1beta1.TemplateResourceRef{
			Resource: corev1.ObjectReference{
				APIVersion: "v1",
				Kind:       "Secret",
				Namespace:  cred.Spec.IdentityRef.Namespace,
				Name:       cred.Spec.IdentityRef.Name + "-secret",
			},
			Identifier: "InfrastructureProviderIdentitySecret",
		})
	}

	return refs
}

func getProjectPolicyRefs(mc *kcm.ClusterDeployment, cred *kcm.Credential) []sveltosv1beta1.PolicyRef {
	if !mc.Spec.PropagateCredentials || cred.Spec.IdentityRef == nil {
		return nil
	}

	return []sveltosv1beta1.PolicyRef{
		{
			Kind:           "ConfigMap",
			Namespace:      cred.Spec.IdentityRef.Namespace,
			Name:           cred.Spec.IdentityRef.Name + "-resource-template",
			DeploymentType: sveltosv1beta1.DeploymentTypeRemote,
		},
	}
}

func (*ClusterDeploymentReconciler) initServicesConditions(cd *kcm.ClusterDeployment) (changed bool) {
	for _, typ := range [3]string{kcm.SveltosProfileReadyCondition, kcm.FetchServicesStatusSuccessCondition, kcm.ServicesReferencesValidationCondition} {
		if apimeta.SetStatusCondition(&cd.Status.Conditions, metav1.Condition{
			Type:               typ,
			Status:             metav1.ConditionUnknown,
			Reason:             kcm.ProgressingReason,
			ObservedGeneration: cd.Generation,
		}) {
			changed = true
		}
	}

	return changed
}

func (*ClusterDeploymentReconciler) setCondition(cd *kcm.ClusterDeployment, typ string, err error) (changed bool) {
	reason, cstatus, msg := kcm.SucceededReason, metav1.ConditionTrue, ""
	if err != nil {
		reason, cstatus, msg = kcm.FailedReason, metav1.ConditionFalse, err.Error()
	}

	return apimeta.SetStatusCondition(&cd.Status.Conditions, metav1.Condition{
		Type:               typ,
		Status:             cstatus,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: cd.Generation,
	})
}

// updateServices reconciles services provided in ClusterDeployment.Spec.ServiceSpec.
func (r *ClusterDeploymentReconciler) updateServices(ctx context.Context, cd *kcm.ClusterDeployment) (_ ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling Services")

	r.initServicesConditions(cd)

	{
		merr := validation.ClusterDeployCrossNamespaceServicesRefs(ctx, cd) // changes must be done in the spec, which is a new event
		if r.IsDisabledValidationWH {
			merr = errors.Join(merr, validation.ServicesHaveValidTemplates(ctx, r.Client, cd.Spec.ServiceSpec.Services, cd.Namespace))
		}
		r.setCondition(cd, kcm.ServicesReferencesValidationCondition, merr)
		if merr != nil {
			// at this point 2/3 conditions will have unknown status, 1/3 (services validation) cond will have failed cond
			l.Error(merr, "failed to validate services, will not retrigger this error")
			return ctrl.Result{}, nil // no reason to reconcile further
		}
	}

	// servicesErr is handled separately from err because we do not want
	// to set the condition of SveltosProfileReady type to "False"
	// if there is an error while retrieving status for the services.
	var servicesErr error

	// TODO: should be refactored, unmaintainable; requires to refactor the whole conditions-approach (e.g. init on each event); requires to refactor controllers to be moved to dedicated pkgs
	defer func() {
		r.setCondition(cd, kcm.SveltosProfileReadyCondition, err)
		r.setCondition(cd, kcm.FetchServicesStatusSuccessCondition, servicesErr)
		if r.IsDisabledValidationWH && apierrors.IsNotFound(err) {
			// non-services NotFound errors relate only to ServiceTemplate
			// if they are gone then nothing to do
			err = nil
		}
		err = errors.Join(err, servicesErr)
	}()

	helmCharts, err := sveltos.GetHelmCharts(ctx, r.Client, cd.Namespace, cd.Spec.ServiceSpec.Services)
	if err != nil {
		return ctrl.Result{}, err
	}
	kustomizationRefs, err := sveltos.GetKustomizationRefs(ctx, r.Client, cd.Namespace, cd.Spec.ServiceSpec.Services)
	if err != nil {
		return ctrl.Result{}, err
	}
	policyRefs, err := sveltos.GetPolicyRefs(ctx, r.Client, cd.Namespace, cd.Spec.ServiceSpec.Services)
	if err != nil {
		return ctrl.Result{}, err
	}

	cred := new(kcm.Credential)
	if err := r.Client.Get(ctx, client.ObjectKey{Name: cd.Spec.Credential, Namespace: cd.Namespace}, cred); err != nil {
		return ctrl.Result{}, err
	}

	if _, err := sveltos.ReconcileProfile(ctx, r.Client, cd.Namespace, cd.Name,
		sveltos.ReconcileProfileOpts{
			OwnerReference: &metav1.OwnerReference{
				APIVersion: kcm.GroupVersion.String(),
				Kind:       kcm.ClusterDeploymentKind,
				Name:       cd.Name,
				UID:        cd.UID,
			},
			LabelSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					kcm.FluxHelmChartNamespaceKey: cd.Namespace,
					kcm.FluxHelmChartNameKey:      cd.Name,
				},
			},
			HelmCharts:        helmCharts,
			KustomizationRefs: kustomizationRefs,
			Priority:          cd.Spec.ServiceSpec.Priority,
			StopOnConflict:    cd.Spec.ServiceSpec.StopOnConflict,
			Reload:            cd.Spec.ServiceSpec.Reload,
			TemplateResourceRefs: append(
				getProjectTemplateResourceRefs(cd, cred), cd.Spec.ServiceSpec.TemplateResourceRefs...,
			),
			PolicyRefs:      append(getProjectPolicyRefs(cd, cred), policyRefs...),
			SyncMode:        cd.Spec.ServiceSpec.SyncMode,
			DriftIgnore:     cd.Spec.ServiceSpec.DriftIgnore,
			DriftExclusions: cd.Spec.ServiceSpec.DriftExclusions,
			ContinueOnError: cd.Spec.ServiceSpec.ContinueOnError,
		}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile Profile: %w", err)
	}

	metrics.TrackMetricTemplateUsage(ctx, kcm.ClusterTemplateKind, cd.Spec.Template, kcm.ClusterDeploymentKind, cd.ObjectMeta, true)

	for _, svc := range cd.Spec.ServiceSpec.Services {
		metrics.TrackMetricTemplateUsage(ctx, kcm.ServiceTemplateKind, svc.Template, kcm.ClusterDeploymentKind, cd.ObjectMeta, true)
	}

	// NOTE:
	// We are returning nil in the return statements whenever servicesErr != nil
	// because we don't want the error content in servicesErr to be assigned to err.
	// The servicesErr var is joined with err in the defer func() so this function
	// will ultimately return the error in servicesErr instead of nil.
	profile := sveltosv1beta1.Profile{}
	profileRef := client.ObjectKey{Name: cd.Name, Namespace: cd.Namespace}
	if servicesErr = r.Client.Get(ctx, profileRef, &profile); servicesErr != nil {
		servicesErr = fmt.Errorf("failed to get Profile %s to fetch status from its associated ClusterSummary: %w", profileRef.String(), servicesErr)
		return ctrl.Result{}, nil
	}

	if len(cd.Spec.ServiceSpec.Services) == 0 {
		cd.Status.Services = nil
	} else {
		var servicesStatus []kcm.ServiceStatus
		servicesStatus, servicesErr = updateServicesStatus(ctx, r.Client, profileRef, profile.Status.MatchingClusterRefs, cd.Status.Services)
		if servicesErr != nil {
			return ctrl.Result{}, nil
		}
		cd.Status.Services = servicesStatus
		l.Info("Successfully updated status of services")
	}

	return ctrl.Result{}, nil
}

// updateStatus updates the status for the ClusterDeployment object.
func (r *ClusterDeploymentReconciler) updateStatus(ctx context.Context, cd *kcm.ClusterDeployment, template *kcm.ClusterTemplate) error {
	apimeta.SetStatusCondition(cd.GetConditions(), getServicesReadinessCondition(cd.Status.Services, len(cd.Spec.ServiceSpec.Services)))

	cd.Status.ObservedGeneration = cd.Generation
	cd.Status.Conditions = updateStatusConditions(cd.Status.Conditions)

	if err := r.setAvailableUpgrades(ctx, cd, template); err != nil {
		return errors.New("failed to set available upgrades")
	}

	if err := r.Client.Status().Update(ctx, cd); err != nil {
		return fmt.Errorf("failed to update status for clusterDeployment %s/%s: %w", cd.Namespace, cd.Name, err)
	}

	return nil
}

func (r *ClusterDeploymentReconciler) getSourceArtifact(ctx context.Context, ref *hcv2.CrossNamespaceSourceReference) (*sourcev1.Artifact, error) {
	if ref == nil {
		return nil, errors.New("helm chart source is not provided")
	}

	key := client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}
	hc := new(sourcev1.HelmChart)
	if err := r.Client.Get(ctx, key, hc); err != nil {
		return nil, fmt.Errorf("failed to get HelmChart %s: %w", key, err)
	}

	return hc.GetArtifact(), nil
}

func (r *ClusterDeploymentReconciler) reconcileDelete(ctx context.Context, cd *kcm.ClusterDeployment) (result ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)

	defer func() {
		if err == nil {
			metrics.TrackMetricTemplateUsage(ctx, kcm.ClusterTemplateKind, cd.Spec.Template, kcm.ClusterDeploymentKind, cd.ObjectMeta, false)

			for _, svc := range cd.Spec.ServiceSpec.Services {
				metrics.TrackMetricTemplateUsage(ctx, kcm.ServiceTemplateKind, svc.Template, kcm.ClusterDeploymentKind, cd.ObjectMeta, false)
			}
		}
		err = errors.Join(err, r.updateStatus(ctx, cd, nil))
	}()

	if _, err = r.aggregateCapiConditions(ctx, cd); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to aggregate conditions from CAPI Cluster for ClusterDeployment %s: %w", client.ObjectKeyFromObject(cd), err)
	}

	// Without explicitly deleting the Profile object, we run into a race condition
	// which prevents Sveltos objects from being removed from the management cluster.
	// It is detailed in https://github.com/projectsveltos/addon-controller/issues/732.
	// We may try to remove the explicit call to Delete once a fix for it has been merged.
	// TODO(https://github.com/K0rdent/kcm/issues/526).
	if err := sveltos.DeleteProfile(ctx, r.Client, cd.Namespace, cd.Name); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.releaseProviderCluster(ctx, cd); err != nil {
		if r.IsDisabledValidationWH && errors.Is(err, errClusterTemplateNotFound) {
			r.setCondition(cd, kcm.DeletingCondition, err)
			l.Error(err, "failed to release provider cluster object due to absent ClusterTemplate, will not retrigger")
			// there is not much to do, we cannot release the clusterdeployment without the clustertemplate
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	err = r.Client.Get(ctx, client.ObjectKeyFromObject(cd), &hcv2.HelmRelease{})
	if err == nil { // if NO error
		if err := helm.DeleteHelmRelease(ctx, r.Client, cd.Name, cd.Namespace); err != nil {
			r.setCondition(cd, kcm.DeletingCondition, err)
			return ctrl.Result{}, err
		}

		l.Info("HelmRelease still exists, retrying")
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}
	if !apierrors.IsNotFound(err) {
		r.setCondition(cd, kcm.DeletingCondition, err)
		return ctrl.Result{}, err
	}
	r.eventf(cd, "HelmReleaseDeleted", "HelmRelease %s has been deleted", client.ObjectKeyFromObject(cd))

	cluster := &metav1.PartialObjectMetadata{}
	cluster.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "Cluster",
	})

	err = r.Client.Get(ctx, client.ObjectKeyFromObject(cd), cluster)
	if err == nil { // if NO error
		l.Info("Cluster still exists, retrying", "cluster name", client.ObjectKeyFromObject(cluster))
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}
	if !apierrors.IsNotFound(err) {
		r.setCondition(cd, kcm.DeletingCondition, err)
		l.Error(err, "failed to get Cluster")
		return ctrl.Result{}, err
	}

	r.setCondition(cd, kcm.DeletingCondition, nil)
	if controllerutil.RemoveFinalizer(cd, kcm.ClusterDeploymentFinalizer) {
		l.Info("Removing Finalizer", "finalizer", kcm.ClusterDeploymentFinalizer)
		if err := r.Client.Update(ctx, cd); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update clusterDeployment %s: %w", client.ObjectKeyFromObject(cd), err)
		}
	}
	r.eventf(cd, "ClusterDeleted", "Cluster %s has been deleted", client.ObjectKeyFromObject(cd))

	r.eventf(cd, "SuccessfulDelete", "ClusterDeployment has been deleted")
	l.Info("ClusterDeployment deleted")

	return ctrl.Result{}, nil
}

func (r *ClusterDeploymentReconciler) releaseProviderCluster(ctx context.Context, cd *kcm.ClusterDeployment) error {
	providers, err := r.getInfraProvidersNames(ctx, cd.Namespace, cd.Spec.Template)
	if err != nil {
		return err
	}

	// Associate the provider with it's GVK
	for _, provider := range providers {
		gvks := providersloader.GetClusterGVKs(provider)
		if len(gvks) == 0 {
			continue
		}

		cluster, err := r.getProviderCluster(ctx, cd.Namespace, cd.Name, gvks...)
		if err != nil {
			if !errors.Is(err, errClusterNotFound) {
				return err
			}
			return nil
		}

		found, err := r.clusterCAPIMachinesExist(ctx, cd.Namespace, cluster.Name)
		if err != nil {
			continue
		}

		if !found {
			if err := r.removeClusterFinalizer(ctx, cluster); err != nil {
				return fmt.Errorf("failed to remove finalizer from %s %s: %w", cluster.Kind, client.ObjectKeyFromObject(cluster), err)
			}
		}
	}

	return nil
}

func (r *ClusterDeploymentReconciler) getInfraProvidersNames(ctx context.Context, templateNamespace, templateName string) ([]string, error) {
	template := &kcm.ClusterTemplate{}
	templateRef := client.ObjectKey{Name: templateName, Namespace: templateNamespace}
	if err := r.Client.Get(ctx, templateRef, template); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "Failed to get ClusterTemplate", "template namespace", templateNamespace, "template name", templateName)
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get ClusterTemplate %s: %w", templateRef, errClusterTemplateNotFound)
		}
		return nil, err
	}

	var (
		ips     = make([]string, 0, len(template.Status.Providers))
		lprefix = len(providersloader.InfraPrefix)
	)
	for _, v := range template.Status.Providers {
		if idx := strings.Index(v, providersloader.InfraPrefix); idx > -1 {
			ips = append(ips, v[idx+lprefix:])
		}
	}

	return ips[:len(ips):len(ips)], nil
}

// getProviderCluster fetches a first provider Cluster from the given list of GVKs.
func (r *ClusterDeploymentReconciler) getProviderCluster(ctx context.Context, namespace, name string, gvks ...schema.GroupVersionKind) (*metav1.PartialObjectMetadata, error) {
	for _, gvk := range gvks {
		itemsList := &metav1.PartialObjectMetadataList{}
		itemsList.SetGroupVersionKind(gvk)
		if err := r.Client.List(ctx, itemsList, client.InNamespace(namespace), client.MatchingLabels{kcm.FluxHelmChartNameKey: name}); err != nil {
			return nil, fmt.Errorf("failed to list %s in namespace %s: %w", gvk.Kind, namespace, err)
		}

		if len(itemsList.Items) > 0 {
			return &itemsList.Items[0], nil
		}
	}

	return nil, errClusterNotFound
}

func (r *ClusterDeploymentReconciler) removeClusterFinalizer(ctx context.Context, cluster *metav1.PartialObjectMetadata) error {
	originalCluster := *cluster
	if controllerutil.RemoveFinalizer(cluster, kcm.BlockingFinalizer) {
		ctrl.LoggerFrom(ctx).Info("Allow to stop cluster", "finalizer", kcm.BlockingFinalizer)
		if err := r.Client.Patch(ctx, cluster, client.MergeFrom(&originalCluster)); err != nil {
			return fmt.Errorf("failed to patch cluster %s/%s: %w", cluster.Namespace, cluster.Name, err)
		}
	}

	return nil
}

func (r *ClusterDeploymentReconciler) clusterCAPIMachinesExist(ctx context.Context, namespace, clusterName string) (bool, error) {
	gvkMachine := schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "Machine",
	}

	itemsList := &metav1.PartialObjectMetadataList{}
	itemsList.SetGroupVersionKind(gvkMachine)
	if err := r.Client.List(ctx, itemsList, client.InNamespace(namespace), client.Limit(1), client.MatchingLabels{clusterapiv1.ClusterNameLabel: clusterName}); err != nil {
		return false, err
	}
	return len(itemsList.Items) != 0, nil
}

func (r *ClusterDeploymentReconciler) setAvailableUpgrades(ctx context.Context, clusterDeployment *kcm.ClusterDeployment, clusterTpl *kcm.ClusterTemplate) error {
	if clusterTpl == nil {
		return nil
	}

	chains := new(kcm.ClusterTemplateChainList)
	if err := r.Client.List(ctx, chains,
		client.InNamespace(clusterTpl.Namespace),
		client.MatchingFields{kcm.TemplateChainSupportedTemplatesIndexKey: clusterTpl.Name},
	); err != nil {
		return fmt.Errorf("failed to list ClusterTemplateChains: %w", err)
	}

	availableUpgradesMap := make(map[string]kcm.AvailableUpgrade)
	for _, chain := range chains.Items {
		for _, supportedTemplate := range chain.Spec.SupportedTemplates {
			if supportedTemplate.Name == clusterTpl.Name {
				for _, availableUpgrade := range supportedTemplate.AvailableUpgrades {
					availableUpgradesMap[availableUpgrade.Name] = availableUpgrade
				}
			}
		}
	}

	availableUpgrades := make([]string, 0, len(availableUpgradesMap))
	for _, availableUpgrade := range availableUpgradesMap {
		availableUpgrades = append(availableUpgrades, availableUpgrade.Name)
	}

	clusterDeployment.Status.AvailableUpgrades = availableUpgrades
	return nil
}

// templatesValidUpdateSource is a source of update and create events which enqueues ClusterDeployment objects if the referenced ServiceTemplate or ClusterTemplate object gets the valid status.
func (*ClusterDeploymentReconciler) templatesValidUpdateSource(cl client.Client, cache crcache.Cache, obj client.Object) source.TypedSource[ctrl.Request] {
	var isServiceTemplateKind bool // quick kludge to avoid complicated switches
	var indexKey string

	switch obj.(type) {
	case *kcm.ServiceTemplate:
		isServiceTemplateKind = true
		indexKey = kcm.ClusterDeploymentServiceTemplatesIndexKey
	case *kcm.ClusterTemplate:
		indexKey = kcm.ClusterDeploymentTemplateIndexKey
	default:
		panic(fmt.Sprintf("unexpected type %T, expected one of [%T, %T]", obj, new(kcm.ServiceTemplate), new(kcm.ClusterTemplate)))
	}

	return source.TypedKind(cache, obj, handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
		clds := new(kcm.ClusterDeploymentList)
		if err := cl.List(ctx, clds, client.InNamespace(o.GetNamespace()), client.MatchingFields{indexKey: o.GetName()}); err != nil {
			return nil
		}

		resp := make([]ctrl.Request, 0, len(clds.Items))
		for _, v := range clds.Items {
			resp = append(resp, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&v)})
		}

		return resp
	}), predicate.TypedFuncs[client.Object]{
		GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
		DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
		UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
			// NOTE: might be optimized, probably with go's core types gone (>=go1.25)
			if isServiceTemplateKind {
				sto, ok := tue.ObjectOld.(*kcm.ServiceTemplate)
				if !ok {
					return false
				}
				stn, ok := tue.ObjectNew.(*kcm.ServiceTemplate)
				if !ok {
					return false
				}
				return stn.Status.Valid && !sto.Status.Valid
			}

			cto, ok := tue.ObjectOld.(*kcm.ClusterTemplate)
			if !ok {
				return false
			}
			ctn, ok := tue.ObjectNew.(*kcm.ClusterTemplate)
			if !ok {
				return false
			}
			return ctn.Status.Valid && !cto.Status.Valid
		},
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.Config = mgr.GetConfig()

	r.helmActor = helm.NewActor(r.Config, r.Client.RESTMapper())

	r.defaultRequeueTime = 10 * time.Second

	managedController := ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcm.ClusterDeployment{}).
		Watches(&hcv2.HelmRelease{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
				clusterDeploymentRef := client.ObjectKeyFromObject(o)
				if err := r.Client.Get(ctx, clusterDeploymentRef, &kcm.ClusterDeployment{}); err != nil {
					return []ctrl.Request{}
				}

				return []ctrl.Request{{NamespacedName: clusterDeploymentRef}}
			}),
		).
		Watches(&kcm.ClusterTemplateChain{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
				chain, ok := o.(*kcm.ClusterTemplateChain)
				if !ok {
					return nil
				}

				var req []ctrl.Request
				for _, template := range getTemplateNamesManagedByChain(chain) {
					clusterDeployments := &kcm.ClusterDeploymentList{}
					err := r.Client.List(ctx, clusterDeployments,
						client.InNamespace(chain.Namespace),
						client.MatchingFields{kcm.ClusterDeploymentTemplateIndexKey: template})
					if err != nil {
						return []ctrl.Request{}
					}
					for _, cluster := range clusterDeployments.Items {
						req = append(req, ctrl.Request{
							NamespacedName: client.ObjectKey{
								Namespace: cluster.Namespace,
								Name:      cluster.Name,
							},
						})
					}
				}
				return req
			}),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc:  func(event.UpdateEvent) bool { return false },
				GenericFunc: func(event.GenericEvent) bool { return false },
			}),
		).
		Watches(&sveltosv1beta1.ClusterSummary{},
			handler.EnqueueRequestsFromMapFunc(requeueSveltosProfileForClusterSummary),
			builder.WithPredicates(predicate.Funcs{
				DeleteFunc:  func(event.DeleteEvent) bool { return false },
				GenericFunc: func(event.GenericEvent) bool { return false },
			}),
		).
		Watches(&kcm.Credential{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
				clusterDeployments := &kcm.ClusterDeploymentList{}
				err := r.Client.List(ctx, clusterDeployments,
					client.InNamespace(o.GetNamespace()),
					client.MatchingFields{kcm.ClusterDeploymentCredentialIndexKey: o.GetName()})
				if err != nil {
					return []ctrl.Request{}
				}

				req := []ctrl.Request{}
				for _, cluster := range clusterDeployments.Items {
					req = append(req, ctrl.Request{
						NamespacedName: client.ObjectKey{
							Namespace: cluster.Namespace,
							Name:      cluster.Name,
						},
					})
				}

				return req
			}),
		)

	if r.IsDisabledValidationWH {
		setupLog := mgr.GetLogger().WithName("clusterdeployment_ctrl_setup")
		managedController.WatchesRawSource(r.templatesValidUpdateSource(mgr.GetClient(), mgr.GetCache(), &kcm.ServiceTemplate{}))
		setupLog.Info("Validations are disabled, watcher for ServiceTemplate objects is set")
		managedController.WatchesRawSource(r.templatesValidUpdateSource(mgr.GetClient(), mgr.GetCache(), &kcm.ClusterTemplate{}))
		setupLog.Info("Validations are disabled, watcher for ClusterTemplate objects is set")
	}

	return managedController.Complete(r)
}

func (*ClusterDeploymentReconciler) eventf(cd *kcm.ClusterDeployment, reason, message string, args ...any) {
	record.Eventf(cd, cd.Generation, reason, message, args...)
}

func (*ClusterDeploymentReconciler) warnf(cd *kcm.ClusterDeployment, reason, message string, args ...any) {
	record.Warnf(cd, cd.Generation, reason, message, args...)
}
