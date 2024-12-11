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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hmc "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/utils"
)

// TemplateChainReconciler reconciles a TemplateChain object
type TemplateChainReconciler struct {
	client.Client
	SystemNamespace string

	templateKind string
}

type ClusterTemplateChainReconciler struct {
	TemplateChainReconciler
}

type ServiceTemplateChainReconciler struct {
	TemplateChainReconciler
}

// templateChain is the interface defining a list of methods to interact with *templatechains
type templateChain interface {
	client.Object
	GetSpec() *hmc.TemplateChainSpec
}

func (r *ClusterTemplateChainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ClusterTemplateChain")

	clusterTemplateChain := &hmc.ClusterTemplateChain{}
	err := r.Get(ctx, req.NamespacedName, clusterTemplateChain)
	if err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ClusterTemplateChain not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		l.Error(err, "Failed to get ClusterTemplateChain")
		return ctrl.Result{}, err
	}

	return r.ReconcileTemplateChain(ctx, clusterTemplateChain)
}

func (r *ServiceTemplateChainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ServiceTemplateChain")

	serviceTemplateChain := &hmc.ServiceTemplateChain{}
	err := r.Get(ctx, req.NamespacedName, serviceTemplateChain)
	if err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("ServiceTemplateChain not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		l.Error(err, "Failed to get ServiceTemplateChain")
		return ctrl.Result{}, err
	}

	return r.ReconcileTemplateChain(ctx, serviceTemplateChain)
}

func (r *TemplateChainReconciler) ReconcileTemplateChain(ctx context.Context, templateChain templateChain) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)

	if err := utils.AddHMCComponentLabel(ctx, r.Client, templateChain); err != nil {
		l.Error(err, "adding component label")
		return ctrl.Result{}, err
	}

	if templateChain.GetNamespace() == r.SystemNamespace {
		return ctrl.Result{}, nil
	}

	systemTemplates, err := r.getTemplates(ctx, &client.ListOptions{Namespace: r.SystemNamespace})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get system templates: %w", err)
	}

	var errs error
	for _, supportedTemplate := range templateChain.GetSpec().SupportedTemplates {
		meta := metav1.ObjectMeta{
			Name:      supportedTemplate.Name,
			Namespace: templateChain.GetNamespace(),
			Labels: map[string]string{
				hmc.HMCManagedLabelKey: hmc.HMCManagedLabelValue,
			},
		}

		source, found := systemTemplates[supportedTemplate.Name]
		if !found {
			errs = errors.Join(errs, fmt.Errorf("source %s %s/%s is not found", r.templateKind, r.SystemNamespace, supportedTemplate.Name))
			continue
		}
		if source.GetCommonStatus().ChartRef == nil {
			errs = errors.Join(errs, fmt.Errorf("source %s %s/%s does not have chart reference yet", r.templateKind, r.SystemNamespace, supportedTemplate.Name))
			continue
		}

		var target client.Object
		switch r.templateKind {
		case hmc.ClusterTemplateKind:
			clusterTemplate, ok := source.(*hmc.ClusterTemplate)
			if !ok {
				return ctrl.Result{}, fmt.Errorf("type assertion failed: expected ClusterTemplate but got %T", source)
			}
			spec := clusterTemplate.Spec
			spec.Helm = hmc.HelmSpec{ChartRef: clusterTemplate.Status.ChartRef}
			target = &hmc.ClusterTemplate{ObjectMeta: meta, Spec: spec}
		case hmc.ServiceTemplateKind:
			serviceTemplate, ok := source.(*hmc.ServiceTemplate)
			if !ok {
				return ctrl.Result{}, fmt.Errorf("type assertion failed: expected ServiceTemplate but got %T", source)
			}
			spec := serviceTemplate.Spec
			spec.Helm = hmc.HelmSpec{ChartRef: serviceTemplate.Status.ChartRef}
			target = &hmc.ServiceTemplate{ObjectMeta: meta, Spec: spec}
		default:
			return ctrl.Result{}, fmt.Errorf("invalid Template kind. Supported kinds are %s and %s", hmc.ClusterTemplateKind, hmc.ServiceTemplateKind)
		}

		operation, err := ctrl.CreateOrUpdate(ctx, r.Client, target, func() error {
			utils.AddOwnerReference(target, templateChain)
			return nil
		})
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}

		if operation == controllerutil.OperationResultCreated {
			l.Info(r.templateKind+" was successfully created", "template namespace", templateChain.GetNamespace(), "template name", supportedTemplate.Name)
		}
		if operation == controllerutil.OperationResultUpdated {
			l.Info("Successfully updated OwnerReference on "+r.templateKind, "template namespace", templateChain.GetNamespace(), "template name", supportedTemplate.Name)
		}
	}

	return ctrl.Result{}, errs
}

func (r *TemplateChainReconciler) getTemplates(ctx context.Context, opts *client.ListOptions) (map[string]templateCommon, error) {
	templates := make(map[string]templateCommon)

	switch r.templateKind {
	case hmc.ClusterTemplateKind:
		ctList := &hmc.ClusterTemplateList{}
		err := r.List(ctx, ctList, opts)
		if err != nil {
			return nil, err
		}
		for _, template := range ctList.Items {
			templates[template.Name] = &template
		}
	case hmc.ServiceTemplateKind:
		stList := &hmc.ServiceTemplateList{}
		err := r.List(ctx, stList, opts)
		if err != nil {
			return nil, err
		}
		for _, template := range stList.Items {
			templates[template.Name] = &template
		}
	default:
		return nil, fmt.Errorf("invalid Template kind. Supported kinds are %s and %s", hmc.ClusterTemplateKind, hmc.ServiceTemplateKind)
	}
	return templates, nil
}

func getTemplateNamesManagedByChain(chain templateChain) []string {
	result := make([]string, 0, len(chain.GetSpec().SupportedTemplates))
	for _, tmpl := range chain.GetSpec().SupportedTemplates {
		result = append(result, tmpl.Name)
	}
	return result
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterTemplateChainReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.templateKind = hmc.ClusterTemplateKind

	return ctrl.NewControllerManagedBy(mgr).
		For(&hmc.ClusterTemplateChain{}).
		Complete(r)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceTemplateChainReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.templateKind = hmc.ServiceTemplateKind

	return ctrl.NewControllerManagedBy(mgr).
		For(&hmc.ServiceTemplateChain{}).
		Complete(r)
}
