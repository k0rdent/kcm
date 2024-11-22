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

package webhook

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/Mirantis/hmc/api/v1alpha1"
	"github.com/Mirantis/hmc/internal/helm"
)

var errTemplateDeletionForbidden = errors.New("template deletion is forbidden")

type TemplateValidator struct {
	client.Client
	SystemNamespace   string
	templateKind      string
	templateChainKind string
}

type ClusterTemplateValidator struct {
	TemplateValidator
}

func (v *ClusterTemplateValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	v.templateKind = v1alpha1.ClusterTemplateKind
	v.templateChainKind = v1alpha1.ClusterTemplateChainKind
	return ctrl.NewWebhookManagedBy(mgr).
		For(&v1alpha1.ClusterTemplate{}).
		WithValidator(v).
		WithDefaulter(v).
		Complete()
}

var (
	_ webhook.CustomValidator = &ClusterTemplateValidator{}
	_ webhook.CustomDefaulter = &ClusterTemplateValidator{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (*ClusterTemplateValidator) ValidateCreate(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (*ClusterTemplateValidator) ValidateUpdate(_ context.Context, _, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *ClusterTemplateValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	template, ok := obj.(*v1alpha1.ClusterTemplate)
	if !ok {
		return admission.Warnings{"Wrong object"}, apierrors.NewBadRequest(fmt.Sprintf("expected ClusterTemplate but got a %T", obj))
	}

	inUseByCluster, err := v.templateIsInUseByCluster(ctx, template)
	if err != nil {
		return nil, err
	}
	if inUseByCluster {
		return admission.Warnings{fmt.Sprintf("The %s object can't be removed if ManagedCluster objects referencing it still exist", v.templateKind)}, errTemplateDeletionForbidden
	}

	owners := getOwnersWithKind(template, v.templateChainKind)
	if len(owners) > 0 {
		return admission.Warnings{fmt.Sprintf("The %s object can't be removed if it is managed by %s: %s",
			v.templateKind, v.templateChainKind, strings.Join(owners, ", "))}, errTemplateDeletionForbidden
	}

	return nil, nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (*ClusterTemplateValidator) Default(_ context.Context, obj runtime.Object) error {
	template, ok := obj.(*v1alpha1.ClusterTemplate)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected ClusterTemplate but got a %T", obj))
	}
	setHelmChartDefaults(template.GetHelmSpec())
	return nil
}

type ServiceTemplateValidator struct {
	TemplateValidator
}

func (v *ServiceTemplateValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	v.templateKind = v1alpha1.ServiceTemplateKind
	v.templateChainKind = v1alpha1.ServiceTemplateChainKind
	return ctrl.NewWebhookManagedBy(mgr).
		For(&v1alpha1.ServiceTemplate{}).
		WithValidator(v).
		WithDefaulter(v).
		Complete()
}

var (
	_ webhook.CustomValidator = &ServiceTemplateValidator{}
	_ webhook.CustomDefaulter = &ServiceTemplateValidator{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (*ServiceTemplateValidator) ValidateCreate(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (*ServiceTemplateValidator) ValidateUpdate(_ context.Context, _, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *ServiceTemplateValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	tmpl, ok := obj.(*v1alpha1.ServiceTemplate)
	if !ok {
		return admission.Warnings{"Wrong object"}, apierrors.NewBadRequest(fmt.Sprintf("expected ServiceTemplate but got a %T", obj))
	}

	inUseByCluster, err := v.templateIsInUseByCluster(ctx, tmpl)
	if err != nil {
		return nil, fmt.Errorf("failed to check if the ServiceTemplate %s/%s is in use: %w", tmpl.Namespace, tmpl.Name, err)
	}
	if inUseByCluster {
		return admission.Warnings{fmt.Sprintf("The %s object can't be removed if ManagedCluster objects referencing it still exist", v.templateKind)}, errTemplateDeletionForbidden
	}

	owners := getOwnersWithKind(tmpl, v.templateChainKind)
	if len(owners) > 0 {
		return admission.Warnings{fmt.Sprintf("The %s object can't be removed if it is managed by %s: %s",
			v.templateKind, v.templateChainKind, strings.Join(owners, ", "))}, errTemplateDeletionForbidden
	}

	// MultiClusterServices can only refer to serviceTemplates in system namespace.
	if tmpl.Namespace == v.SystemNamespace {
		multiSvcClusters := &v1alpha1.MultiClusterServiceList{}
		if err := v.Client.List(ctx, multiSvcClusters,
			client.MatchingFields{v1alpha1.MultiClusterServiceTemplatesIndexKey: tmpl.Name},
			client.Limit(1)); err != nil {
			return nil, err
		}

		if len(multiSvcClusters.Items) > 0 {
			return admission.Warnings{"The ServiceTemplate object can't be removed if MultiClusterService objects referencing it still exist"}, errTemplateDeletionForbidden
		}
	}

	return nil, nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (*ServiceTemplateValidator) Default(_ context.Context, obj runtime.Object) error {
	template, ok := obj.(*v1alpha1.ServiceTemplate)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected ServiceTemplate but got a %T", obj))
	}
	setHelmChartDefaults(template.GetHelmSpec())
	return nil
}

type ProviderTemplateValidator struct {
	TemplateValidator
}

func (v *ProviderTemplateValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	v.templateKind = v1alpha1.ProviderTemplateKind
	return ctrl.NewWebhookManagedBy(mgr).
		For(&v1alpha1.ProviderTemplate{}).
		WithValidator(v).
		WithDefaulter(v).
		Complete()
}

var (
	_ webhook.CustomValidator = &ProviderTemplateValidator{}
	_ webhook.CustomDefaulter = &ProviderTemplateValidator{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (*ProviderTemplateValidator) ValidateCreate(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (*ProviderTemplateValidator) ValidateUpdate(_ context.Context, _, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *ProviderTemplateValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	template, ok := obj.(*v1alpha1.ProviderTemplate)
	if !ok {
		return admission.Warnings{"Wrong object"}, apierrors.NewBadRequest(fmt.Sprintf("expected ProviderTemplate but got a %T", obj))
	}

	owners := getOwnersWithKind(template, v1alpha1.ReleaseKind)
	if len(owners) > 0 {
		return admission.Warnings{fmt.Sprintf("The ProviderTemplate %s cannot be removed while it is part of existing Releases: %s",
			template.GetName(), strings.Join(owners, ", "))}, errTemplateDeletionForbidden
	}

	mgmt, err := getManagement(ctx, v.Client)
	if err != nil {
		if errors.Is(err, errManagementIsNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if slices.Contains(mgmt.Templates(), template.Name) {
		return admission.Warnings{fmt.Sprintf("The ProviderTemplate %s cannot be removed while it is used in the Management spec",
			template.GetName())}, errTemplateDeletionForbidden
	}
	return nil, nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (*ProviderTemplateValidator) Default(_ context.Context, obj runtime.Object) error {
	template, ok := obj.(*v1alpha1.ProviderTemplate)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected ProviderTemplate but got a %T", obj))
	}
	setHelmChartDefaults(template.GetHelmSpec())
	return nil
}

func (v TemplateValidator) templateIsInUseByCluster(ctx context.Context, template client.Object) (bool, error) {
	var key string

	switch v.templateKind {
	case v1alpha1.ClusterTemplateKind:
		key = v1alpha1.ManagedClusterTemplateIndexKey
	case v1alpha1.ServiceTemplateKind:
		key = v1alpha1.ManagedClusterServiceTemplatesIndexKey
	default:
		return false, fmt.Errorf("invalid Template kind %s. Supported values are: %s and %s", v.templateKind, v1alpha1.ClusterTemplateKind, v1alpha1.ServiceTemplateKind)
	}

	managedClusters := &v1alpha1.ManagedClusterList{}
	if err := v.Client.List(ctx, managedClusters,
		client.InNamespace(template.GetNamespace()),
		client.MatchingFields{key: template.GetName()},
		client.Limit(1)); err != nil {
		return false, err
	}
	if len(managedClusters.Items) > 0 {
		return true, nil
	}
	return false, nil
}

func getOwnersWithKind(template client.Object, kind string) []string {
	var owners []string
	for _, ownerRef := range template.GetOwnerReferences() {
		if ownerRef.Kind == kind {
			owners = append(owners, ownerRef.Name)
		}
	}
	return owners
}

func setHelmChartDefaults(helmSpec *v1alpha1.HelmSpec) {
	if helmSpec == nil || helmSpec.ChartSpec == nil {
		return
	}
	chartSpec := helmSpec.ChartSpec
	if chartSpec.SourceRef.Name == "" && chartSpec.SourceRef.Kind == "" {
		chartSpec.SourceRef = v1alpha1.DefaultSourceRef
	}
	if chartSpec.Interval.Duration == 0 {
		chartSpec.Interval.Duration = helm.DefaultReconcileInterval
	}
}
