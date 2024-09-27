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

	hcv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/storage/driver"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hmc "github.com/Mirantis/hmc/api/v1alpha1"
	"github.com/Mirantis/hmc/internal/build"
	"github.com/Mirantis/hmc/internal/helm"
	"github.com/Mirantis/hmc/internal/utils"
)

// ReleaseReconciler reconciles a Template object
type ReleaseReconciler struct {
	client.Client

	Config *rest.Config

	CreateManagement bool
	CreateRelease    bool

	HMCTemplatesChartName string
	SystemNamespace       string
}

func (r *ReleaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx).WithValues("controller", "ReleaseController")
	l.Info("Reconciling Release")
	defer l.Info("Release reconcile is finished")

	err := r.reconcileHMCTemplates(ctx, req)
	if err != nil {
		l.Error(err, "failed to reconcile HMC Templates")
		return ctrl.Result{}, err
	}

	if initialReconcile(req) {
		if err := r.ensureManagement(ctx); err != nil {
			l.Error(err, "failed to get or create Management object")
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func initialReconcile(req ctrl.Request) bool {
	return req.Name == ""
}

func (r *ReleaseReconciler) ensureManagement(ctx context.Context) error {
	l := ctrl.LoggerFrom(ctx)
	if !r.CreateManagement {
		return nil
	}
	l.Info("Ensuring Management is created")
	mgmtObj := &hmc.Management{
		ObjectMeta: metav1.ObjectMeta{
			Name:       hmc.ManagementName,
			Finalizers: []string{hmc.ManagementFinalizer},
		},
	}
	err := r.Get(ctx, client.ObjectKey{
		Name: hmc.ManagementName,
	}, mgmtObj)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get %s Management object: %w", hmc.TemplateManagementName, err)
	}
	mgmtObj.Spec.Release, err = r.getCurrentReleaseName(ctx)
	if err != nil {
		return err
	}

	mgmtObj.Spec.Providers = hmc.GetDefaultProviders()

	getter := helm.NewMemoryRESTClientGetter(r.Config, r.RESTMapper())
	actionConfig := new(action.Configuration)
	err = actionConfig.Init(getter, r.SystemNamespace, "secret", l.Info)
	if err != nil {
		return err
	}

	hmcConfig := make(chartutil.Values)
	release, err := actionConfig.Releases.Last("hmc")
	if err != nil {
		if !errors.Is(err, driver.ErrReleaseNotFound) {
			return err
		}
	} else {
		if len(release.Config) > 0 {
			chartutil.CoalesceTables(hmcConfig, release.Config)
		}
	}
	rawConfig, err := json.Marshal(hmcConfig)
	if err != nil {
		return err
	}
	mgmtObj.Spec.Core = &hmc.Core{
		HMC: hmc.Component{
			Config: &apiextensionsv1.JSON{
				Raw: rawConfig,
			},
		},
	}
	err = r.Create(ctx, mgmtObj)
	if err != nil {
		return fmt.Errorf("failed to create %s Management object: %w", hmc.TemplateManagementName, err)
	}

	l.Info("Successfully created Management object with default configuration")
	return nil
}

func (r *ReleaseReconciler) reconcileHMCTemplates(ctx context.Context, req ctrl.Request) error {
	l := ctrl.LoggerFrom(ctx)
	if !r.CreateRelease {
		l.Info("Reconciling HMC Templates is skipped")
		return nil
	}
	name := utils.ReleaseNameFromVersion(build.Version)
	version := build.Version
	var ownerRefs []metav1.OwnerReference
	release := &hmc.Release{}
	if !initialReconcile(req) {
		if err := r.Client.Get(ctx, req.NamespacedName, release); err != nil {
			l.Error(err, "failed to get Release")
			return err
		}
		name = req.Name
		version = release.Spec.Version
		ownerRefs = []metav1.OwnerReference{
			{
				APIVersion: hmc.GroupVersion.String(),
				Kind:       release.Kind,
				Name:       release.Name,
				UID:        release.UID,
			},
		}
	}

	helmChart := &sourcev1.HelmChart{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: r.SystemNamespace,
		},
	}

	operation, err := ctrl.CreateOrUpdate(ctx, r.Client, helmChart, func() error {
		helmChart.OwnerReferences = ownerRefs
		if helmChart.Labels == nil {
			helmChart.Labels = make(map[string]string)
		}
		helmChart.Labels[hmc.HMCManagedLabelKey] = hmc.HMCManagedLabelValue
		helmChart.Spec = sourcev1.HelmChartSpec{
			Chart:   r.HMCTemplatesChartName,
			Version: version,
			SourceRef: sourcev1.LocalHelmChartSourceReference{
				Kind: sourcev1.HelmRepositoryKind,
				Name: defaultRepoName,
			},
			Interval: metav1.Duration{Duration: helm.DefaultReconcileInterval},
		}
		return nil
	})
	if err != nil {
		return err
	}
	if operation == controllerutil.OperationResultCreated || operation == controllerutil.OperationResultUpdated {
		l.Info(fmt.Sprintf("Successfully %s %s/%s HelmChart", operation, r.SystemNamespace, name))
	}

	if _, err := helm.ArtifactReady(helmChart); err != nil {
		return fmt.Errorf("HelmChart %s/%s Artifact is not ready: %w", r.SystemNamespace, name, err)
	}

	opts := helm.ReconcileHelmReleaseOpts{
		ChartRef: &hcv2.CrossNamespaceSourceReference{
			Kind:      helmChart.Kind,
			Name:      helmChart.Name,
			Namespace: helmChart.Namespace,
		},
	}

	if initialReconcile(req) {
		createReleaseValues := map[string]any{
			"createRelease": true,
		}
		raw, err := json.Marshal(createReleaseValues)
		if err != nil {
			return err
		}
		opts.Values = &apiextensionsv1.JSON{Raw: raw}
	}

	_, operation, err = helm.ReconcileHelmRelease(ctx, r.Client, name, r.SystemNamespace, opts)
	if err != nil {
		return err
	}
	if operation == controllerutil.OperationResultCreated || operation == controllerutil.OperationResultUpdated {
		l.Info(fmt.Sprintf("Successfully %s %s/%s HelmRelease", operation, r.SystemNamespace, name))
	}
	return nil
}

func (r *ReleaseReconciler) getCurrentReleaseName(ctx context.Context) (string, error) {
	releases := &hmc.ReleaseList{}
	listOptions := client.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{hmc.VersionKey: build.Version}),
	}
	if err := r.Client.List(ctx, releases, &listOptions); err != nil {
		return "", err
	}
	if len(releases.Items) != 1 {
		return "", fmt.Errorf("expected 1 Release with version %s, found %d", build.Version, len(releases.Items))
	}
	return releases.Items[0].Name, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ReleaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&hmc.Release{}, builder.WithPredicates(predicate.Funcs{
			DeleteFunc:  func(event.DeleteEvent) bool { return false },
			GenericFunc: func(event.GenericEvent) bool { return false },
		})).
		Build(r)
	if err != nil {
		return err
	}
	//
	if !r.CreateManagement {
		return nil
	}
	// There's no Release objects created yet and we need to trigger reconcile
	initChannel := make(chan event.GenericEvent, 1)
	initChannel <- event.GenericEvent{Object: &hmc.Release{}}
	return c.Watch(source.Channel(initChannel, &handler.EnqueueRequestForObject{}))
}
