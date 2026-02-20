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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/record"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	labelsutil "github.com/K0rdent/kcm/internal/util/labels"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
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
	GetSpec() *kcmv1.TemplateChainSpec
	GetStatus() *kcmv1.TemplateChainStatus
}

func (r *ClusterTemplateChainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ClusterTemplateChain")

	clusterTemplateChain := &kcmv1.ClusterTemplateChain{}
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

	serviceTemplateChain := &kcmv1.ServiceTemplateChain{}
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

	management := &kcmv1.Management{}
	if err := r.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, management); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Management: %w", err)
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping TemplateChain reconciliation")
		return ctrl.Result{}, nil
	}

	if updated, err := labelsutil.AddKCMComponentLabel(ctx, r.Client, templateChain); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{}, err
	}

	updated, valid := r.setObjectValidity(templateChain)
	if updated {
		l.V(1).Info("TemplateChain validity state changed", "valid", valid)
		return ctrl.Result{}, r.updateStatus(ctx, templateChain)
	}
	if !valid {
		l.Info("TemplateChain is not valid, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	if templateChain.GetNamespace() == r.SystemNamespace ||
		templateChain.GetLabels()[kcmv1.KCMManagedLabelKey] != kcmv1.KCMManagedLabelValue {
		l.Info("TemplateChain is not managed, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, errors.Join(r.reconcileObj(ctx, templateChain), r.updateStatus(ctx, templateChain))
}

// setObjectValidity returns if the given object is valid and ready to be proceeded, setting its status accordingly.
func (*TemplateChainReconciler) setObjectValidity(tc templateChain) (updated, valid bool) {
	warnings, valid := tc.GetSpec().IsValid()
	status := tc.GetStatus()
	updated = status.Valid != valid
	status.Valid = valid
	status.ValidationError = strings.Join(warnings, ";")
	return updated, valid
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
				kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
			},
		}

		source, found := systemTemplates[supportedTemplate.Name]
		if !found {
			errs = errors.Join(errs, fmt.Errorf("source %s %s/%s is not found", r.templateKind, r.SystemNamespace, supportedTemplate.Name))
			continue
		}
		// if the template status is not valid, it means that the template was not reconciled yet,
		// hence there will be no valid status.
		if !source.GetCommonStatus().Valid {
			errs = errors.Join(errs, fmt.Errorf("source %s %s/%s does not have chart reference yet", r.templateKind, r.SystemNamespace, supportedTemplate.Name))
			continue
		}

		var target client.Object
		switch r.templateKind {
		case kcmv1.ClusterTemplateKind:
			clusterTemplate, ok := source.(*kcmv1.ClusterTemplate)
			if !ok {
				return fmt.Errorf("type assertion failed: expected ClusterTemplate but got %T", source)
			}
			spec := clusterTemplate.Spec
			spec.Helm = kcmv1.HelmSpec{ChartRef: clusterTemplate.Status.ChartRef}
			target = &kcmv1.ClusterTemplate{ObjectMeta: meta, Spec: spec}
		case kcmv1.ServiceTemplateKind:
			serviceTemplate, ok := source.(*kcmv1.ServiceTemplate)
			if !ok {
				return fmt.Errorf("type assertion failed: expected ServiceTemplate but got %T", source)
			}
			spec := serviceTemplate.Spec
			if spec.Helm != nil && (spec.Helm.ChartRef != nil || spec.Helm.ChartSpec != nil) {
				spec.Helm = &kcmv1.HelmSpec{ChartRef: serviceTemplate.Status.ChartRef}
			} else {
				status := serviceTemplate.Status.SourceStatus
				// we won't allow cross-namespace references to sources of the type of ConfigMap/Secret
				// as this may lead to a security breach.
				if status.Kind == kcmv1.SecretKind || status.Kind == kcmv1.ConfigMapKind {
					return fmt.Errorf("source of a kind %s cannot be populated across namespaces", status.Kind)
				}
				// in opposite we allow cross-namespace references to sources of the type of GitRepository,
				// Bucket or OCIRepository, as possible secrets won't be directly exposed to the user.
				sourceRef := &kcmv1.LocalSourceRef{
					Kind:      status.Kind,
					Name:      status.Name,
					Namespace: status.Namespace,
				}
				if serviceTemplate.Spec.Helm != nil {
					spec.Helm.ChartSource.RemoteSourceSpec = nil
					spec.Helm.ChartSource.LocalSourceRef = sourceRef
				}
				if serviceTemplate.Spec.Kustomize != nil {
					spec.Kustomize.RemoteSourceSpec = nil
					spec.Kustomize.LocalSourceRef = sourceRef
				}
				if serviceTemplate.Spec.Resources != nil {
					spec.Resources.RemoteSourceSpec = nil
					spec.Resources.LocalSourceRef = sourceRef
				}
			}
			target = &kcmv1.ServiceTemplate{ObjectMeta: meta, Spec: spec}
		default:
			return fmt.Errorf("invalid Template kind. Supported kinds are %s and %s", kcmv1.ClusterTemplateKind, kcmv1.ServiceTemplateKind)
		}

		operation, err := ctrl.CreateOrUpdate(ctx, r.Client, target, func() error {
			kubeutil.AddOwnerReference(target, tplChain)
			return nil
		})
		if err != nil {
			record.Warnf(tplChain, target, r.templateKind+"CreationFailed", "creating", "Failed to create %s %s: %v", r.templateKind, client.ObjectKeyFromObject(target), err)
			errs = errors.Join(errs, err)
			continue
		}

		if operation == controllerutil.OperationResultCreated {
			record.Eventf(tplChain, target, r.templateKind+"Created", "creating", "Successfully created %s %s", r.templateKind, client.ObjectKeyFromObject(target))
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
	case kcmv1.ClusterTemplateKind:
		ctList := &kcmv1.ClusterTemplateList{}
		err := r.List(ctx, ctList, opts)
		if err != nil {
			return nil, err
		}
		for _, template := range ctList.Items {
			templates[template.Name] = &template
		}
	case kcmv1.ServiceTemplateKind:
		stList := &kcmv1.ServiceTemplateList{}
		err := r.List(ctx, stList, opts)
		if err != nil {
			return nil, err
		}
		for _, template := range stList.Items {
			templates[template.Name] = &template
		}
	default:
		return nil, fmt.Errorf("invalid Template kind. Supported kinds are %s and %s", kcmv1.ClusterTemplateKind, kcmv1.ServiceTemplateKind)
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
	r.templateKind = kcmv1.ClusterTemplateKind

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.ClusterTemplateChain{}).
		Complete(r)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceTemplateChainReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.templateKind = kcmv1.ServiceTemplateKind

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.ServiceTemplateChain{}).
		Complete(r)
}
