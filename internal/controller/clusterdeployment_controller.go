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
	"strings"
	"time"

	hcv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	fluxconditions "github.com/fluxcd/pkg/runtime/conditions"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sveltosv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/helm"
	providersloader "github.com/K0rdent/kcm/internal/providers"
	"github.com/K0rdent/kcm/internal/sveltos"
	"github.com/K0rdent/kcm/internal/telemetry"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/status"
	"github.com/K0rdent/kcm/internal/webhook"
)

const (
	DefaultRequeueInterval = 10 * time.Second
)

var ErrClusterNotFound = errors.New("cluster is not found")

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
		return r.Delete(ctx, clusterDeployment)
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

func (r *ClusterDeploymentReconciler) setStatusFromChildObjects(ctx context.Context, clusterDeployment *kcm.ClusterDeployment, gvr schema.GroupVersionResource, conditions []string) (requeue bool, _ error) {
	l := ctrl.LoggerFrom(ctx)

	resourceConditions, err := status.GetResourceConditions(ctx, clusterDeployment.Namespace, r.DynamicClient, gvr,
		labels.SelectorFromSet(map[string]string{kcm.FluxHelmChartNameKey: clusterDeployment.Name}).String())
	if err != nil {
		if errors.As(err, &status.ResourceNotFoundError{}) {
			l.Info(err.Error())
			// don't error or retry if nothing is available
			return false, nil
		}
		return false, fmt.Errorf("failed to get conditions: %w", err)
	}

	allConditionsComplete := true
	for _, metaCondition := range resourceConditions.Conditions {
		if slices.Contains(conditions, metaCondition.Type) {
			if metaCondition.Status != metav1.ConditionTrue {
				allConditionsComplete = false
			}

			if metaCondition.Reason == "" && metaCondition.Status == metav1.ConditionTrue {
				metaCondition.Message += " is Ready"
				metaCondition.Reason = kcm.SucceededReason
			}
			apimeta.SetStatusCondition(clusterDeployment.GetConditions(), metaCondition)
		}
	}

	return !allConditionsComplete, nil
}

func (r *ClusterDeploymentReconciler) reconcileUpdate(ctx context.Context, mc *kcm.ClusterDeployment) (_ ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)

	if controllerutil.AddFinalizer(mc, kcm.ClusterDeploymentFinalizer) {
		if err := r.Client.Update(ctx, mc); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update clusterDeployment %s/%s: %w", mc.Namespace, mc.Name, err)
		}
		return ctrl.Result{}, nil
	}

	if updated, err := utils.AddKCMComponentLabel(ctx, r.Client, mc); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{}, err
	}

	if len(mc.Status.Conditions) == 0 {
		mc.InitConditions()
	}

	clusterTpl := &kcm.ClusterTemplate{}

	defer func() {
		err = errors.Join(err, r.updateStatus(ctx, mc, clusterTpl))
	}()

	if err = r.Client.Get(ctx, client.ObjectKey{Name: mc.Spec.Template, Namespace: mc.Namespace}, clusterTpl); err != nil {
		l.Error(err, "Failed to get Template")
		errMsg := fmt.Sprintf("failed to get provided template: %s", err)
		if apierrors.IsNotFound(err) {
			errMsg = "provided template is not found"
		}
		apimeta.SetStatusCondition(mc.GetConditions(), metav1.Condition{
			Type:    kcm.TemplateReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  kcm.FailedReason,
			Message: errMsg,
		})
		return ctrl.Result{}, err
	}

	clusterRes, clusterErr := r.updateCluster(ctx, mc, clusterTpl)
	servicesRes, servicesErr := r.updateServices(ctx, mc)

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

func (r *ClusterDeploymentReconciler) updateCluster(ctx context.Context, mc *kcm.ClusterDeployment, clusterTpl *kcm.ClusterTemplate) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	if clusterTpl == nil {
		return ctrl.Result{}, errors.New("cluster template cannot be nil")
	}

	if !clusterTpl.Status.Valid {
		errMsg := "provided template is not marked as valid"
		apimeta.SetStatusCondition(mc.GetConditions(), metav1.Condition{
			Type:    kcm.TemplateReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  kcm.FailedReason,
			Message: errMsg,
		})
		return ctrl.Result{}, errors.New(errMsg)
	}
	// template is ok, propagate data from it
	mc.Status.KubernetesVersion = clusterTpl.Status.KubernetesVersion

	apimeta.SetStatusCondition(mc.GetConditions(), metav1.Condition{
		Type:    kcm.TemplateReadyCondition,
		Status:  metav1.ConditionTrue,
		Reason:  kcm.SucceededReason,
		Message: "Template is valid",
	})

	source, err := r.getSource(ctx, clusterTpl.Status.ChartRef)
	if err != nil {
		apimeta.SetStatusCondition(mc.GetConditions(), metav1.Condition{
			Type:    kcm.HelmChartReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  kcm.FailedReason,
			Message: fmt.Sprintf("failed to get helm chart source: %s", err),
		})
		return ctrl.Result{}, err
	}
	l.Info("Downloading Helm chart")
	hcChart, err := r.DownloadChartFromArtifact(ctx, source.GetArtifact())
	if err != nil {
		apimeta.SetStatusCondition(mc.GetConditions(), metav1.Condition{
			Type:    kcm.HelmChartReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  kcm.FailedReason,
			Message: fmt.Sprintf("failed to download helm chart: %s", err),
		})
		return ctrl.Result{}, err
	}

	l.Info("Initializing Helm client")
	actionConfig, err := r.InitializeConfiguration(mc, l.Info)
	if err != nil {
		return ctrl.Result{}, err
	}

	l.Info("Validating Helm chart with provided values")
	if err = r.EnsureReleaseWithValues(ctx, actionConfig, hcChart, mc); err != nil {
		apimeta.SetStatusCondition(mc.GetConditions(), metav1.Condition{
			Type:    kcm.HelmChartReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  kcm.FailedReason,
			Message: fmt.Sprintf("failed to validate template with provided configuration: %s", err),
		})
		return ctrl.Result{}, err
	}

	apimeta.SetStatusCondition(mc.GetConditions(), metav1.Condition{
		Type:    kcm.HelmChartReadyCondition,
		Status:  metav1.ConditionTrue,
		Reason:  kcm.SucceededReason,
		Message: "Helm chart is valid",
	})

	cred := &kcm.Credential{}
	err = r.Client.Get(ctx, client.ObjectKey{
		Name:      mc.Spec.Credential,
		Namespace: mc.Namespace,
	}, cred)
	if err != nil {
		apimeta.SetStatusCondition(mc.GetConditions(), metav1.Condition{
			Type:    kcm.CredentialReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  kcm.FailedReason,
			Message: fmt.Sprintf("Failed to get Credential: %s", err),
		})
		return ctrl.Result{}, err
	}

	if !cred.Status.Ready {
		apimeta.SetStatusCondition(mc.GetConditions(), metav1.Condition{
			Type:    kcm.CredentialReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  kcm.FailedReason,
			Message: "Credential is not in Ready state",
		})
	}

	apimeta.SetStatusCondition(mc.GetConditions(), metav1.Condition{
		Type:    kcm.CredentialReadyCondition,
		Status:  metav1.ConditionTrue,
		Reason:  kcm.SucceededReason,
		Message: "Credential is Ready",
	})

	if mc.Spec.DryRun {
		return ctrl.Result{}, nil
	}

	if err := mc.AddHelmValues(func(values map[string]any) error {
		values["clusterIdentity"] = cred.Spec.IdentityRef

		if _, ok := values["clusterLabels"]; !ok {
			// Use the ManagedCluster's own labels if not defined.
			values["clusterLabels"] = mc.GetObjectMeta().GetLabels()
		}

		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}

	hrReconcileOpts := helm.ReconcileHelmReleaseOpts{
		Values: mc.Spec.Config,
		OwnerReference: &metav1.OwnerReference{
			APIVersion: kcm.GroupVersion.String(),
			Kind:       kcm.ClusterDeploymentKind,
			Name:       mc.Name,
			UID:        mc.UID,
		},
		ChartRef: clusterTpl.Status.ChartRef,
	}
	if clusterTpl.Spec.Helm.ChartSpec != nil {
		hrReconcileOpts.ReconcileInterval = &clusterTpl.Spec.Helm.ChartSpec.Interval.Duration
	}

	hr, _, err := helm.ReconcileHelmRelease(ctx, r.Client, mc.Name, mc.Namespace, hrReconcileOpts)
	if err != nil {
		apimeta.SetStatusCondition(mc.GetConditions(), metav1.Condition{
			Type:    kcm.HelmReleaseReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  kcm.FailedReason,
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	hrReadyCondition := fluxconditions.Get(hr, fluxmeta.ReadyCondition)
	if hrReadyCondition != nil {
		apimeta.SetStatusCondition(mc.GetConditions(), metav1.Condition{
			Type:    kcm.HelmReleaseReadyCondition,
			Status:  hrReadyCondition.Status,
			Reason:  hrReadyCondition.Reason,
			Message: hrReadyCondition.Message,
		})
	}

	requeue, err := r.aggregateCapoConditions(ctx, mc)
	if err != nil {
		if requeue {
			return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, err
		}

		return ctrl.Result{}, err
	}

	if requeue {
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, nil
	}

	if !fluxconditions.IsReady(hr) {
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ClusterDeploymentReconciler) aggregateCapoConditions(ctx context.Context, clusterDeployment *kcm.ClusterDeployment) (requeue bool, _ error) {
	type objectToCheck struct {
		gvr        schema.GroupVersionResource
		conditions []string
	}

	var errs error
	for _, obj := range []objectToCheck{
		{
			gvr: schema.GroupVersionResource{
				Group:    "cluster.x-k8s.io",
				Version:  "v1beta1",
				Resource: "clusters",
			},
			conditions: []string{"ControlPlaneInitialized", "ControlPlaneReady", "InfrastructureReady"},
		},
		{
			gvr: schema.GroupVersionResource{
				Group:    "cluster.x-k8s.io",
				Version:  "v1beta1",
				Resource: "machinedeployments",
			},
			conditions: []string{"Available"},
		},
	} {
		needRequeue, err := r.setStatusFromChildObjects(ctx, clusterDeployment, obj.gvr, obj.conditions)
		errs = errors.Join(errs, err)
		if needRequeue {
			requeue = true
		}
	}

	return requeue, errs
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

// updateServices reconciles services provided in ClusterDeployment.Spec.Services.
func (r *ClusterDeploymentReconciler) updateServices(ctx context.Context, mc *kcm.ClusterDeployment) (_ ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling Services")

	// servicesErr is handled separately from err because we do not want
	// to set the condition of SveltosProfileReady type to "False"
	// if there is an error while retrieving status for the services.
	var servicesErr error

	defer func() {
		condition := metav1.Condition{
			Reason: kcm.SucceededReason,
			Status: metav1.ConditionTrue,
			Type:   kcm.SveltosProfileReadyCondition,
		}
		if err != nil {
			condition.Message = err.Error()
			condition.Reason = kcm.FailedReason
			condition.Status = metav1.ConditionFalse
		}
		apimeta.SetStatusCondition(&mc.Status.Conditions, condition)

		servicesCondition := metav1.Condition{
			Reason: kcm.SucceededReason,
			Status: metav1.ConditionTrue,
			Type:   kcm.FetchServicesStatusSuccessCondition,
		}
		if servicesErr != nil {
			servicesCondition.Message = servicesErr.Error()
			servicesCondition.Reason = kcm.FailedReason
			servicesCondition.Status = metav1.ConditionFalse
		}
		apimeta.SetStatusCondition(&mc.Status.Conditions, servicesCondition)

		err = errors.Join(err, servicesErr)
	}()

	if err := webhook.ValidateCrossNamespaceRefs(ctx, mc.Namespace, &mc.Spec.ServiceSpec); err != nil {
		return ctrl.Result{}, err
	}

	opts, err := sveltos.GetHelmChartOpts(ctx, r.Client, mc.Namespace, mc.Spec.ServiceSpec.Services)
	if err != nil {
		return ctrl.Result{}, err
	}

	cred := &kcm.Credential{}
	err = r.Client.Get(ctx, client.ObjectKey{
		Name:      mc.Spec.Credential,
		Namespace: mc.Namespace,
	}, cred)
	if err != nil {
		return ctrl.Result{}, err
	}

	if _, err = sveltos.ReconcileProfile(ctx, r.Client, mc.Namespace, mc.Name,
		sveltos.ReconcileProfileOpts{
			OwnerReference: &metav1.OwnerReference{
				APIVersion: kcm.GroupVersion.String(),
				Kind:       kcm.ClusterDeploymentKind,
				Name:       mc.Name,
				UID:        mc.UID,
			},
			LabelSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					kcm.FluxHelmChartNamespaceKey: mc.Namespace,
					kcm.FluxHelmChartNameKey:      mc.Name,
				},
			},
			HelmChartOpts:  opts,
			Priority:       mc.Spec.ServiceSpec.Priority,
			StopOnConflict: mc.Spec.ServiceSpec.StopOnConflict,
			Reload:         mc.Spec.ServiceSpec.Reload,
			TemplateResourceRefs: append(
				getProjectTemplateResourceRefs(mc, cred), mc.Spec.ServiceSpec.TemplateResourceRefs...,
			),
			PolicyRefs:      getProjectPolicyRefs(mc, cred),
			SyncMode:        mc.Spec.ServiceSpec.SyncMode,
			DriftIgnore:     mc.Spec.ServiceSpec.DriftIgnore,
			DriftExclusions: mc.Spec.ServiceSpec.DriftExclusions,
		}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile Profile: %w", err)
	}

	// NOTE:
	// We are returning nil in the return statements whenever servicesErr != nil
	// because we don't want the error content in servicesErr to be assigned to err.
	// The servicesErr var is joined with err in the defer func() so this function
	// will ultimately return the error in servicesErr instead of nil.
	profile := sveltosv1beta1.Profile{}
	profileRef := client.ObjectKey{Name: mc.Name, Namespace: mc.Namespace}
	if servicesErr = r.Client.Get(ctx, profileRef, &profile); servicesErr != nil {
		servicesErr = fmt.Errorf("failed to get Profile %s to fetch status from its associated ClusterSummary: %w", profileRef.String(), servicesErr)
		return ctrl.Result{}, nil
	}

	var servicesStatus []kcm.ServiceStatus
	servicesStatus, servicesErr = updateServicesStatus(ctx, r.Client, profileRef, profile.Status.MatchingClusterRefs, mc.Status.Services)
	if servicesErr != nil {
		return ctrl.Result{}, nil
	}
	mc.Status.Services = servicesStatus
	l.Info("Successfully updated status of services")

	return ctrl.Result{}, nil
}

// updateStatus updates the status for the ClusterDeployment object.
func (r *ClusterDeploymentReconciler) updateStatus(ctx context.Context, clusterDeployment *kcm.ClusterDeployment, template *kcm.ClusterTemplate) error {
	clusterDeployment.Status.ObservedGeneration = clusterDeployment.Generation
	clusterDeployment.Status.Conditions = updateStatusConditions(clusterDeployment.Status.Conditions, "ClusterDeployment is ready")

	if err := r.setAvailableUpgrades(ctx, clusterDeployment, template); err != nil {
		return errors.New("failed to set available upgrades")
	}

	if err := r.Client.Status().Update(ctx, clusterDeployment); err != nil {
		return fmt.Errorf("failed to update status for clusterDeployment %s/%s: %w", clusterDeployment.Namespace, clusterDeployment.Name, err)
	}

	return nil
}

func (r *ClusterDeploymentReconciler) getSource(ctx context.Context, ref *hcv2.CrossNamespaceSourceReference) (sourcev1.Source, error) {
	if ref == nil {
		return nil, errors.New("helm chart source is not provided")
	}
	chartRef := client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}
	hc := sourcev1.HelmChart{}
	if err := r.Client.Get(ctx, chartRef, &hc); err != nil {
		return nil, err
	}
	return &hc, nil
}

func (r *ClusterDeploymentReconciler) Delete(ctx context.Context, clusterDeployment *kcm.ClusterDeployment) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	hr := &hcv2.HelmRelease{}

	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(clusterDeployment), hr); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		l.Info("Removing Finalizer", "finalizer", kcm.ClusterDeploymentFinalizer)
		if controllerutil.RemoveFinalizer(clusterDeployment, kcm.ClusterDeploymentFinalizer) {
			if err := r.Client.Update(ctx, clusterDeployment); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update clusterDeployment %s/%s: %w", clusterDeployment.Namespace, clusterDeployment.Name, err)
			}
		}
		l.Info("ClusterDeployment deleted")
		return ctrl.Result{}, nil
	}

	if err := helm.DeleteHelmRelease(ctx, r.Client, clusterDeployment.Name, clusterDeployment.Namespace); err != nil {
		return ctrl.Result{}, err
	}

	// Without explicitly deleting the Profile object, we run into a race condition
	// which prevents Sveltos objects from being removed from the management cluster.
	// It is detailed in https://github.com/projectsveltos/addon-controller/issues/732.
	// We may try to remove the explicit call to Delete once a fix for it has been merged.
	// TODO(https://github.com/K0rdent/kcm/issues/526).
	if err := sveltos.DeleteProfile(ctx, r.Client, clusterDeployment.Namespace, clusterDeployment.Name); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.releaseCluster(ctx, clusterDeployment.Namespace, clusterDeployment.Name, clusterDeployment.Spec.Template); err != nil {
		return ctrl.Result{}, err
	}

	l.Info("HelmRelease still exists, retrying")
	return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, nil
}

func (r *ClusterDeploymentReconciler) releaseCluster(ctx context.Context, namespace, name, templateName string) error {
	providers, err := r.getInfraProvidersNames(ctx, namespace, templateName)
	if err != nil {
		return err
	}

	gvkMachine := schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta1",
		Kind:    "Machine",
	}

	// Associate the provider with it's GVK
	for _, provider := range providers {
		gvks := providersloader.GetClusterGVKs(provider)
		if len(gvks) == 0 {
			continue
		}

		cluster, err := r.getCluster(ctx, namespace, name, gvks...)
		if err != nil {
			if !errors.Is(err, ErrClusterNotFound) {
				return err
			}
			return nil
		}

		found, err := r.objectsAvailable(ctx, namespace, cluster.Name, gvkMachine)
		if err != nil {
			continue
		}

		if !found {
			return r.removeClusterFinalizer(ctx, cluster)
		}
	}

	return nil
}

func (r *ClusterDeploymentReconciler) getInfraProvidersNames(ctx context.Context, templateNamespace, templateName string) ([]string, error) {
	template := &kcm.ClusterTemplate{}
	templateRef := client.ObjectKey{Name: templateName, Namespace: templateNamespace}
	if err := r.Client.Get(ctx, templateRef, template); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "Failed to get ClusterTemplate", "template namespace", templateNamespace, "template name", templateName)
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

func (r *ClusterDeploymentReconciler) getCluster(ctx context.Context, namespace, name string, gvks ...schema.GroupVersionKind) (*metav1.PartialObjectMetadata, error) {
	for _, gvk := range gvks {
		opts := &client.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{kcm.FluxHelmChartNameKey: name}),
			Namespace:     namespace,
		}
		itemsList := &metav1.PartialObjectMetadataList{}
		itemsList.SetGroupVersionKind(gvk)

		if err := r.Client.List(ctx, itemsList, opts); err != nil {
			return nil, fmt.Errorf("failed to list %s in namespace %s: %w", gvk.Kind, namespace, err)
		}

		if len(itemsList.Items) > 0 {
			return &itemsList.Items[0], nil
		}
	}

	return nil, ErrClusterNotFound
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

func (r *ClusterDeploymentReconciler) objectsAvailable(ctx context.Context, namespace, clusterName string, gvk schema.GroupVersionKind) (bool, error) {
	opts := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{kcm.ClusterNameLabelKey: clusterName}),
		Namespace:     namespace,
		Limit:         1,
	}
	itemsList := &metav1.PartialObjectMetadataList{}
	itemsList.SetGroupVersionKind(gvk)
	if err := r.Client.List(ctx, itemsList, opts); err != nil {
		return false, err
	}
	return len(itemsList.Items) != 0, nil
}

func (r *ClusterDeploymentReconciler) setAvailableUpgrades(ctx context.Context, clusterDeployment *kcm.ClusterDeployment, template *kcm.ClusterTemplate) error {
	if template == nil {
		return nil
	}
	chains := &kcm.ClusterTemplateChainList{}
	err := r.Client.List(ctx, chains,
		client.InNamespace(template.Namespace),
		client.MatchingFields{kcm.TemplateChainSupportedTemplatesIndexKey: template.GetName()},
	)
	if err != nil {
		return err
	}

	availableUpgradesMap := make(map[string]kcm.AvailableUpgrade)
	for _, chain := range chains.Items {
		for _, supportedTemplate := range chain.Spec.SupportedTemplates {
			if supportedTemplate.Name == template.Name {
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

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	r.Config = mgr.GetConfig()

	r.helmActor = helm.NewActor(r.Config, r.Client.RESTMapper())

	return ctrl.NewControllerManagedBy(mgr).
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
		).
		Complete(r)
}
