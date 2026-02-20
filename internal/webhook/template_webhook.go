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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/helm"
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
	v.templateKind = kcmv1.ClusterTemplateKind
	v.templateChainKind = kcmv1.ClusterTemplateChainKind
	return ctrl.NewWebhookManagedBy(mgr, &kcmv1.ClusterTemplate{}).
		WithValidator(v).
		WithDefaulter(v).
		Complete()
}

var (
	_ admission.Validator[*kcmv1.ClusterTemplate] = &ClusterTemplateValidator{}
	_ admission.Defaulter[*kcmv1.ClusterTemplate] = &ClusterTemplateValidator{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (*ClusterTemplateValidator) ValidateCreate(_ context.Context, _ *kcmv1.ClusterTemplate) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (*ClusterTemplateValidator) ValidateUpdate(_ context.Context, _, _ *kcmv1.ClusterTemplate) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *ClusterTemplateValidator) ValidateDelete(ctx context.Context, obj *kcmv1.ClusterTemplate) (admission.Warnings, error) {
	inUseByCluster, err := v.templateIsInUseByCluster(ctx, obj)
	if err != nil {
		return nil, err
	}
	if inUseByCluster {
		return admission.Warnings{fmt.Sprintf("The %s object can't be removed if ClusterDeployment objects referencing it still exist", v.templateKind)}, errTemplateDeletionForbidden
	}

	owners, err := getOwnersWithKind(ctx, v.Client, obj, v.templateChainKind)
	if err != nil {
		return nil, fmt.Errorf("failed to get owners with kind %s: %w", v.templateChainKind, err)
	}
	if len(owners) > 0 {
		return admission.Warnings{fmt.Sprintf("The %s object can't be removed if it is managed by %s: %s",
			v.templateKind, v.templateChainKind, strings.Join(owners, ", "))}, errTemplateDeletionForbidden
	}

	return nil, nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (*ClusterTemplateValidator) Default(_ context.Context, obj *kcmv1.ClusterTemplate) error {
	setHelmChartDefaults(obj.GetHelmSpec())
	return nil
}

type ServiceTemplateValidator struct {
	TemplateValidator
}

func (v *ServiceTemplateValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	v.templateKind = kcmv1.ServiceTemplateKind
	v.templateChainKind = kcmv1.ServiceTemplateChainKind
	return ctrl.NewWebhookManagedBy(mgr, &kcmv1.ServiceTemplate{}).
		WithValidator(v).
		WithDefaulter(v).
		Complete()
}

var (
	_ admission.Validator[*kcmv1.ServiceTemplate] = &ServiceTemplateValidator{}
	_ admission.Defaulter[*kcmv1.ServiceTemplate] = &ServiceTemplateValidator{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (*ServiceTemplateValidator) ValidateCreate(_ context.Context, _ *kcmv1.ServiceTemplate) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (*ServiceTemplateValidator) ValidateUpdate(_ context.Context, _, _ *kcmv1.ServiceTemplate) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *ServiceTemplateValidator) ValidateDelete(ctx context.Context, obj *kcmv1.ServiceTemplate) (admission.Warnings, error) {
	inUseByCluster, err := v.templateIsInUseByCluster(ctx, obj)
	if err != nil {
		return nil, fmt.Errorf("failed to check if the ServiceTemplate %s/%s is in use: %w", obj.Namespace, obj.Name, err)
	}
	if inUseByCluster {
		return admission.Warnings{fmt.Sprintf("The %s object can't be removed if ClusterDeployment objects referencing it still exist", v.templateKind)}, errTemplateDeletionForbidden
	}

	owners, err := getOwnersWithKind(ctx, v.Client, obj, v.templateChainKind)
	if err != nil {
		return nil, fmt.Errorf("failed to get owners with kind %s: %w", v.templateChainKind, err)
	}
	if len(owners) > 0 {
		return admission.Warnings{fmt.Sprintf("The %s object can't be removed if it is managed by %s: %s",
			v.templateKind, v.templateChainKind, strings.Join(owners, ", "))}, errTemplateDeletionForbidden
	}

	// MultiClusterServices can only refer to serviceTemplates in system namespace.
	if obj.Namespace == v.SystemNamespace {
		multiSvcClusters := &kcmv1.MultiClusterServiceList{}
		if err := v.List(ctx, multiSvcClusters,
			client.MatchingFields{kcmv1.MultiClusterServiceTemplatesIndexKey: obj.Name},
			client.Limit(1)); err != nil {
			return nil, err
		}

		var mscNames []string
		for _, msc := range multiSvcClusters.Items {
			if msc.GetDeletionTimestamp().IsZero() {
				mscNames = append(mscNames, msc.Name)
			}
		}

		if len(mscNames) > 0 {
			return admission.Warnings{fmt.Sprintf("The %s ServiceTemplate object can't be removed if MultiClusterService objects [%s] referencing it still exist", obj.Name, strings.Join(mscNames, ","))}, errTemplateDeletionForbidden
		}
	}

	return nil, nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (*ServiceTemplateValidator) Default(_ context.Context, obj *kcmv1.ServiceTemplate) error {
	setHelmChartDefaults(obj.GetHelmSpec())
	return nil
}

type ProviderTemplateValidator struct {
	TemplateValidator
}

func (v *ProviderTemplateValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	v.templateKind = kcmv1.ProviderTemplateKind
	return ctrl.NewWebhookManagedBy(mgr, &kcmv1.ProviderTemplate{}).
		WithValidator(v).
		WithDefaulter(v).
		Complete()
}

var (
	_ admission.Validator[*kcmv1.ProviderTemplate] = &ProviderTemplateValidator{}
	_ admission.Defaulter[*kcmv1.ProviderTemplate] = &ProviderTemplateValidator{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (*ProviderTemplateValidator) ValidateCreate(_ context.Context, _ *kcmv1.ProviderTemplate) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (*ProviderTemplateValidator) ValidateUpdate(_ context.Context, _, _ *kcmv1.ProviderTemplate) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *ProviderTemplateValidator) ValidateDelete(ctx context.Context, obj *kcmv1.ProviderTemplate) (admission.Warnings, error) {
	owners, err := getOwnersWithKind(ctx, v.Client, obj, kcmv1.ReleaseKind)
	if err != nil {
		return nil, fmt.Errorf("failed to get owners with kind %s: %w", kcmv1.ReleaseKind, err)
	}
	if len(owners) > 0 {
		return admission.Warnings{fmt.Sprintf("The ProviderTemplate %s cannot be removed while it is part of existing Releases: %s",
			obj.Name, strings.Join(owners, ", "))}, errTemplateDeletionForbidden
	}

	mgmt, err := getManagement(ctx, v.Client)
	if err != nil {
		if errors.Is(err, errManagementIsNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if mgmt.DeletionTimestamp.IsZero() && slices.Contains(mgmt.Templates(), obj.Name) {
		return admission.Warnings{fmt.Sprintf("The ProviderTemplate %s cannot be removed while it is used in the Management spec",
			obj.Name)}, errTemplateDeletionForbidden
	}
	return nil, nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (*ProviderTemplateValidator) Default(_ context.Context, obj *kcmv1.ProviderTemplate) error {
	setHelmChartDefaults(obj.GetHelmSpec())
	return nil
}

func (v TemplateValidator) templateIsInUseByCluster(ctx context.Context, template client.Object) (bool, error) {
	var key string

	switch v.templateKind {
	case kcmv1.ClusterTemplateKind:
		key = kcmv1.ClusterDeploymentTemplateIndexKey
	case kcmv1.ServiceTemplateKind:
		key = kcmv1.ClusterDeploymentServiceTemplatesIndexKey
	default:
		return false, fmt.Errorf("invalid Template kind %s. Supported values are: %s and %s", v.templateKind, kcmv1.ClusterTemplateKind, kcmv1.ServiceTemplateKind)
	}

	clusterDeployments := &kcmv1.ClusterDeploymentList{}
	if err := v.List(ctx, clusterDeployments,
		client.InNamespace(template.GetNamespace()),
		client.MatchingFields{key: template.GetName()},
		client.Limit(1)); err != nil {
		return false, err
	}
	if len(clusterDeployments.Items) > 0 {
		return true, nil
	}
	return false, nil
}

func getOwnersWithKind(ctx context.Context, cl client.Client, template client.Object, kind string) ([]string, error) {
	var owners []string
	var errs error
	for _, ownerRef := range template.GetOwnerReferences() {
		if ownerRef.Kind == kind {
			exists, err := ownerExists(ctx, cl, ownerRef, template.GetNamespace())
			if err != nil {
				errs = errors.Join(errs, err)
				continue
			}
			if exists {
				owners = append(owners, ownerRef.Name)
			}
		}
	}
	return owners, errs
}

func ownerExists(ctx context.Context, cl client.Client, ownerRef metav1.OwnerReference, namespace string) (bool, error) {
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      ownerRef.Name,
	}

	obj := &metav1.PartialObjectMetadata{}
	obj.SetGroupVersionKind(schema.FromAPIVersionAndKind(ownerRef.APIVersion, ownerRef.Kind))
	err := cl.Get(ctx, key, obj)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func setHelmChartDefaults(helmSpec *kcmv1.HelmSpec) {
	if helmSpec == nil || helmSpec.ChartSpec == nil {
		return
	}
	chartSpec := helmSpec.ChartSpec
	if chartSpec.SourceRef.Name == "" && chartSpec.SourceRef.Kind == "" {
		chartSpec.SourceRef = kcmv1.DefaultSourceRef
	}
	if chartSpec.Interval.Duration == 0 {
		chartSpec.Interval.Duration = helm.DefaultReconcileInterval
	}
}
