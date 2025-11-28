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

package components

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	fluxconditions "github.com/fluxcd/pkg/runtime/conditions"
	helmreleasepkg "helm.sh/helm/v3/pkg/release"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	capioperatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha2"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/helm"
	"github.com/K0rdent/kcm/internal/record"
	pointerutil "github.com/K0rdent/kcm/internal/util/pointer"
)

type ReconcileComponentsOpts struct {
	// KubeConfigRef is a reference to the Secret containing the kubeconfig
	// of the target cluster where components will be installed. If unset,
	// the component will be installed on the current cluster.
	KubeConfigRef *fluxmeta.SecretKeyReference
	// Labels defines additional labels to apply to the created HelmReleases.
	Labels map[string]string

	// Namespace is the namespace to install components in.
	Namespace string
	// GlobalRegistry is the URL of the global registry to be passed to components values.
	GlobalRegistry string
	// RegistryCertSecretName is the name of the secret with CA certificate of the global registry
	// to be passed to components values.
	RegistryCertSecretName string
	// RegistryCredentialsSecretName is the name of the secret with registry credentials (username/password)
	// to be used for pulling images from authenticated registries.
	RegistryCredentialsSecretName string

	// CreateNamespace tells the Helm install action to create the namespace if it does not exist yet.
	CreateNamespace bool
	// CertManagerInstalled indicates whether cert-manager is installed in the cluster.
	CertManagerInstalled bool
	// DefaultHelmTimeout is the timeout duration for Helm install or upgrade operations.
	DefaultHelmTimeout time.Duration
}

type clusterInterface interface {
	client.Object

	Components() kcmv1.ComponentsCommonSpec
	GetComponentsStatus() *kcmv1.ComponentsCommonStatus
	KCMTemplate(*kcmv1.Release) string
	KCMHelmChartName() string
	HelmReleaseName(string) string
}

type component struct {
	kcmv1.Component

	name            string
	helmReleaseName string
	targetNamespace string
	installSettings *helmcontrollerv2.Install
	// helm release dependencies
	dependsOn      []fluxmeta.NamespacedObjectReference
	isCAPIProvider bool
}

type StatusAccumulator struct {
	Components             map[string]kcmv1.ComponentStatus
	CompatibilityContracts map[string]kcmv1.CompatibilityContracts
	Providers              kcmv1.Providers
}

func Reconcile(
	ctx context.Context,
	mgmtClient client.Client,
	rgnlClient client.Client,
	cluster clusterInterface,
	restConfig *rest.Config,
	release *kcmv1.Release,
	opts ReconcileComponentsOpts,
) (bool, error) {
	l := ctrl.LoggerFrom(ctx)

	var (
		errs error

		statusAccumulator = &StatusAccumulator{
			Providers:              kcmv1.Providers{"infrastructure-internal"},
			Components:             make(map[string]kcmv1.ComponentStatus),
			CompatibilityContracts: make(map[string]kcmv1.CompatibilityContracts),
		}
		requeue bool
	)

	opts.CertManagerInstalled = certManagerInstalled(ctx, restConfig, opts.Namespace) == nil

	components, err := getWrappedComponents(ctx, cluster, release, opts)
	if err != nil {
		l.Error(err, "failed to wrap KCM components")
		return requeue, err
	}

	for _, component := range components {
		l.V(1).Info("reconciling components", "component", component)
		var notReadyDeps []string
		for _, dep := range component.dependsOn {
			if !statusAccumulator.Components[dep.Name].Success {
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
		if err := mgmtClient.Get(ctx, client.ObjectKey{Name: component.Template}, template); err != nil {
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

		var dependsOn []helmcontrollerv2.DependencyReference
		for _, comp := range component.dependsOn {
			dependsOn = append(dependsOn, helmcontrollerv2.DependencyReference{
				Namespace: comp.Namespace,
				Name:      cluster.HelmReleaseName(comp.Name),
			})
		}
		hrReconcileOpts := helm.ReconcileHelmReleaseOpts{
			ReleaseName:     component.name,
			Values:          component.Config,
			ChartRef:        template.Status.ChartRef,
			DependsOn:       dependsOn,
			TargetNamespace: component.targetNamespace,
			Install:         component.installSettings,
			Timeout:         opts.DefaultHelmTimeout,
		}

		if opts.CreateNamespace {
			hrReconcileOpts.Install.CreateNamespace = true
		}
		if opts.KubeConfigRef != nil {
			hrReconcileOpts.KubeConfigRef = opts.KubeConfigRef
		}
		if len(opts.Labels) > 0 {
			hrReconcileOpts.Labels = opts.Labels
		}

		if template.Spec.Helm.ChartSpec != nil {
			hrReconcileOpts.ReconcileInterval = &template.Spec.Helm.ChartSpec.Interval.Duration
		}

		_, operation, err := helm.ReconcileHelmRelease(ctx, mgmtClient, component.helmReleaseName, opts.Namespace, hrReconcileOpts)
		if err != nil {
			errMsg := fmt.Sprintf("Failed to reconcile HelmRelease %s/%s: %v", opts.Namespace, component.helmReleaseName, err)
			updateComponentsStatus(statusAccumulator, component, template, errMsg)
			errs = errors.Join(errs, errors.New(errMsg))
			continue
		}
		if operation == controllerutil.OperationResultCreated {
			record.Eventf(cluster, cluster.GetGeneration(), "HelmReleaseCreated", "Successfully created %s/%s HelmRelease", opts.Namespace, component.helmReleaseName)
		}
		if operation == controllerutil.OperationResultUpdated {
			record.Eventf(cluster, cluster.GetGeneration(), "HelmReleaseUpdated", "Successfully updated %s/%s HelmRelease", opts.Namespace, component.helmReleaseName)
		}

		if err := checkProviderStatus(ctx, mgmtClient, rgnlClient, component, opts.Namespace); err != nil {
			l.Info("Provider is not yet ready", "template", component.Template, "err", err)
			requeue = true
			updateComponentsStatus(statusAccumulator, component, template, err.Error())
			continue
		}

		updateComponentsStatus(statusAccumulator, component, template, "")
	}

	componentsStatus := cluster.GetComponentsStatus()
	componentsStatus.AvailableProviders = statusAccumulator.Providers
	componentsStatus.CAPIContracts = statusAccumulator.CompatibilityContracts
	componentsStatus.Components = statusAccumulator.Components

	return requeue, errs
}

func getWrappedComponents(ctx context.Context, cluster clusterInterface, release *kcmv1.Release, opts ReconcileComponentsOpts) ([]component, error) {
	components := make([]component, 0, len(cluster.Components().Providers)+2)

	kcmComponent := kcmv1.Component{}
	capiComponent := kcmv1.Component{}
	if cluster.Components().Core != nil {
		kcmComponent = cluster.Components().Core.KCM
		capiComponent = cluster.Components().Core.CAPI
	}

	remediationSettings := &helmcontrollerv2.InstallRemediation{
		Retries:              3,
		RemediateLastFailure: pointerutil.To(true),
	}

	kcmComp := component{
		Component:       kcmComponent,
		targetNamespace: opts.Namespace,
		installSettings: &helmcontrollerv2.Install{
			Remediation: remediationSettings,
		},
		name:            cluster.KCMHelmChartName(),
		helmReleaseName: cluster.HelmReleaseName(cluster.KCMHelmChartName()),
	}
	if kcmComp.Template == "" {
		kcmComp.Template = cluster.KCMTemplate(release)
	}

	kcmConfig, err := getComponentValues(ctx, cluster.KCMHelmChartName(), kcmComp.Config, opts)
	if err != nil {
		return nil, err
	}
	kcmComp.Config = kcmConfig
	components = append(components, kcmComp)

	capiComp := component{
		Component: capiComponent,
		installSettings: &helmcontrollerv2.Install{
			Remediation: remediationSettings,
		},
		name:            kcmv1.CoreCAPIName,
		helmReleaseName: cluster.HelmReleaseName(kcmv1.CoreCAPIName),
		dependsOn:       []fluxmeta.NamespacedObjectReference{{Name: cluster.KCMHelmChartName()}},
		isCAPIProvider:  true,
	}
	if capiComp.Template == "" {
		capiComp.Template = release.Spec.CAPI.Template
	}

	capiConfig, err := getComponentValues(ctx, kcmv1.CoreCAPIName, capiComp.Config, opts)
	if err != nil {
		return nil, err
	}
	capiComp.Config = capiConfig

	components = append(components, capiComp)

	const sveltosTargetNamespace = "projectsveltos"

	for _, p := range cluster.Components().Providers {
		c := component{
			Component:       p.Component,
			name:            p.Name,
			helmReleaseName: cluster.HelmReleaseName(p.Name),
			installSettings: &helmcontrollerv2.Install{
				Remediation: remediationSettings,
			},
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
				Remediation:     remediationSettings,
			}
		}

		config, err := getComponentValues(ctx, p.Name, c.Config, opts)
		if err != nil {
			return nil, err
		}
		c.Config = config

		components = append(components, c)
	}

	return components, nil
}

func updateComponentsStatus(
	stAcc *StatusAccumulator,
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
			stAcc.Providers = append(stAcc.Providers, template.Status.Providers...)
			slices.Sort(stAcc.Providers)
			stAcc.Providers = slices.Compact(stAcc.Providers)
			for _, v := range template.Status.Providers {
				stAcc.CompatibilityContracts[v] = template.Status.CAPIContracts
			}
		}
	}
	stAcc.Components[comp.name] = componentStatus
}

// checkProviderStatus checks the status of a provider associated with a given
// ProviderTemplate name. Since there's no way to determine resource Kind from
// the given template iterate over all possible provider types.
func checkProviderStatus(ctx context.Context, mgmtClient, rgnlClient client.Client, component component, systemNamespace string) error {
	helmReleaseName := component.helmReleaseName
	hr := &helmcontrollerv2.HelmRelease{}
	if err := mgmtClient.Get(ctx, client.ObjectKey{Namespace: systemNamespace, Name: helmReleaseName}, hr); err != nil {
		return fmt.Errorf("failed to check provider status: %w", err)
	}

	hrReadyCondition := fluxconditions.Get(hr, fluxmeta.ReadyCondition)
	if hrReadyCondition == nil || hrReadyCondition.ObservedGeneration != hr.Generation {
		return fmt.Errorf("HelmRelease %s/%s Ready condition is not updated yet", systemNamespace, helmReleaseName)
	}
	if hr.Status.ObservedGeneration != hr.Generation {
		return fmt.Errorf("HelmRelease %s/%s has not observed new values yet", systemNamespace, helmReleaseName)
	}
	if !fluxconditions.IsReady(hr) {
		return fmt.Errorf("HelmRelease %s/%s is not yet ready: %s", systemNamespace, helmReleaseName, hrReadyCondition.Message)
	}

	// mostly for sanity check
	latestSnapshot := hr.Status.History.Latest()
	if latestSnapshot == nil {
		return fmt.Errorf("HelmRelease %s/%s has empty deployment history in the status", systemNamespace, helmReleaseName)
	}
	if latestSnapshot.Status != helmreleasepkg.StatusDeployed.String() {
		return fmt.Errorf("HelmRelease %s/%s is not yet deployed, actual status is %s", systemNamespace, helmReleaseName, latestSnapshot.Status)
	}
	if latestSnapshot.ConfigDigest != hr.Status.LastAttemptedConfigDigest {
		return fmt.Errorf("HelmRelease %s/%s is not yet reconciled the latest values", systemNamespace, helmReleaseName)
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
		if err := rgnlClient.List(ctx, gpl, client.MatchingLabels{kcmv1.FluxHelmChartNameKey: hr.Status.History.Latest().Name}); meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
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
	return slices.ContainsFunc(gp.GetStatus().Conditions, func(c metav1.Condition) bool {
		return c.Type == clusterapiv1.ReadyCondition && c.Status == metav1.ConditionTrue
	})
}

func getFalseConditions(gp capioperatorv1.GenericProvider) []string {
	conditions := gp.GetStatus().Conditions
	messages := make([]string, 0, len(conditions))
	for _, cond := range conditions {
		if cond.Status == metav1.ConditionTrue {
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
