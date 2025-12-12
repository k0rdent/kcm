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

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
)

type ClusterDeploymentValidator struct {
	client.Client

	SystemNamespace            string
	ValidateClusterUpgradePath bool
}

const invalidClusterDeploymentMsg = "the ClusterDeployment is invalid"

var errClusterUpgradeForbidden = errors.New("cluster upgrade is forbidden")

func (v *ClusterDeploymentValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&kcmv1.ClusterDeployment{}).
		WithValidator(v).
		WithDefaulter(v).
		Complete()
}

var (
	_ webhook.CustomValidator = &ClusterDeploymentValidator{}
	_ webhook.CustomDefaulter = &ClusterDeploymentValidator{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (v *ClusterDeploymentValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	clusterDeployment, ok := obj.(*kcmv1.ClusterDeployment)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected clusterDeployment but got a %T", obj))
	}

	template, err := v.getClusterDeploymentTemplate(ctx, clusterDeployment.Namespace, clusterDeployment.Spec.Template)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	if err := isClusterTemplateValid(template); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	if err := validationutil.ClusterTemplateProviders(ctx, v.Client, template, clusterDeployment); err != nil {
		return admission.Warnings{"Failed to validate required providers"}, fmt.Errorf("failed to validate required providers: %w", err)
	}

	if err := validationutil.ClusterTemplateK8sCompatibility(ctx, v.Client, template, client.ObjectKeyFromObject(clusterDeployment), &clusterDeployment.Spec.ServiceSpec); err != nil {
		return admission.Warnings{"Failed to validate k8s version compatibility with ServiceTemplates"}, fmt.Errorf("failed to validate k8s compatibility: %w", err)
	}

	if err := validationutil.ClusterDeployCredential(ctx, v.Client, v.SystemNamespace, clusterDeployment, template); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	if err := validationutil.ServicesHaveValidTemplates(ctx, v.Client, clusterDeployment.Spec.ServiceSpec.Services, clusterDeployment.Namespace); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	if err := validationutil.ValidateServiceDependencyOverall(clusterDeployment.Spec.ServiceSpec.Services); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (v *ClusterDeploymentValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldClusterDeployment, ok := oldObj.(*kcmv1.ClusterDeployment)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected ClusterDeployment but got a %T", oldObj))
	}
	newClusterDeployment, ok := newObj.(*kcmv1.ClusterDeployment)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected ClusterDeployment but got a %T", newObj))
	}
	oldTemplate := oldClusterDeployment.Spec.Template
	newTemplate := newClusterDeployment.Spec.Template

	template, err := v.getClusterDeploymentTemplate(ctx, newClusterDeployment.Namespace, newTemplate)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	if oldTemplate != newTemplate {
		if v.ValidateClusterUpgradePath && !slices.Contains(oldClusterDeployment.Status.AvailableUpgrades, newTemplate) {
			msg := fmt.Sprintf("Cluster can't be upgraded from %s to %s. This upgrade sequence is not allowed", oldTemplate, newTemplate)
			return admission.Warnings{msg}, errClusterUpgradeForbidden
		}

		if err := isClusterTemplateValid(template); err != nil {
			return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
		}

		if err := validationutil.ClusterTemplateProviders(ctx, v.Client, template, newClusterDeployment); err != nil {
			return admission.Warnings{"Failed to validate required providers"}, fmt.Errorf("failed to validate required providers: %w", err)
		}

		if err := validationutil.ClusterTemplateK8sCompatibility(ctx, v.Client, template, client.ObjectKeyFromObject(newClusterDeployment), &newClusterDeployment.Spec.ServiceSpec); err != nil {
			return admission.Warnings{"Failed to validate k8s version compatibility with ServiceTemplates"}, fmt.Errorf("failed to validate k8s compatibility: %w", err)
		}
	}

	if err := validationutil.ClusterDeployCredential(ctx, v.Client, v.SystemNamespace, newClusterDeployment, template); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	if err := validationutil.ServicesHaveValidTemplates(ctx, v.Client, newClusterDeployment.Spec.ServiceSpec.Services, newClusterDeployment.Namespace); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	if err := validationutil.ValidateServiceDependencyOverall(newClusterDeployment.Spec.ServiceSpec.Services); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *ClusterDeploymentValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	clusterDeployment, ok := obj.(*kcmv1.ClusterDeployment)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected clusterDeployment but got a %T", obj))
	}
	err := validationutil.ClusterDeploymentDeletionAllowed(ctx, v.Client, clusterDeployment)
	if err != nil {
		return nil, err
	}
	return nil, nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (v *ClusterDeploymentValidator) Default(ctx context.Context, obj runtime.Object) error {
	clusterDeployment, ok := obj.(*kcmv1.ClusterDeployment)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected clusterDeployment but got a %T", obj))
	}

	// Only apply defaults when there's no configuration provided;
	// if template ref is empty, then nothing to default
	if clusterDeployment.Spec.Config != nil || clusterDeployment.Spec.Template == "" {
		return nil
	}

	template, err := v.getClusterDeploymentTemplate(ctx, clusterDeployment.Namespace, clusterDeployment.Spec.Template)
	if err != nil {
		return fmt.Errorf("failed to get ClusterTemplate for the ClusterDeployment %s: %w", client.ObjectKeyFromObject(clusterDeployment), err)
	}

	if err := isClusterTemplateValid(template); err != nil {
		return err
	}

	if template.Status.Config == nil {
		return nil
	}

	clusterDeployment.Spec.DryRun = true
	clusterDeployment.Spec.Config = &apiextv1.JSON{Raw: template.Status.Config.Raw}

	return nil
}

func (v *ClusterDeploymentValidator) getClusterDeploymentTemplate(ctx context.Context, templateNamespace, templateName string) (tpl *kcmv1.ClusterTemplate, err error) {
	tpl = new(kcmv1.ClusterTemplate)
	return tpl, v.Get(ctx, client.ObjectKey{Namespace: templateNamespace, Name: templateName}, tpl)
}

func isClusterTemplateValid(ct *kcmv1.ClusterTemplate) error {
	if !ct.Status.Valid {
		return fmt.Errorf("the ClusterTemplate %s is invalid with the error: %s", client.ObjectKeyFromObject(ct), ct.Status.ValidationError)
	}

	return nil
}
