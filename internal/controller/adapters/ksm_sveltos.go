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

package adapters

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sveltosv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
)

var errEmptyConfig = errors.New("empty config")

type ProfileConfig struct {
	// KSM specific configuration
	// Priority is the priority of the Profile.
	Priority *int32
	// DriftIgnore is a list of [github.com/projectsveltos/libsveltos/api/v1beta1.PatchSelector] to ignore
	// when checking for drift.
	DriftIgnore []libsveltosv1beta1.PatchSelector

	SyncMode             string
	LabelSelector        metav1.LabelSelector
	TemplateResourceRefs []sveltosv1beta1.TemplateResourceRef
	DriftExclusions      []sveltosv1beta1.DriftExclusion
	Patches              []libsveltosv1beta1.Patch
	StopOnConflict       bool
	Reloader             bool
	ContinueOnError      bool
}

// Reconciler reconciles a ServiceSet object and produces
// [github.com/projectsveltos/addon-controller/api/v1beta1.Profile] objects.
type Reconciler struct {
	client.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
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
		l.Info("ServiceSet is being deleted, skipping")
		return ctrl.Result{}, nil
	}

	smp := new(kcmv1beta1.StateManagementProvider)
	if err = r.Get(ctx, client.ObjectKey{Name: serviceSet.Spec.Provider}, smp); err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		err = errors.Join(err, r.Status().Update(ctx, serviceSet))
		l.Info("ServiceSet reconciled", "duration", time.Since(start))
	}()

	serviceSet.Status.Provider = kcmv1beta1.ProviderState{
		Ready:     smp.Status.Ready,
		Suspended: smp.Spec.Suspend,
	}
	if !smp.Status.Ready {
		l.Info("StateManagementProvider is not ready, skipping", "provider", serviceSet.Spec.Provider)
		return ctrl.Result{}, nil
	}
	if smp.Spec.Suspend {
		l.Info("StateManagementProvider is suspended, skipping", "provider", serviceSet.Spec.Provider)
		return ctrl.Result{}, nil
	}
	err = r.ensureProfile(ctx, serviceSet)
	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			MaxConcurrentReconciles: 10,
			RateLimiter:             ratelimit.DefaultFastSlow(),
		}).
		For(&kcmv1beta1.ServiceSet{}).
		Owns(&sveltosv1beta1.Profile{}).
		Complete(r)
}

// ensureProfile ensures that a [github.com/projectsveltos/addon-controller/api/v1beta1.Profile]
// object exists for a given [github.com/K0rdent/kcm/api/v1beta1.ServiceSet].
func (r *Reconciler) ensureProfile(ctx context.Context, serviceSet *kcmv1beta1.ServiceSet) error {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring ProjectSveltos Profile")
	defer func() {
		l.Info("Finished ensuring ProjectSveltos Profile", "duration", time.Since(start))
	}()

	profile := &sveltosv1beta1.Profile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceSet.Name,
			Namespace: serviceSet.Namespace,
			Labels: map[string]string{
				kcmv1beta1.KCMManagedLabelKey: kcmv1beta1.KCMManagedLabelValue,
			},
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, profile, func() error {
		helmCharts, err := getHelmCharts(ctx, r.Client, serviceSet.Spec.TemplatesNamespace, serviceSet.Spec.Services)
		if err != nil {
			return err
		}
		kustomizationRefs, err := getKustomizationRefs(ctx, r.Client, serviceSet.Spec.TemplatesNamespace, serviceSet.Spec.Services)
		if err != nil {
			return err
		}
		policyRefs, err := getPolicyRefs(ctx, r.Client, serviceSet.Spec.TemplatesNamespace, serviceSet.Spec.Services)
		if err != nil {
			return err
		}
		spec, err := buildProfileSpec(serviceSet.Spec.Config, policyRefs, kustomizationRefs, helmCharts)
		if err != nil && !errors.Is(err, errEmptyConfig) {
			return err
		}
		profile.Spec = *spec
		return controllerutil.SetOwnerReference(serviceSet, profile, r.Scheme())
	})
	if err != nil {
		return fmt.Errorf("failed to create/update Profile: %w", err)
	}
	l.V(1).Info("Ensured Profile", "operation", op, "profile", profile.Name)
	if op == controllerutil.OperationResultNone {
		return r.collectServiceStatuses(ctx, serviceSet, profile)
	}
	return nil
}

func (r *Reconciler) collectServiceStatuses(ctx context.Context, serviceSet *kcmv1beta1.ServiceSet, profile *sveltosv1beta1.Profile) error {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Collecting Service statuses")
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

	repo := &sourcev1.HelmRepository{}
	repoRef := client.ObjectKey{
		// Using chart's namespace because it's source
		// should be within the same namespace.
		Namespace: chart.Namespace,
		Name:      chart.Spec.SourceRef.Name,
	}
	if err := c.Get(ctx, repoRef, repo); err != nil {
		return helmChart, fmt.Errorf("failed to get HelmRepository %s: %w", repoRef.String(), err)
	}

	chartName := chart.Spec.Chart
	helmChart = sveltosv1beta1.HelmChart{
		Values:        svc.Values,
		ValuesFrom:    convertValuesFrom(svc.ValuesFrom, namespace),
		RepositoryURL: repo.Spec.URL,
		// We don't have repository name so chart name becomes repository name.
		RepositoryName: chartName,
		ChartName: func() string {
			if repo.Spec.Type == utils.RegistryTypeOCI {
				return chartName
			}
			// Sveltos accepts ChartName in <repository>/<chart> format for non-OCI.
			// We don't have a repository name, so we can use <chart>/<chart> instead.
			// See: https://projectsveltos.github.io/sveltos/addons/helm_charts/.
			return fmt.Sprintf("%s/%s", chartName, chartName)
		}(),
		ChartVersion:              chart.Spec.Version,
		ReleaseName:               svc.Name,
		ReleaseNamespace:          svc.Namespace,
		RegistryCredentialsConfig: generateRegistryCredentialsConfig(namespace, repo),
	}
	return helmChart, nil
}

func generateRegistryCredentialsConfig(namespace string, repo *sourcev1.HelmRepository) *sveltosv1beta1.RegistryCredentialsConfig {
	if repo == nil || (!repo.Spec.Insecure && repo.Spec.SecretRef == nil) {
		return nil
	}

	c := new(sveltosv1beta1.RegistryCredentialsConfig)

	// The reason it is passed to PlainHTTP instead of InsecureSkipTLSVerify is because
	// the source.Spec.Insecure field is meant to be used for connecting to repositories
	// over plain HTTP, which is different than what InsecureSkipTLSVerify is meant for.
	// See: https://github.com/fluxcd/source-controller/pull/1288
	c.PlainHTTP = repo.Spec.Insecure
	if c.PlainHTTP {
		// InsecureSkipTLSVerify is redundant in this case.
		// At the time of implementation, Sveltos would return an error when PlainHTTP
		// and InsecureSkipTLSVerify were both set, so verify before removing.
		c.InsecureSkipTLSVerify = false
	}

	if repo.Spec.SecretRef != nil {
		c.CredentialsSecretRef = &corev1.SecretReference{
			Name:      repo.Spec.SecretRef.Name,
			Namespace: namespace,
		}
	}

	return c
}

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
		},
		)
	}
	return valueFrom
}

// buildProfileSpec converts raw JSON configuration to [github.com/projectsveltos/addon-controller/api/v1beta1.Spec].
// This conversion is done using intermediate structure [ProfileConfig] which defines additional configuration fields
// along with mirroring [github.com/projectsveltos/addon-controller/api/v1beta1.Spec] fields. This is done to,
// first, support [github.com/projectsveltos/addon-controller/api/v1beta1.Spec] fields and, second, allow additional
// configuration.
func buildProfileSpec(
	config *apiextensionsv1.JSON,
	policyRefs []sveltosv1beta1.PolicyRef,
	kustomizationRefs []sveltosv1beta1.KustomizationRef,
	helmCharts []sveltosv1beta1.HelmChart,
) (*sveltosv1beta1.Spec, error) {
	if config == nil {
		return nil, errEmptyConfig
	}
	params := new(ProfileConfig)
	err := json.Unmarshal(config.Raw, params)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal raw config to profile configuration: %w", err)
	}
	tier, err := priorityToTier(*params.Priority)
	if err != nil {
		return nil, fmt.Errorf("failed to convert priority to tier: %w", err)
	}

	spec := &sveltosv1beta1.Spec{
		Tier:                 tier,
		SyncMode:             sveltosv1beta1.SyncMode(params.SyncMode),
		ContinueOnConflict:   !params.StopOnConflict,
		ContinueOnError:      params.ContinueOnError,
		Reloader:             params.Reloader,
		TemplateResourceRefs: params.TemplateResourceRefs,
		Patches:              params.Patches,
		DriftExclusions:      params.DriftExclusions,
		PolicyRefs:           policyRefs,
		KustomizationRefs:    kustomizationRefs,
		HelmCharts:           helmCharts,
	}

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
