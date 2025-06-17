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

package sveltos

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	fluxcdmeta "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sveltosv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	kcmv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/record"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
)

var (
	errEmptyConfig                  = errors.New("empty config")
	errBuildProfileFromConfigFailed = errors.New("failed to build profile from config")
	errBuildHelmChartsFailed        = errors.New("failed to build helm charts")
	errBuildKustomizationRefsFailed = errors.New("failed to build kustomization refs")
	errBuildPolicyRefsFailed        = errors.New("failed to build policy refs")
)

type ProfileConfig struct {
	// KSM specific configuration
	// Priority is the priority of the Profile.
	Priority *int32 `json:"priority,omitempty"`
	// DriftIgnore is a list of [github.com/projectsveltos/libsveltos/api/v1beta1.PatchSelector] to ignore
	// when checking for drift.
	DriftIgnore []libsveltosv1beta1.PatchSelector `json:"driftIgnore,omitempty"`

	SyncMode             string                               `json:"syncMode,omitempty"`
	TemplateResourceRefs []sveltosv1beta1.TemplateResourceRef `json:"templateResourceRefs,omitempty"`
	DriftExclusions      []sveltosv1beta1.DriftExclusion      `json:"driftExclusions,omitempty"`
	Patches              []libsveltosv1beta1.Patch            `json:"patches,omitempty"`
	ContinueOnError      bool                                 `json:"continueOnError,omitempty"`
	Reloader             bool                                 `json:"reloader,omitempty"`
	StopOnConflict       bool                                 `json:"stopOnConflict,omitempty"`
}

// ServiceSetReconciler reconciles a ServiceSet object and produces
// [github.com/projectsveltos/addon-controller/api/v1beta1.Profile] objects.
type ServiceSetReconciler struct {
	client.Client

	timeFunc func() time.Time

	// AdapterName is the name of the workload running the controller
	// effectively this name is used to identify adapter in the
	// [github.com/k0rdent/kcm/api/v1beta1.StateManagementProvider] spec.
	AdapterName      string
	AdapterNamespace string

	requeueInterval time.Duration
}

func (r *ServiceSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ServiceSet")

	serviceSet := new(kcmv1beta1.ServiceSet)
	err = r.Get(ctx, req.NamespacedName, serviceSet)
	if apierrors.IsNotFound(err) {
		l.Info("ServiceSet not found, skipping")
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	if !serviceSet.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, serviceSet)
	}

	if !controllerutil.ContainsFinalizer(serviceSet, kcmv1beta1.ServiceSetFinalizer) {
		controllerutil.AddFinalizer(serviceSet, kcmv1beta1.ServiceSetFinalizer)
		if err = r.Update(ctx, serviceSet); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: r.requeueInterval}, nil
	}

	smp := new(kcmv1beta1.StateManagementProvider)
	if err = r.Get(ctx, client.ObjectKey{Name: serviceSet.Spec.Provider.Name}, smp); err != nil {
		return ctrl.Result{}, err
	}

	continueReconciliation, err := labelsMatchSelector(serviceSet.Labels, smp.Spec.Selector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check ServiceSet labels: %w", err)
	}
	if !continueReconciliation {
		l.V(1).Info("ServiceSet labels do not match provider selector, skipping")
		return ctrl.Result{}, nil
	}

	defer func() {
		fillNotDeployedServices(serviceSet, r.timeFunc)
		serviceSet.Status.Deployed = !slices.ContainsFunc(serviceSet.Status.Services, func(s kcmv1beta1.ServiceState) bool {
			return s.State == kcmv1beta1.ServiceStateFailed ||
				s.State == kcmv1beta1.ServiceStateNotDeployed ||
				s.State == kcmv1beta1.ServiceStateProvisioning
		})
		err = errors.Join(err, r.Status().Update(ctx, serviceSet))
		l.Info("ServiceSet reconciled", "duration", time.Since(start))
	}()

	serviceSet.Status.Provider = kcmv1beta1.ProviderState{
		Ready:     smp.Status.Ready,
		Suspended: smp.Spec.Suspend,
	}
	if !smp.Status.Ready {
		record.Eventf(serviceSet, serviceSet.Generation, kcmv1beta1.StateManagementProviderNotReadyEvent,
			"StateManagementProvider %s not ready, skipping ServiceSet %s reconciliation", smp.Name, serviceSet.Name)
		l.Info("StateManagementProvider is not ready, skipping", "provider", serviceSet.Spec.Provider)
		return ctrl.Result{}, nil
	}
	if smp.Spec.Suspend {
		record.Eventf(serviceSet, serviceSet.Generation, kcmv1beta1.StateManagementProviderSuspendedEvent,
			"StateManagementProvider %s suspended, skipping ServiceSet %s reconciliation", smp.Name, serviceSet.Name)
		l.Info("StateManagementProvider is suspended, skipping", "provider", serviceSet.Spec.Provider)
		return ctrl.Result{}, nil
	}

	// first we'll ensure the profile exists and up-to-date
	if err = r.ensureProfile(ctx, serviceSet); err != nil {
		record.Warnf(serviceSet, serviceSet.Generation, kcmv1beta1.ServiceSetEnsureProfileFailedEvent,
			"Failed to ensure Profile for ServiceSet %s: %v", serviceSet.Name, err)
		return ctrl.Result{}, err
	}
	// then we'll collect the statuses of the services
	if err = r.collectServiceStatuses(ctx, serviceSet); err != nil {
		record.Warnf(serviceSet, serviceSet.Generation, kcmv1beta1.ServiceSetCollectServiceStatusesFailedEvent,
			"Failed to collect Service statuses for ServiceSet %s: %v", serviceSet.Name, err)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ServiceSetReconciler) reconcileDelete(ctx context.Context, serviceSet *kcmv1beta1.ServiceSet) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ServiceSet deletion")

	// we'll update the ServiceSet status to reflect the deletion of the services
	if slices.ContainsFunc(serviceSet.Status.Services, func(state kcmv1beta1.ServiceState) bool {
		return state.State != kcmv1beta1.ServiceStateDeleting
	}) {
		serviceStates := make([]kcmv1beta1.ServiceState, 0, len(serviceSet.Status.Services))
		for _, state := range serviceSet.Status.Services {
			if state.State == kcmv1beta1.ServiceStateDeleting {
				continue
			}
			newState := state.DeepCopy()
			newState.State = kcmv1beta1.ServiceStateDeleting
			newState.LastStateTransitionTime = ptr.To(metav1.NewTime(r.timeFunc()))
			serviceStates = append(serviceStates, *newState)
		}
		serviceSet.Status.Services = serviceStates
		return ctrl.Result{}, r.Status().Update(ctx, serviceSet)
	}

	profile := new(sveltosv1beta1.Profile)
	key := client.ObjectKeyFromObject(serviceSet)
	err := r.Get(ctx, key, profile)
	// if IgnoreNotFound returns non-nil error, it means that the error
	// occurred while sending the request to the kube-apiserver.
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Profile: %w", err)
	}
	// if error is nil, it means that the Profile was found and we need
	// to initiate the deletion of the Profile or wait for it to be deleted.
	if err == nil {
		if profile.DeletionTimestamp.IsZero() {
			l.Info("Deleting Profile", "profile", profile.Name)
			if err = r.Delete(ctx, profile); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete Profile: %w", err)
			}
			return ctrl.Result{RequeueAfter: r.requeueInterval}, nil
		}
		l.V(1).Info("Waiting for Profile to be deleted", "profile", profile.Name)
		return ctrl.Result{RequeueAfter: r.requeueInterval}, nil
	}

	// otherwise the Profile was not found, so we can remove the finalizer
	controllerutil.RemoveFinalizer(serviceSet, kcmv1beta1.ServiceSetFinalizer)
	if err = r.Update(ctx, serviceSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.timeFunc == nil {
		r.timeFunc = time.Now
	}
	r.requeueInterval = 10 * time.Second

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			MaxConcurrentReconciles: 10,
			RateLimiter:             ratelimit.DefaultFastSlow(),
		}).
		Named("ksm-sveltos-adapter").
		Watches(&kcmv1beta1.ServiceSet{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
			serviceSet, ok := o.(*kcmv1beta1.ServiceSet)
			if !ok {
				return nil
			}
			provider := new(kcmv1beta1.StateManagementProvider)
			if err := r.Get(ctx, client.ObjectKey{Name: serviceSet.Spec.Provider.Name}, provider); err != nil {
				return nil
			}
			if provider.Spec.Adapter.Name != r.AdapterName && provider.Spec.Adapter.Namespace != r.AdapterNamespace {
				return nil
			}
			return []ctrl.Request{
				{
					NamespacedName: types.NamespacedName{
						Name:      serviceSet.Name,
						Namespace: serviceSet.Namespace,
					},
				},
			}
		})).
		Watches(&kcmv1beta1.StateManagementProvider{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
			provider, ok := o.(*kcmv1beta1.StateManagementProvider)
			if !ok {
				return nil
			}
			if provider.Spec.Adapter.Name != r.AdapterName && provider.Spec.Adapter.Namespace != r.AdapterNamespace {
				return nil
			}

			selector := fields.OneTermEqualSelector(kcmv1beta1.ServiceSetProviderIndexKey, provider.Name)
			serviceSets := new(kcmv1beta1.ServiceSetList)
			if err := r.List(ctx, serviceSets, client.MatchingFieldsSelector{Selector: selector}); err != nil {
				return nil
			}
			return func() []ctrl.Request {
				requests := make([]ctrl.Request, 0, len(serviceSets.Items))
				for _, serviceSet := range serviceSets.Items {
					requests = append(requests, ctrl.Request{
						NamespacedName: types.NamespacedName{
							Name:      serviceSet.Name,
							Namespace: serviceSet.Namespace,
						},
					})
				}
				return requests
			}()
		})).
		Watches(&sveltosv1beta1.Profile{}, handler.EnqueueRequestForOwner(
			mgr.GetScheme(), mgr.GetRESTMapper(), &kcmv1beta1.ServiceSet{}, handler.OnlyControllerOwner())).
		Complete(r)
}

// ensureProfile ensures that a [github.com/projectsveltos/addon-controller/api/v1beta1.Profile]
// object exists for a given [github.com/K0rdent/kcm/api/v1beta1.ServiceSet].
func (r *ServiceSetReconciler) ensureProfile(ctx context.Context, serviceSet *kcmv1beta1.ServiceSet) error {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring ProjectSveltos Profile")
	profileCondition, _ := findCondition(serviceSet, kcmv1beta1.ServiceSetProfileCondition)

	status := metav1.ConditionFalse
	reason := kcmv1beta1.ServiceSetProfileNotReadyReason
	message := kcmv1beta1.ServiceSetProfileNotReadyMessage

	defer func() {
		if updateCondition(serviceSet, profileCondition, status, reason, message, r.timeFunc()) && status == metav1.ConditionTrue {
			l.Info("Successfully ensured ProjectSveltos Profile")
			record.Eventf(serviceSet, serviceSet.Generation, kcmv1beta1.ServiceSetEnsureProfileSuccessEvent,
				"Successfully ensured ProjectSveltos Profile for ServiceSet %s", serviceSet.Name)
		}
		l.V(1).Info("Finished ensuring ProjectSveltos Profile", "duration", time.Since(start))
	}()

	spec, err := r.profileSpec(ctx, serviceSet)
	if errors.Is(err, errBuildProfileFromConfigFailed) {
		reason = kcmv1beta1.ServiceSetProfileBuildFailedReason
		message = fmt.Sprintf("Failed to build Profile from ServiceSet %s configuration: %v", serviceSet.Name, err)
	}
	if errors.Is(err, errBuildHelmChartsFailed) {
		reason = kcmv1beta1.ServiceSetHelmChartsBuildFailedReason
		message = fmt.Sprintf("Failed to build Helm Charts from ServiceSet %s configuration: %v", serviceSet.Name, err)
	}
	if errors.Is(err, errBuildKustomizationRefsFailed) {
		reason = kcmv1beta1.ServiceSetKustomizationRefsBuildFailedReason
		message = fmt.Sprintf("Failed to build KustomizationRefs from ServiceSet %s configuration: %v", serviceSet.Name, err)
	}
	if errors.Is(err, errBuildPolicyRefsFailed) {
		reason = kcmv1beta1.ServiceSetPolicyRefsBuildFailedReason
		message = fmt.Sprintf("Failed to build PolicyRefs from ServiceSet %s configuration: %v", serviceSet.Name, err)
	}
	if err != nil {
		return fmt.Errorf("failed to build Profile: %w", err)
	}

	ownerReference := metav1.NewControllerRef(serviceSet, kcmv1beta1.GroupVersion.WithKind(kcmv1beta1.ServiceSetKind))

	profile := new(sveltosv1beta1.Profile)
	key := client.ObjectKeyFromObject(serviceSet)
	err = r.Get(ctx, key, profile)
	// if IgnoreNotFound returns non-nil error, this means that
	// an error occurred while trying to request kube-apiserver
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get Profile: %w", err)
	}

	switch {
	// we already excluded all errors except NotFound
	// hence if the error is not nil, it means that the object was not found
	case err != nil:
		profile.Name = serviceSet.Name
		profile.Namespace = serviceSet.Namespace
		profile.Labels = map[string]string{
			kcmv1beta1.KCMManagedLabelKey: kcmv1beta1.KCMManagedLabelValue,
		}
		profile.OwnerReferences = []metav1.OwnerReference{*ownerReference}
		profile.Spec = *spec
		if err = r.Create(ctx, profile); err != nil {
			return fmt.Errorf("failed to create Profile for ServiceSet %s: %w", serviceSet.Name, err)
		}
	// if profile spec is not equal to the spec we just created,
	// we need to update it
	case !equality.Semantic.DeepEqual(profile.Spec, *spec):
		profile.OwnerReferences = []metav1.OwnerReference{*ownerReference}
		profile.Spec = *spec
		if err = r.Update(ctx, profile); err != nil {
			return fmt.Errorf("failed to update Profile for ServiceSet %s: %w", serviceSet.Name, err)
		}
	}

	status = metav1.ConditionTrue
	reason = kcmv1beta1.ServiceSetProfileReadyReason
	message = kcmv1beta1.ServiceSetProfileReadyMessage
	return nil
}

func (r *ServiceSetReconciler) profileSpec(ctx context.Context, serviceSet *kcmv1beta1.ServiceSet) (*sveltosv1beta1.Spec, error) {
	cd := new(kcmv1beta1.ClusterDeployment)
	key := client.ObjectKey{
		Namespace: serviceSet.Namespace,
		Name:      serviceSet.Spec.Cluster,
	}
	if err := r.Get(ctx, key, cd); err != nil {
		return nil, fmt.Errorf("failed to get ClusterDeployment: %w", err)
	}
	cred := new(kcmv1beta1.Credential)
	key = client.ObjectKey{
		Namespace: cd.Namespace,
		Name:      cd.Spec.Credential,
	}
	if err := r.Get(ctx, key, cred); err != nil {
		return nil, fmt.Errorf("failed to get Credential: %w", err)
	}

	spec, err := buildProfileSpec(serviceSet.Spec.Provider.Config)
	if err != nil && !errors.Is(err, errEmptyConfig) {
		record.Warnf(serviceSet, serviceSet.Generation, kcmv1beta1.ServiceSetProfileBuildFailedEvent,
			"Failed to build Profile for ServiceSet %s: %v", serviceSet.Name, err)
		return nil, errors.Join(errBuildProfileFromConfigFailed, err)
	}
	spec.ClusterSelector = libsveltosv1beta1.Selector{
		LabelSelector: metav1.LabelSelector{
			MatchLabels: map[string]string{
				kcmv1beta1.FluxHelmChartNamespaceKey: cd.Namespace,
				kcmv1beta1.FluxHelmChartNameKey:      cd.Name,
			},
		},
	}
	spec.TemplateResourceRefs = append(spec.TemplateResourceRefs, projectTemplateResourceRefs(cd, cred)...)

	helmCharts, err := getHelmCharts(ctx, r.Client, serviceSet.Spec.EffectiveNamespace, serviceSet.Spec.Services)
	if err != nil {
		record.Warnf(serviceSet, serviceSet.Generation, kcmv1beta1.ServiceSetHelmChartsBuildFailedEvent,
			"Failed to get Helm charts for ServiceSet %s: %v", serviceSet.Name, err)
		return nil, errors.Join(errBuildHelmChartsFailed, err)
	}
	kustomizationRefs, err := getKustomizationRefs(ctx, r.Client, serviceSet.Spec.EffectiveNamespace, serviceSet.Spec.Services)
	if err != nil {
		record.Warnf(serviceSet, serviceSet.Generation, kcmv1beta1.ServiceSetKustomizationRefsBuildFailedEvent,
			"Failed to get KustomizationRefs for ServiceSet %s: %v", serviceSet.Name, err)
		return nil, errors.Join(errBuildKustomizationRefsFailed, err)
	}
	policyRefs, err := getPolicyRefs(ctx, r.Client, serviceSet.Spec.EffectiveNamespace, serviceSet.Spec.Services)
	if err != nil {
		record.Warnf(serviceSet, serviceSet.Generation, kcmv1beta1.ServiceSetPolicyRefsBuildFailedEvent,
			"Failed to get PolicyRefs for ServiceSet %s: %v", serviceSet.Name, err)
		return nil, errors.Join(errBuildPolicyRefsFailed, err)
	}
	policyRefs = append(policyRefs, projectPolicyRefs(cd, cred)...)
	spec.HelmCharts = helmCharts
	spec.KustomizationRefs = kustomizationRefs
	spec.PolicyRefs = policyRefs
	return spec, nil
}

func (*ServiceSetReconciler) collectServiceStatuses(ctx context.Context, _ *kcmv1beta1.ServiceSet) error {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Collecting Service statuses")

	// todo: implement collecting statuses for all services.
	//  to do that need to copy hashing logic from sveltos addon-controller

	l.Info("Collecting Service statuses completed", "duration", time.Since(start))
	return nil
}

// getHelmCharts returns slice of helm chart options to use with Sveltos.
// Namespace is the namespace of the referred templates in services slice.
func getHelmCharts(ctx context.Context, c client.Client, namespace string, services []kcmv1beta1.ServiceWithValues) ([]sveltosv1beta1.HelmChart, error) {
	helmCharts := make([]sveltosv1beta1.HelmChart, 0)
	for _, svc := range services {
		tmpl, err := serviceTemplateObjectFromService(ctx, c, svc, namespace)
		if err != nil {
			return nil, err
		}

		if tmpl.Spec.Helm == nil {
			continue
		}

		if !tmpl.Status.Valid {
			continue
		}

		var helmChart sveltosv1beta1.HelmChart
		switch {
		case tmpl.Spec.Helm.ChartRef != nil, tmpl.Spec.Helm.ChartSpec != nil:
			helmChart, err = helmChartFromSpecOrRef(ctx, c, namespace, svc, tmpl)
		case tmpl.Spec.Helm.ChartSource != nil:
			helmChart, err = helmChartFromFluxSource(ctx, svc, tmpl, namespace)
		default:
			return nil, fmt.Errorf("ServiceTemplate %s/%s has no Helm chart defined", tmpl.Namespace, tmpl.Name)
		}

		if err != nil {
			return nil, err
		}

		helmCharts = append(helmCharts, helmChart)
	}

	return helmCharts, nil
}

// helmChartFromSpecOrRef returns a HelmChart object from a ServiceTemplate.
func helmChartFromSpecOrRef(
	ctx context.Context,
	c client.Client,
	namespace string,
	svc kcmv1beta1.ServiceWithValues,
	template *kcmv1beta1.ServiceTemplate,
) (sveltosv1beta1.HelmChart, error) {
	var helmChart sveltosv1beta1.HelmChart
	if template.GetCommonStatus() == nil || template.GetCommonStatus().ChartRef == nil {
		return helmChart, fmt.Errorf("status for ServiceTemplate %s/%s has not been updated yet", template.Namespace, template.Name)
	}
	templateRef := client.ObjectKeyFromObject(template)
	chart := &sourcev1.HelmChart{}
	chartRef := client.ObjectKey{
		Namespace: template.GetCommonStatus().ChartRef.Namespace,
		Name:      template.GetCommonStatus().ChartRef.Name,
	}
	if err := c.Get(ctx, chartRef, chart); err != nil {
		return helmChart, fmt.Errorf("failed to get HelmChart %s referenced by ServiceTemplate %s: %w", chartRef.String(), templateRef.String(), err)
	}

	chartVersion := chart.Spec.Version
	if chart.Status.Artifact != nil && chart.Status.Artifact.Revision != "" {
		if _, err := semver.NewVersion(chart.Status.Artifact.Revision); err == nil {
			// Try to get the HelmChart version from status.artifact.revision
			// It contains the valid chart version for charts from a GitRepository
			chartVersion = chart.Status.Artifact.Revision
		}
	}
	_, err := semver.NewVersion(chartVersion)
	if err != nil {
		return helmChart, fmt.Errorf("failed to determine version %s of HelmChart %s referenced by ServiceTemplate %s: %w", chartVersion, chartRef.String(), templateRef.String(), err)
	}

	repoRef := client.ObjectKey{
		// Using chart's namespace because it's source
		// should be within the same namespace.
		Namespace: chart.Namespace,
		Name:      chart.Spec.SourceRef.Name,
	}

	var repoURL string
	var repoChartName string
	var registryCredentialsConfig *sveltosv1beta1.RegistryCredentialsConfig
	chartName := chart.Spec.Chart

	switch chart.Spec.SourceRef.Kind {
	case sourcev1.HelmRepositoryKind:
		repo := &sourcev1.HelmRepository{}
		if err := c.Get(ctx, repoRef, repo); err != nil {
			return helmChart, fmt.Errorf("failed to get %s: %w", repoRef.String(), err)
		}
		repoURL = repo.Spec.URL
		repoChartName = func() string {
			if repo.Spec.Type == utils.RegistryTypeOCI {
				return chartName
			}
			// Sveltos accepts ChartName in <repository>/<chart> format for non-OCI.
			// We don't have a repository name, so we can use <chart>/<chart> instead.
			// See: https://projectsveltos.github.io/sveltos/addons/helm_charts/.
			return fmt.Sprintf("%s/%s", chartName, chartName)
		}()
		if repo.Spec.Insecure || repo.Spec.SecretRef != nil {
			registryCredentialsConfig = generateRegistryCredentialsConfig(namespace, repo.Spec.Insecure, repo.Spec.SecretRef)
		}
	case sourcev1.GitRepositoryKind:
		repo := &sourcev1.GitRepository{}
		if err := c.Get(ctx, repoRef, repo); err != nil {
			return helmChart, fmt.Errorf("failed to get %s: %w", repoRef.String(), err)
		}
		repoURL = fmt.Sprintf("gitrepository://%s/%s/%s", chart.Namespace, chart.Spec.SourceRef.Name, chartName)
		// Sveltos accepts ChartName in <repository>/<chart> format for non-OCI.
		// We don't have a repository name, so we can use <chart>/<chart> instead.
		// See: https://projectsveltos.github.io/sveltos/addons/helm_charts/.
		repoChartName = chartName
		registryCredentialsConfig = generateRegistryCredentialsConfig(namespace, false, repo.Spec.SecretRef)
	default:
		return helmChart, fmt.Errorf("unsupported HelmChart source kind %s", repoRef.String())
	}

	helmChart = sveltosv1beta1.HelmChart{
		Values:        svc.Values,
		ValuesFrom:    convertValuesFrom(svc.ValuesFrom, namespace),
		RepositoryURL: repoURL,
		// We don't have repository name so chart name becomes repository name.
		RepositoryName: chartName,
		ChartName:      repoChartName,
		ChartVersion:   chartVersion,
		ReleaseName:    svc.Name,
		ReleaseNamespace: func() string {
			if svc.Namespace != "" {
				return svc.Namespace
			}
			return svc.Name
		}(),
		RegistryCredentialsConfig: registryCredentialsConfig,
	}
	return helmChart, nil
}

// generateRegistryCredentialsConfig returns a RegistryCredentialsConfig object.
func generateRegistryCredentialsConfig(namespace string, insecure bool, secretRef *fluxcdmeta.LocalObjectReference) *sveltosv1beta1.RegistryCredentialsConfig {
	c := new(sveltosv1beta1.RegistryCredentialsConfig)

	// The reason it is passed to PlainHTTP instead of InsecureSkipTLSVerify is because
	// the source.Spec.Insecure field is meant to be used for connecting to repositories
	// over plain HTTP, which is different than what InsecureSkipTLSVerify is meant for.
	// See: https://github.com/fluxcd/source-controller/pull/1288
	c.PlainHTTP = insecure
	if c.PlainHTTP {
		// InsecureSkipTLSVerify is redundant in this case.
		// At the time of implementation, Sveltos would return an error when PlainHTTP
		// and InsecureSkipTLSVerify were both set, so verify before removing.
		c.InsecureSkipTLSVerify = false
	}

	if secretRef != nil {
		c.CredentialsSecretRef = &corev1.SecretReference{
			Name:      secretRef.Name,
			Namespace: namespace,
		}
	}

	return c
}

// helmChartFromFluxSource returns a HelmChart object from a Flux source.
func helmChartFromFluxSource(
	_ context.Context,
	svc kcmv1beta1.ServiceWithValues,
	template *kcmv1beta1.ServiceTemplate,
	namespace string,
) (sveltosv1beta1.HelmChart, error) {
	var helmChart sveltosv1beta1.HelmChart
	if template.Status.SourceStatus == nil {
		return helmChart, fmt.Errorf("status for ServiceTemplate %s/%s has not been updated yet", template.Namespace, template.Name)
	}

	source := template.Spec.Helm.ChartSource
	status := template.Status.SourceStatus
	sanitizedPath := strings.TrimPrefix(strings.TrimPrefix(source.Path, "."), "/")
	url := fmt.Sprintf("%s://%s/%s/%s", status.Kind, status.Namespace, status.Name, sanitizedPath)

	helmChart = sveltosv1beta1.HelmChart{
		RepositoryURL:    url,
		ReleaseName:      svc.Name,
		ReleaseNamespace: svc.Namespace,
		Values:           svc.Values,
		ValuesFrom:       convertValuesFrom(svc.ValuesFrom, namespace),
	}

	return helmChart, nil
}

// getKustomizationRefs returns a list of KustomizationRefs for the given services.
func getKustomizationRefs(ctx context.Context, c client.Client, namespace string, services []kcmv1beta1.ServiceWithValues) ([]sveltosv1beta1.KustomizationRef, error) {
	kustomizationRefs := make([]sveltosv1beta1.KustomizationRef, 0)
	for _, svc := range services {
		tmpl, err := serviceTemplateObjectFromService(ctx, c, svc, namespace)
		if err != nil {
			return nil, err
		}

		if tmpl.Spec.Kustomize == nil {
			continue
		}

		if !tmpl.Status.Valid {
			continue
		}

		kustomization := sveltosv1beta1.KustomizationRef{
			Namespace:       tmpl.Status.SourceStatus.Namespace,
			Name:            tmpl.Status.SourceStatus.Name,
			Kind:            tmpl.Status.SourceStatus.Kind,
			Path:            tmpl.Spec.Kustomize.Path,
			TargetNamespace: svc.Namespace,
			DeploymentType:  sveltosv1beta1.DeploymentType(tmpl.Spec.Kustomize.DeploymentType),
			ValuesFrom:      convertValuesFrom(svc.ValuesFrom, namespace),
		}

		kustomizationRefs = append(kustomizationRefs, kustomization)
	}
	return kustomizationRefs, nil
}

// getPolicyRefs returns a list of PolicyRefs for the given services.
func getPolicyRefs(ctx context.Context, c client.Client, namespace string, services []kcmv1beta1.ServiceWithValues) ([]sveltosv1beta1.PolicyRef, error) {
	policyRefs := make([]sveltosv1beta1.PolicyRef, 0)
	for _, svc := range services {
		tmpl, err := serviceTemplateObjectFromService(ctx, c, svc, namespace)
		if err != nil {
			return nil, err
		}

		if tmpl.Spec.Resources == nil {
			continue
		}

		if !tmpl.Status.Valid {
			continue
		}

		policyRef := sveltosv1beta1.PolicyRef{
			Namespace:      tmpl.Status.SourceStatus.Namespace,
			Name:           tmpl.Status.SourceStatus.Name,
			Kind:           tmpl.Status.SourceStatus.Kind,
			Path:           tmpl.Spec.Resources.Path,
			DeploymentType: sveltosv1beta1.DeploymentType(tmpl.Spec.Resources.DeploymentType),
		}

		policyRefs = append(policyRefs, policyRef)
	}
	return policyRefs, nil
}

// serviceTemplateObjectFromService returns the [github.com/K0rdent/kcm/api/v1beta1.ServiceTemplate]
// object found either by direct reference or in [github.com/K0rdent/kcm/api/v1beta1.ServiceTemplateChain] by defined version.
func serviceTemplateObjectFromService(
	ctx context.Context,
	cl client.Client,
	svc kcmv1beta1.ServiceWithValues,
	namespace string,
) (*kcmv1beta1.ServiceTemplate, error) {
	template := new(kcmv1beta1.ServiceTemplate)
	key := client.ObjectKey{Name: svc.Template, Namespace: namespace}
	if err := cl.Get(ctx, key, template); err != nil {
		return nil, fmt.Errorf("failed to get ServiceTemplate %s: %w", key.String(), err)
	}
	return template, nil
}

// convertValuesFrom converts [github.com/K0rdent/kcm/api/v1beta1.ValuesFrom]
// to [github.com/projectsveltos/addon-controller/api/v1beta1.ValueFrom].
func convertValuesFrom(src []kcmv1beta1.ValuesFrom, namespace string) []sveltosv1beta1.ValueFrom {
	valueFrom := make([]sveltosv1beta1.ValueFrom, 0, len(src))
	for _, item := range src {
		valueFrom = append(valueFrom, sveltosv1beta1.ValueFrom{
			Kind:      item.Kind,
			Name:      item.Name,
			Namespace: namespace,
		})
	}
	return valueFrom
}

// buildProfileSpec converts raw JSON configuration to [github.com/projectsveltos/addon-controller/api/v1beta1.Spec].
// This conversion is done using intermediate structure [ProfileConfig] which defines additional configuration fields
// along with mirroring [github.com/projectsveltos/addon-controller/api/v1beta1.Spec] fields. This is done to,
// first, support [github.com/projectsveltos/addon-controller/api/v1beta1.Spec] fields and, second, allow additional
// configuration.
func buildProfileSpec(config *apiextensionsv1.JSON) (*sveltosv1beta1.Spec, error) {
	spec := new(sveltosv1beta1.Spec)
	if config == nil {
		return spec, errEmptyConfig
	}
	params := new(ProfileConfig)
	err := json.Unmarshal(config.Raw, params)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal raw config to profile configuration: %w", err)
	}

	tier, err := priorityToTier(ptr.Deref(params.Priority, int32(100)))
	if err != nil {
		return nil, fmt.Errorf("failed to convert priority to tier: %w", err)
	}

	spec.Tier = tier
	spec.SyncMode = sveltosv1beta1.SyncMode(params.SyncMode)
	spec.ContinueOnConflict = !params.StopOnConflict
	spec.ContinueOnError = params.ContinueOnError
	spec.Reloader = params.Reloader
	spec.TemplateResourceRefs = params.TemplateResourceRefs
	spec.Patches = params.Patches
	spec.DriftExclusions = params.DriftExclusions

	for _, target := range params.DriftIgnore {
		spec.Patches = append(spec.Patches, libsveltosv1beta1.Patch{
			Target: &target,
			Patch:  kcmv1beta1.DriftIgnorePatch,
		})
	}
	return spec, nil
}

// priorityToTier converts priority value to Sveltos tier value.
func priorityToTier(priority int32) (int32, error) {
	var mini int32 = 1
	maxi := math.MaxInt32 - mini

	// This check is needed because Sveltos asserts a min value of 1 on tier.
	if priority >= mini && priority <= maxi {
		return math.MaxInt32 - priority, nil
	}

	return 0, fmt.Errorf("invalid value %d, priority has to be between %d and %d", priority, mini, maxi)
}

// fillNotDeployedServices fills [github.com/K0rdent/kcm/api/v1beta1.ServiceSet] status for
// services that are not deployed.
func fillNotDeployedServices(serviceSet *kcmv1beta1.ServiceSet, now func() time.Time) {
	deployedServicesMap := make(map[types.NamespacedName]struct{})
	for _, service := range serviceSet.Status.Services {
		key := types.NamespacedName{
			Namespace: service.Namespace,
			Name:      service.Name,
		}
		deployedServicesMap[key] = struct{}{}
	}

	for _, service := range serviceSet.Spec.Services {
		key := types.NamespacedName{
			Namespace: service.Namespace,
			Name:      service.Name,
		}
		if _, ok := deployedServicesMap[key]; ok {
			continue
		}
		serviceSet.Status.Services = append(serviceSet.Status.Services, kcmv1beta1.ServiceState{
			Name:                    service.Name,
			Namespace:               service.Namespace,
			Template:                service.Template,
			State:                   kcmv1beta1.ServiceStateNotDeployed,
			LastStateTransitionTime: ptr.To(metav1.NewTime(now())),
		})
	}
}

func projectTemplateResourceRefs(cd *kcmv1beta1.ClusterDeployment, cred *kcmv1beta1.Credential) []sveltosv1beta1.TemplateResourceRef {
	if !cd.Spec.PropagateCredentials || cred.Spec.IdentityRef == nil {
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

func projectPolicyRefs(cd *kcmv1beta1.ClusterDeployment, cred *kcmv1beta1.Credential) []sveltosv1beta1.PolicyRef {
	if !cd.Spec.PropagateCredentials || cred.Spec.IdentityRef == nil {
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

// labelsMatchSelector returns true if the labels of the ServiceSet match the selector.
func labelsMatchSelector(serviceSetLabels map[string]string, selector *metav1.LabelSelector) (bool, error) {
	if selector == nil {
		return true, nil
	}

	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return false, fmt.Errorf("failed to convert LabelSelector to Selector: %w", err)
	}
	return labelSelector.Matches(labels.Set(serviceSetLabels)), nil
}

// findCondition finds the condition of the given type in the ServiceSet.
// If no condition is found, a new condition of given type is created.
func findCondition(serviceSet *kcmv1beta1.ServiceSet, conditionType string) (metav1.Condition, bool) {
	var created bool
	condition := apimeta.FindStatusCondition(serviceSet.Status.Conditions, conditionType)
	if condition == nil {
		condition = &metav1.Condition{Type: conditionType, ObservedGeneration: serviceSet.Generation}
		created = true
	}
	return *condition, created
}

// updateCondition updates the given condition of the ServiceSet.
func updateCondition(
	serviceSet *kcmv1beta1.ServiceSet,
	condition metav1.Condition,
	status metav1.ConditionStatus,
	reason string,
	message string,
	transitionTime time.Time,
) bool {
	if condition.Status != status || condition.Reason != reason || condition.Message != message {
		condition.LastTransitionTime = metav1.NewTime(transitionTime)
	}
	condition.ObservedGeneration = serviceSet.Generation
	condition.Status = status
	condition.Reason = reason
	condition.Message = message
	return apimeta.SetStatusCondition(&serviceSet.Status.Conditions, condition)
}
