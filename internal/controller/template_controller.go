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
	"strings"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"helm.sh/helm/v3/pkg/chart"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	hmc "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/helm"
	"github.com/K0rdent/kcm/internal/utils"
)

const defaultRequeueTime = 1 * time.Minute

// TemplateReconciler reconciles a *Template object
type TemplateReconciler struct {
	client.Client

	downloadHelmChartFunc func(context.Context, *sourcev1.Artifact) (*chart.Chart, error)

	SystemNamespace       string
	DefaultRegistryConfig helm.DefaultRegistryConfig
}

type ClusterTemplateReconciler struct {
	TemplateReconciler
}

type ServiceTemplateReconciler struct {
	TemplateReconciler
}

type ProviderTemplateReconciler struct {
	TemplateReconciler
}

func (r *ClusterTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ClusterTemplate")

	clusterTemplate := new(hmc.ClusterTemplate)
	if err := r.Get(ctx, req.NamespacedName, clusterTemplate); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ClusterTemplate not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		l.Error(err, "Failed to get ClusterTemplate")
		return ctrl.Result{}, err
	}

	if utils.AddLabel(clusterTemplate, hmc.GenericComponentLabelName, hmc.GenericComponentLabelValueHMC) {
		if err := r.Update(ctx, clusterTemplate); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update labels: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil // generation has not changed, need explicit requeue
	}

	result, err := r.ReconcileTemplate(ctx, clusterTemplate)
	if err != nil {
		l.Error(err, "failed to reconcile template")
		return result, err
	}

	l.Info("Validating template compatibility attributes")
	if err := r.validateCompatibilityAttrs(ctx, clusterTemplate); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("Validation cannot be performed until Management cluster appears", "requeue in", defaultRequeueTime)
			return ctrl.Result{RequeueAfter: defaultRequeueTime}, nil
		}

		l.Error(err, "failed to validate compatibility attributes")
		return ctrl.Result{}, err
	}

	return result, nil
}

func (r *ServiceTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ServiceTemplate")

	serviceTemplate := new(hmc.ServiceTemplate)
	if err := r.Get(ctx, req.NamespacedName, serviceTemplate); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ServiceTemplate not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		l.Error(err, "Failed to get ServiceTemplate")
		return ctrl.Result{}, err
	}

	if utils.AddLabel(serviceTemplate, hmc.GenericComponentLabelName, hmc.GenericComponentLabelValueHMC) {
		if err := r.Update(ctx, serviceTemplate); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update labels: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil // generation has not changed, need explicit requeue
	}

	return r.ReconcileTemplate(ctx, serviceTemplate)
}

func (r *ProviderTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ProviderTemplate")

	providerTemplate := new(hmc.ProviderTemplate)
	if err := r.Get(ctx, req.NamespacedName, providerTemplate); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ProviderTemplate not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		l.Error(err, "Failed to get ProviderTemplate")
		return ctrl.Result{}, err
	}

	if utils.AddLabel(providerTemplate, hmc.GenericComponentLabelName, hmc.GenericComponentLabelValueHMC) {
		if err := r.Update(ctx, providerTemplate); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update labels: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil // generation has not changed, need explicit requeue
	}

	changed, err := r.setReleaseOwnership(ctx, providerTemplate)
	if err != nil {
		l.Error(err, "Failed to set OwnerReferences")
		return ctrl.Result{}, err
	}
	if changed {
		l.Info("Updating OwnerReferences with associated Releases")
		return ctrl.Result{}, r.Update(ctx, providerTemplate)
	}

	return r.ReconcileTemplate(ctx, providerTemplate)
}

func (r *ProviderTemplateReconciler) setReleaseOwnership(ctx context.Context, providerTemplate *hmc.ProviderTemplate) (changed bool, err error) {
	releases := &hmc.ReleaseList{}
	err = r.Client.List(ctx, releases,
		client.MatchingFields{hmc.ReleaseTemplatesIndexKey: providerTemplate.Name},
	)
	if err != nil {
		return changed, fmt.Errorf("failed to get associated releases: %w", err)
	}
	for _, release := range releases.Items {
		if utils.AddOwnerReference(providerTemplate, &release) {
			changed = true
		}
	}
	return changed, nil
}

type templateCommon interface {
	client.Object
	GetHelmSpec() *hmc.HelmSpec
	GetCommonStatus() *hmc.TemplateStatusCommon
	FillStatusWithProviders(map[string]string) error
}

func (r *TemplateReconciler) ReconcileTemplate(ctx context.Context, template templateCommon) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	helmSpec := template.GetHelmSpec()
	status := template.GetCommonStatus()
	var err error
	var hcChart *sourcev1.HelmChart
	if helmSpec.ChartRef != nil {
		hcChart, err = r.getHelmChartFromChartRef(ctx, helmSpec.ChartRef)
		if err != nil {
			l.Error(err, "failed to get artifact from chartRef", "chartRef", helmSpec.String())
			return ctrl.Result{}, err
		}
	} else {
		if helmSpec.ChartSpec == nil {
			err := errors.New("neither chartSpec nor chartRef is set")
			l.Error(err, "invalid helm chart reference")
			return ctrl.Result{}, err
		}
		if template.GetNamespace() == r.SystemNamespace || !templateManagedByHMC(template) {
			namespace := template.GetNamespace()
			if namespace == "" {
				namespace = r.SystemNamespace
			}
			err := helm.ReconcileHelmRepository(ctx, r.Client, hmc.DefaultRepoName, namespace, r.DefaultRegistryConfig.HelmRepositorySpec())
			if err != nil {
				l.Error(err, "Failed to reconcile default HelmRepository")
				return ctrl.Result{}, err
			}
		}
		l.Info("Reconciling helm-controller objects ")
		hcChart, err = r.reconcileHelmChart(ctx, template)
		if err != nil {
			l.Error(err, "Failed to reconcile HelmChart")
			return ctrl.Result{}, err
		}
	}
	if hcChart == nil {
		err := errors.New("HelmChart is nil")
		l.Error(err, "could not get the helm chart")
		return ctrl.Result{}, err
	}

	status.ChartRef = &helmcontrollerv2.CrossNamespaceSourceReference{
		Kind:      sourcev1.HelmChartKind,
		Name:      hcChart.Name,
		Namespace: hcChart.Namespace,
	}
	status.ChartVersion = hcChart.Spec.Version

	if reportStatus, err := helm.ShouldReportStatusOnArtifactReadiness(hcChart); err != nil {
		l.Info("HelmChart Artifact is not ready")
		if reportStatus {
			_ = r.updateStatus(ctx, template, err.Error())
		}
		return ctrl.Result{}, err
	}

	artifact := hcChart.Status.Artifact

	if r.downloadHelmChartFunc == nil {
		r.downloadHelmChartFunc = helm.DownloadChartFromArtifact
	}

	l.Info("Downloading Helm chart")
	helmChart, err := r.downloadHelmChartFunc(ctx, artifact)
	if err != nil {
		l.Error(err, "Failed to download Helm chart")
		err = fmt.Errorf("failed to download chart: %w", err)
		_ = r.updateStatus(ctx, template, err.Error())
		return ctrl.Result{}, err
	}

	l.Info("Validating Helm chart")
	if err := helmChart.Validate(); err != nil {
		l.Error(err, "Helm chart validation failed")
		_ = r.updateStatus(ctx, template, err.Error())
		return ctrl.Result{}, err
	}

	l.Info("Parsing Helm chart metadata")
	if err := fillStatusWithProviders(template, helmChart); err != nil {
		l.Error(err, "Failed to fill status with providers")
		_ = r.updateStatus(ctx, template, err.Error())
		return ctrl.Result{}, err
	}

	status.Description = helmChart.Metadata.Description

	rawValues, err := json.Marshal(helmChart.Values)
	if err != nil {
		l.Error(err, "Failed to parse Helm chart values")
		err = fmt.Errorf("failed to parse Helm chart values: %w", err)
		_ = r.updateStatus(ctx, template, err.Error())
		return ctrl.Result{}, err
	}
	status.Config = &apiextensionsv1.JSON{Raw: rawValues}

	l.Info("Chart validation completed successfully")

	return ctrl.Result{}, r.updateStatus(ctx, template, "")
}

func templateManagedByHMC(template templateCommon) bool {
	return template.GetLabels()[hmc.HMCManagedLabelKey] == hmc.HMCManagedLabelValue
}

func fillStatusWithProviders(template templateCommon, helmChart *chart.Chart) error {
	if helmChart.Metadata == nil {
		return errors.New("chart metadata is empty")
	}

	return template.FillStatusWithProviders(helmChart.Metadata.Annotations)
}

func (r *TemplateReconciler) updateStatus(ctx context.Context, template templateCommon, validationError string) error {
	status := template.GetCommonStatus()
	status.ObservedGeneration = template.GetGeneration()
	status.ValidationError = validationError
	status.Valid = validationError == ""
	err := r.Status().Update(ctx, template)
	if err != nil {
		return fmt.Errorf("failed to update status for template %s/%s: %w", template.GetNamespace(), template.GetName(), err)
	}
	return nil
}

func (r *TemplateReconciler) reconcileHelmChart(ctx context.Context, template templateCommon) (*sourcev1.HelmChart, error) {
	namespace := template.GetNamespace()
	if namespace == "" {
		namespace = r.SystemNamespace
	}
	helmChart := &sourcev1.HelmChart{
		ObjectMeta: metav1.ObjectMeta{
			Name:      template.GetName(),
			Namespace: namespace,
		},
	}

	helmSpec := template.GetHelmSpec()
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, helmChart, func() error {
		if helmChart.Labels == nil {
			helmChart.Labels = make(map[string]string)
		}

		helmChart.Labels[hmc.HMCManagedLabelKey] = hmc.HMCManagedLabelValue
		utils.AddOwnerReference(helmChart, template)

		helmChart.Spec = *helmSpec.ChartSpec
		return nil
	})

	return helmChart, err
}

func (r *TemplateReconciler) getHelmChartFromChartRef(ctx context.Context, chartRef *helmcontrollerv2.CrossNamespaceSourceReference) (*sourcev1.HelmChart, error) {
	if chartRef.Kind != sourcev1.HelmChartKind {
		return nil, fmt.Errorf("invalid chartRef.Kind: %s. Only HelmChart kind is supported", chartRef.Kind)
	}
	helmChart := &sourcev1.HelmChart{}
	err := r.Get(ctx, client.ObjectKey{
		Namespace: chartRef.Namespace,
		Name:      chartRef.Name,
	}, helmChart)
	if err != nil {
		return nil, err
	}
	return helmChart, nil
}

func (r *ClusterTemplateReconciler) validateCompatibilityAttrs(ctx context.Context, template *hmc.ClusterTemplate) error {
	management := new(hmc.Management)
	if err := r.Client.Get(ctx, client.ObjectKey{Name: hmc.ManagementName}, management); err != nil {
		if apierrors.IsNotFound(err) {
			_ = r.updateStatus(ctx, template, "Waiting for Management creation to complete validation")
			return err
		}

		err = fmt.Errorf("failed to get Management: %w", err)
		_ = r.updateStatus(ctx, template, err.Error())
		return err
	}

	exposedProviders, requiredProviders := management.Status.AvailableProviders, template.Status.Providers

	l := ctrl.LoggerFrom(ctx)
	l.V(1).Info("providers to check", "exposed", exposedProviders, "required", requiredProviders)

	var (
		merr          error
		missing       []string
		nonSatisfying []string
	)
	for _, v := range requiredProviders {
		if !slices.Contains(exposedProviders, v) {
			missing = append(missing, v)
			continue
		}
	}

	// already validated contract versions format
	for providerName, requiredContract := range template.Status.ProviderContracts {
		l.V(1).Info("validating contracts", "exposed_provider_capi_contracts", management.Status.CAPIContracts, "required_provider_name", providerName)

		providerCAPIContracts, ok := management.Status.CAPIContracts[providerName] // capi_version: provider_version(s)
		if !ok {
			continue // both the provider and cluster templates contract versions must be set for the validation
		}

		var exposedProviderContracts []string
		for _, supportedVersions := range providerCAPIContracts {
			exposedProviderContracts = append(exposedProviderContracts, strings.Split(supportedVersions, "_")...)
		}

		l.V(1).Info("checking if contract is supported", "exposed_provider_contracts_final_list", exposedProviderContracts, "required_contract", requiredContract)
		if !slices.Contains(exposedProviderContracts, requiredContract) {
			nonSatisfying = append(nonSatisfying, "provider "+providerName+" does not support "+requiredContract)
		}
	}

	if len(missing) > 0 {
		slices.Sort(missing)
		merr = errors.Join(merr, fmt.Errorf("one or more required providers are not deployed yet: %v", missing))
	}

	if len(nonSatisfying) > 0 {
		slices.Sort(nonSatisfying)
		merr = errors.Join(merr, fmt.Errorf("one or more required provider contract versions does not satisfy deployed: %v", nonSatisfying))
	}

	if merr != nil {
		_ = r.updateStatus(ctx, template, merr.Error())
		return merr
	}

	return r.updateStatus(ctx, template, "")
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hmc.ClusterTemplate{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hmc.ServiceTemplate{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProviderTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hmc.ProviderTemplate{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&hmc.Release{},
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, o client.Object) []ctrl.Request {
				release, ok := o.(*hmc.Release)
				if !ok {
					return nil
				}

				templates := release.Templates()
				requests := make([]ctrl.Request, 0, len(templates))
				for _, template := range templates {
					requests = append(requests, ctrl.Request{
						NamespacedName: client.ObjectKey{Name: template},
					})
				}

				return requests
			}),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc:  func(event.UpdateEvent) bool { return false },
				GenericFunc: func(event.GenericEvent) bool { return false },
				DeleteFunc:  func(event.DeleteEvent) bool { return false },
			}),
		).
		Complete(r)
}
