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
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"helm.sh/helm/v3/pkg/chart"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/helm"
	"github.com/K0rdent/kcm/internal/metrics"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	labelsutil "github.com/K0rdent/kcm/internal/util/labels"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
)

const (
	schemaConfigMapKey = "schema"
)

// TemplateReconciler reconciles a *Template object
type TemplateReconciler struct {
	client.Client

	downloadHelmChartFunc func(context.Context, *sourcev1.Artifact) (*chart.Chart, error)

	SystemNamespace       string
	DefaultRegistryConfig helm.DefaultRegistryConfig
	CreateManagement      bool

	defaultRequeueTime time.Duration
}

type ClusterTemplateReconciler struct {
	TemplateReconciler
}

type ProviderTemplateReconciler struct {
	TemplateReconciler
}

func (r *ClusterTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ClusterTemplate")

	clusterTemplate := new(kcmv1.ClusterTemplate)
	if err := r.Get(ctx, req.NamespacedName, clusterTemplate); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ClusterTemplate not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		l.Error(err, "Failed to get ClusterTemplate")
		return ctrl.Result{}, err
	}

	management, err := r.getManagement(ctx, clusterTemplate)
	if err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("Management is not created yet, retrying")
			return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
		}
		return ctrl.Result{}, err
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping ClusterTemplate reconciliation")
		return ctrl.Result{}, nil
	}

	if updated, err := labelsutil.AddKCMComponentLabel(ctx, r.Client, clusterTemplate); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{Requeue: true}, err // generation has not changed, need explicit requeue
	}

	return r.ReconcileTemplate(ctx, clusterTemplate)
}

func (r *ProviderTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ProviderTemplate")

	providerTemplate := new(kcmv1.ProviderTemplate)
	if err := r.Get(ctx, req.NamespacedName, providerTemplate); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ProviderTemplate not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		l.Error(err, "Failed to get ProviderTemplate")
		return ctrl.Result{}, err
	}

	management, err := r.getManagement(ctx, providerTemplate)
	if r.CreateManagement && err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("Management is not created yet, retrying")
			return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
		}
	}
	if management != nil && !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping ProviderTemplate reconciliation")
		return ctrl.Result{}, nil
	}

	if updated, err := labelsutil.AddKCMComponentLabel(ctx, r.Client, providerTemplate); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{Requeue: true}, err // generation has not changed, need explicit requeue
	}

	changed, err := r.setReleaseOwnership(ctx, providerTemplate)
	if err != nil {
		l.Error(err, "Failed to set OwnerReferences")
		return ctrl.Result{}, err
	}
	if changed {
		l.Info("Updating OwnerReferences with associated Releases")
		return ctrl.Result{Requeue: true}, r.Update(ctx, providerTemplate) // generation will NOT change, need explicit requeue
	}

	return r.ReconcileTemplate(ctx, providerTemplate)
}

func (r *ProviderTemplateReconciler) setReleaseOwnership(ctx context.Context, providerTemplate *kcmv1.ProviderTemplate) (changed bool, err error) {
	releases := &kcmv1.ReleaseList{}
	err = r.List(ctx, releases,
		client.MatchingFields{kcmv1.ReleaseTemplatesIndexKey: providerTemplate.Name},
	)
	if err != nil {
		return changed, fmt.Errorf("failed to get associated releases: %w", err)
	}
	for _, release := range releases.Items {
		if kubeutil.AddOwnerReference(providerTemplate, &release) {
			changed = true
		}
	}
	return changed, nil
}

type templateCommon interface {
	client.Object
	GetHelmSpec() *kcmv1.HelmSpec
	GetCommonStatus() *kcmv1.TemplateStatusCommon
	FillStatusWithProviders(map[string]string) error
}

func (r *TemplateReconciler) ReconcileTemplate(ctx context.Context, template templateCommon) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	helmRepositorySecrets := []string{r.DefaultRegistryConfig.CertSecretName, r.DefaultRegistryConfig.CredentialsSecretName}
	{
		exists, missingSecrets, err := kubeutil.CheckAllSecretsExistInNamespace(ctx, r.Client, r.SystemNamespace, helmRepositorySecrets...)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to check if Secrets %v exists: %w", helmRepositorySecrets, err)
		}
		if !exists {
			return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, r.updateStatus(ctx, template, fmt.Sprintf("Some of the predeclared Secrets (%v) are missing (%v) in the %s namespace", helmRepositorySecrets, missingSecrets, r.SystemNamespace))
		}
	}

	helmSpec := template.GetHelmSpec()
	status := template.GetCommonStatus()
	var (
		err     error
		hcChart *sourcev1.HelmChart
	)
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

		if template.GetNamespace() == r.SystemNamespace || !templateManagedByKCM(template) {
			namespace := template.GetNamespace()
			if namespace == "" {
				namespace = r.SystemNamespace
			}

			if namespace != r.SystemNamespace {
				for _, secretName := range helmRepositorySecrets {
					if err := kubeutil.CopySecret(ctx, r.Client, r.Client, client.ObjectKey{Namespace: r.SystemNamespace, Name: secretName}, namespace, nil, nil); err != nil {
						l.Error(err, "failed to copy Secret for the HelmRepository")
						return ctrl.Result{}, err
					}
				}
			}

			if err := helm.ReconcileHelmRepository(ctx, r.Client, kcmv1.DefaultRepoName, namespace, r.DefaultRegistryConfig.HelmRepositorySpec()); err != nil {
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

	if err := fillStatusFromChart(ctx, template, helmChart); err != nil {
		_ = r.updateStatus(ctx, template, err.Error())
		return ctrl.Result{}, err
	}

	if err := r.ensureSchemaConfigmap(ctx, template, helmChart); err != nil {
		_ = r.updateStatus(ctx, template, err.Error())
		return ctrl.Result{}, err
	}

	l.Info("Chart validation completed successfully")

	return ctrl.Result{}, r.updateStatus(ctx, template, "")
}

func generateSchemaConfigMapName(template templateCommon) string {
	var templateTypePrefix string
	switch template.GetObjectKind().GroupVersionKind().Kind {
	case kcmv1.ClusterTemplateKind:
		templateTypePrefix = "ct"
	case kcmv1.ProviderTemplateKind:
		templateTypePrefix = "pt"
	case kcmv1.ServiceTemplateKind:
		templateTypePrefix = "st"
	}
	return fmt.Sprintf("schema-%s-%s", templateTypePrefix, template.GetName())
}

func (r *TemplateReconciler) ensureSchemaConfigmap(ctx context.Context, template templateCommon, helmChart *chart.Chart) error {
	if len(helmChart.Schema) == 0 {
		return nil
	}

	ownerRef := metav1.NewControllerRef(template, template.GetObjectKind().GroupVersionKind())
	ns := template.GetNamespace()
	if ns == "" {
		ns = r.SystemNamespace
	}
	schemaConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            generateSchemaConfigMapName(template),
			Namespace:       ns,
			OwnerReferences: []metav1.OwnerReference{*ownerRef},
		},
	}

	_, err := ctrl.CreateOrUpdate(ctx, r.Client, schemaConfigMap, func() error {
		if schemaConfigMap.Data == nil {
			schemaConfigMap.Data = make(map[string]string)
		}
		schemaConfigMap.Data[schemaConfigMapKey] = string(helmChart.Schema)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update schema ConfigMap: %w", err)
	}

	status := template.GetCommonStatus()
	status.SchemaConfigMapName = schemaConfigMap.Name

	return nil
}

// fillStatusFromChart fills the template's status with relevant information from provided helm chart.
func fillStatusFromChart(ctx context.Context, template templateCommon, hc *chart.Chart) error {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Parsing Helm chart metadata")

	if err := fillStatusWithProviders(template, hc); err != nil {
		l.Error(err, "Failed to fill status with providers")
		return fmt.Errorf("failed to fill status with providers: %w", err)
	}

	desc := hc.Metadata.Description
	values := hc.Values

	for _, d := range hc.Dependencies() {
		// The services in k0rdent/catalog are actually wrappers around the actual helm chart of the app.
		// So the Chart.yaml for the service has the actual helm chart of the app as a dependency with
		// the same name and version. Therefore, if there is a dependency with the same name and version,
		// 	we want to get information from that dependency rather than the parent (wrapper) chart because
		// the parent chart won't have all of the information, like the default helm values for the app.
		if hc.Name() == d.Name() && hc.Metadata.Version == d.Metadata.Version {
			desc = d.Metadata.Description
			values = d.Values
		}
	}

	rawValues, err := json.Marshal(values)
	if err != nil {
		l.Error(err, "Failed to parse Helm chart values")
		return fmt.Errorf("failed to parse Helm chart values: %w", err)
	}

	status := template.GetCommonStatus()
	status.Description = desc
	status.Config = &apiextv1.JSON{Raw: rawValues}

	return nil
}

func templateManagedByKCM(template templateCommon) bool {
	return template.GetLabels()[kcmv1.KCMManagedLabelKey] == kcmv1.KCMManagedLabelValue
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

	if err := r.Status().Update(ctx, template); err != nil {
		return fmt.Errorf("failed to update status for template %s/%s: %w", template.GetNamespace(), template.GetName(), err)
	}

	metrics.TrackMetricTemplateInvalidity(ctx, template.GetObjectKind().GroupVersionKind().Kind, template.GetNamespace(), template.GetName(), status.Valid)

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

		helmChart.Labels[kcmv1.KCMManagedLabelKey] = kcmv1.KCMManagedLabelValue
		kubeutil.AddOwnerReference(helmChart, template)

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

func (r *TemplateReconciler) getManagement(ctx context.Context, template templateCommon) (*kcmv1.Management, error) {
	management := &kcmv1.Management{}
	if err := r.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, management); err != nil {
		if apierrors.IsNotFound(err) {
			_ = r.updateStatus(ctx, template, "Waiting for Management creation to complete validation")
			return nil, err
		}
		err = fmt.Errorf("failed to get Management: %w", err)
		_ = r.updateStatus(ctx, template, err.Error())
		return nil, err
	}

	return management, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.defaultRequeueTime = 1 * time.Minute

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.ClusterTemplate{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&kcmv1.Management{}, handler.Funcs{ // address https://github.com/k0rdent/kcm/issues/954
			UpdateFunc: func(ctx context.Context, tue event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[ctrl.Request]) {
				newO, ok := tue.ObjectNew.(*kcmv1.Management)
				if !ok {
					return
				}

				oldO, ok := tue.ObjectOld.(*kcmv1.Management)
				if !ok {
					return
				}

				if slices.Equal(oldO.Status.AvailableProviders, newO.Status.AvailableProviders) {
					return
				}

				providerNames := []string{}
				toLoop, toSearch := oldO.Status.AvailableProviders, newO.Status.AvailableProviders
				if len(newO.Status.AvailableProviders) > len(oldO.Status.AvailableProviders) {
					toLoop, toSearch = newO.Status.AvailableProviders, slices.Clip(oldO.Status.AvailableProviders)
				}
				for _, providerName := range toLoop {
					if !slices.Contains(toSearch, providerName) {
						providerNames = append(providerNames, providerName)
					}
				}

				if len(providerNames) == 0 {
					return
				}

				l := ctrl.LoggerFrom(ctx).WithName("cluster-templates.mgmt-watcher").WithValues("event", "update")
				cnt := 0
				for _, providerName := range providerNames {
					if providerName == "" {
						continue
					}

					clusterTemplates := new(kcmv1.ClusterTemplateList)
					if err := r.Client.List(ctx, clusterTemplates, client.MatchingFields{kcmv1.ClusterTemplateProvidersIndexKey: providerName}); err != nil {
						l.Error(err, "failed to list ClusterTemplates to put in the queue")
						continue
					}

					for _, clusterTemplate := range clusterTemplates.Items {
						l.V(1).Info("Queuing ClusterTemplate used by a provider", "provider", providerName, "clustertemplate", client.ObjectKeyFromObject(&clusterTemplate))
						q.Add(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&clusterTemplate)})
						cnt++
					}
				}

				l.V(1).Info("Successfully proceed the update event", "num_templates_queued", cnt)
			},
		}).
		Complete(r)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProviderTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.defaultRequeueTime = 1 * time.Minute

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.ProviderTemplate{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&kcmv1.Release{},
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, o client.Object) []ctrl.Request {
				release, ok := o.(*kcmv1.Release)
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
