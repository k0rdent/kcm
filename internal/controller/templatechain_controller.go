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
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/internal/utils/ratelimit"
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
	GetSpec() *kcm.TemplateChainSpec
	GetStatus() *kcm.TemplateChainStatus
}

func (r *ClusterTemplateChainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ClusterTemplateChain")

	clusterTemplateChain := &kcm.ClusterTemplateChain{}
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

	serviceTemplateChain := &kcm.ServiceTemplateChain{}
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

	management := &kcm.Management{}
	if err := r.Get(ctx, client.ObjectKey{Name: kcm.ManagementName}, management); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Management: %w", err)
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping TemplateChain reconciliation")
		return ctrl.Result{}, nil
	}

	if updated, err := utils.AddKCMComponentLabel(ctx, r.Client, templateChain); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{}, err
	}

	if !r.setObjectValidity(templateChain) { // fail fast
		l.Info("TemplateChain is not valid, skipping reconciliation")
		return ctrl.Result{}, r.updateStatus(ctx, templateChain)
	}

	if templateChain.GetNamespace() == r.SystemNamespace ||
		templateChain.GetLabels()[kcm.KCMManagedLabelKey] != kcm.KCMManagedLabelValue {
		l.Info("TemplateChain is not managed, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, errors.Join(r.reconcileObj(ctx, templateChain), r.updateStatus(ctx, templateChain))
}

// setObjectValidity returns if the given object is valid and ready to be proceeded, setting its status accordingly.
func (*TemplateChainReconciler) setObjectValidity(tc templateChain) (valid bool) {
	warnings, isValid := tc.GetSpec().IsValid()
	status := tc.GetStatus()
	status.IsValid = isValid
	status.ValidationErrors = warnings

	return isValid
}

func (r *TemplateChainReconciler) reconcileObj(ctx context.Context, tplChain templateChain) error {
	spec := tplChain.GetSpec()
	if len(spec.SupportedTemplates) == 0 {
		return nil // nothing to do
	}

	l := ctrl.LoggerFrom(ctx)

	l.V(1).Info("Getting system templates")
	systemTemplates, err := r.getTemplates(ctx, &client.ListOptions{Namespace: r.SystemNamespace})
	if err != nil {
		return fmt.Errorf("failed to get system templates: %w", err)
	}

	var errs error
	for _, supportedTemplate := range spec.SupportedTemplates {
		l.V(1).Info("Processing the supported template to create or update it", "supported template", supportedTemplate.Name)
		meta := metav1.ObjectMeta{
			Name:      supportedTemplate.Name,
			Namespace: tplChain.GetNamespace(),
			Labels: map[string]string{
				kcm.KCMManagedLabelKey: kcm.KCMManagedLabelValue,
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
		case kcm.ClusterTemplateKind:
			clusterTemplate, ok := source.(*kcm.ClusterTemplate)
			if !ok {
				return fmt.Errorf("type assertion failed: expected ClusterTemplate but got %T", source)
			}
			spec := clusterTemplate.Spec
			spec.Helm = kcm.HelmSpec{ChartRef: clusterTemplate.Status.ChartRef}
			target = &kcm.ClusterTemplate{ObjectMeta: meta, Spec: spec}
		case kcm.ServiceTemplateKind:
			serviceTemplate, ok := source.(*kcm.ServiceTemplate)
			if !ok {
				return fmt.Errorf("type assertion failed: expected ServiceTemplate but got %T", source)
			}
			spec := serviceTemplate.Spec
			spec.Helm = &kcm.HelmSpec{ChartRef: serviceTemplate.Status.ChartRef}
			target = &kcm.ServiceTemplate{ObjectMeta: meta, Spec: spec}
		default:
			return fmt.Errorf("invalid Template kind. Supported kinds are %s and %s", kcm.ClusterTemplateKind, kcm.ServiceTemplateKind)
		}

		operation, err := ctrl.CreateOrUpdate(ctx, r.Client, target, func() error {
			utils.AddOwnerReference(target, tplChain)
			return nil
		})
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}

		if operation == controllerutil.OperationResultCreated {
			l.Info(r.templateKind+" was successfully created", "template namespace", tplChain.GetNamespace(), "template name", supportedTemplate.Name)
		}
		if operation == controllerutil.OperationResultUpdated {
			l.Info("Successfully updated OwnerReference on "+r.templateKind, "template namespace", tplChain.GetNamespace(), "template name", supportedTemplate.Name)
		}
	}

	l.V(1).Info("Processed all templates of the template chain")

	return errs
}

func (r *TemplateChainReconciler) getTemplates(ctx context.Context, opts *client.ListOptions) (map[string]templateCommon, error) {
	templates := make(map[string]templateCommon)

	switch r.templateKind {
	case kcm.ClusterTemplateKind:
		ctList := &kcm.ClusterTemplateList{}
		err := r.List(ctx, ctList, opts)
		if err != nil {
			return nil, err
		}
		for _, template := range ctList.Items {
			templates[template.Name] = &template
		}
	case kcm.ServiceTemplateKind:
		stList := &kcm.ServiceTemplateList{}
		err := r.List(ctx, stList, opts)
		if err != nil {
			return nil, err
		}
		for _, template := range stList.Items {
			templates[template.Name] = &template
		}
	default:
		return nil, fmt.Errorf("invalid Template kind. Supported kinds are %s and %s", kcm.ClusterTemplateKind, kcm.ServiceTemplateKind)
	}
	return templates, nil
}

func (r *TemplateChainReconciler) updateStatus(ctx context.Context, obj client.Object) error {
	ctrl.LoggerFrom(ctx).V(1).Info("Updating object status")
	if err := r.Client.Status().Update(ctx, obj); err != nil {
		return fmt.Errorf("failed to update status for %s %s: %w", obj.GetObjectKind().GroupVersionKind().Kind, client.ObjectKeyFromObject(obj), err)
	}

	return nil
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
	r.templateKind = kcm.ClusterTemplateKind

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcm.ClusterTemplateChain{}).
		Complete(r)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceTemplateChainReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.templateKind = kcm.ServiceTemplateKind

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimit.DefaultFastSlow(),
		}).
		For(&kcm.ServiceTemplateChain{}).
		Complete(r)
}
