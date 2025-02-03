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

	"github.com/Masterminds/semver/v3"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1alpha1"
	providersloader "github.com/K0rdent/kcm/internal/providers"
)

type ClusterDeploymentValidator struct {
	client.Client
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

	if err := isTemplateValid(template.GetCommonStatus()); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	if err := validateK8sCompatibility(ctx, v.Client, template, clusterDeployment); err != nil {
		return admission.Warnings{"Failed to validate k8s version compatibility with ServiceTemplates"}, fmt.Errorf("failed to validate k8s compatibility: %w", err)
	}

	if err := v.validateCredential(ctx, clusterDeployment, template); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	if err := ValidateCrossNamespaceRefs(ctx, clusterDeployment.Namespace, &clusterDeployment.Spec.ServiceSpec); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	if err := validateServices(ctx, v.Client, clusterDeployment.Namespace, clusterDeployment.Spec.ServiceSpec.Services); err != nil {
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
		if !slices.Contains(oldClusterDeployment.Status.AvailableUpgrades, newTemplate) {
			msg := fmt.Sprintf("Cluster can't be upgraded from %s to %s. This upgrade sequence is not allowed", oldTemplate, newTemplate)
			return admission.Warnings{msg}, errClusterUpgradeForbidden
		}

		if err := isTemplateValid(template.GetCommonStatus()); err != nil {
			return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
		}

		if err := validateK8sCompatibility(ctx, v.Client, template, newClusterDeployment); err != nil {
			return admission.Warnings{"Failed to validate k8s version compatibility with ServiceTemplates"}, fmt.Errorf("failed to validate k8s compatibility: %w", err)
		}
	}

	if err := v.validateCredential(ctx, newClusterDeployment, template); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	if err := ValidateCrossNamespaceRefs(ctx, newClusterDeployment.Namespace, &newClusterDeployment.Spec.ServiceSpec); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	if err := validateServices(ctx, v.Client, newClusterDeployment.Namespace, newClusterDeployment.Spec.ServiceSpec.Services); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterDeploymentMsg, err)
	}

	return nil, nil
}

func validateK8sCompatibility(ctx context.Context, cl client.Client, template *kcmv1.ClusterTemplate, mc *kcmv1.ClusterDeployment) error {
	if len(mc.Spec.ServiceSpec.Services) == 0 || template.Status.KubernetesVersion == "" {
		return nil // nothing to do
	}

	mcVersion, err := semver.NewVersion(template.Status.KubernetesVersion)
	if err != nil { // should never happen
		return fmt.Errorf("failed to parse k8s version %s of the ClusterDeployment %s/%s: %w", template.Status.KubernetesVersion, mc.Namespace, mc.Name, err)
	}

	for _, v := range mc.Spec.ServiceSpec.Services {
		if v.Disable {
			continue
		}

		svcTpl := new(kcmv1.ServiceTemplate)
		if err := cl.Get(ctx, client.ObjectKey{Namespace: mc.Namespace, Name: v.Template}, svcTpl); err != nil {
			return fmt.Errorf("failed to get ServiceTemplate %s/%s: %w", mc.Namespace, v.Template, err)
		}

		constraint := svcTpl.Status.KubernetesConstraint
		if constraint == "" {
			continue
		}

		tplConstraint, err := semver.NewConstraint(constraint)
		if err != nil { // should never happen
			return fmt.Errorf("failed to parse k8s constrained version %s of the ServiceTemplate %s/%s: %w", constraint, mc.Namespace, v.Template, err)
		}

		if !tplConstraint.Check(mcVersion) {
			return fmt.Errorf("k8s version %s of the ClusterDeployment %s/%s does not satisfy constrained version %s from the ServiceTemplate %s/%s",
				template.Status.KubernetesVersion, mc.Namespace, mc.Name,
				constraint, mc.Namespace, v.Template)
		}
	}

	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (*ClusterDeploymentValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
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
		return fmt.Errorf("could not get template for the clusterDeployment: %w", err)
	}

	if err := isTemplateValid(template.GetCommonStatus()); err != nil {
		return fmt.Errorf("template is invalid: %w", err)
	}

	if template.Status.Config == nil {
		return nil
	}

	clusterDeployment.Spec.DryRun = true
	clusterDeployment.Spec.Config = &apiextensionsv1.JSON{Raw: template.Status.Config.Raw}

	return nil
}

func (v *ClusterDeploymentValidator) getClusterDeploymentTemplate(ctx context.Context, templateNamespace, templateName string) (tpl *kcmv1.ClusterTemplate, err error) {
	tpl = new(kcmv1.ClusterTemplate)
	return tpl, v.Get(ctx, client.ObjectKey{Namespace: templateNamespace, Name: templateName}, tpl)
}

func (v *ClusterDeploymentValidator) getClusterDeploymentCredential(ctx context.Context, credNamespace, credName string) (*kcmv1.Credential, error) {
	cred := &kcmv1.Credential{}
	credRef := client.ObjectKey{
		Name:      credName,
		Namespace: credNamespace,
	}
	if err := v.Get(ctx, credRef, cred); err != nil {
		return nil, err
	}
	return cred, nil
}

func isTemplateValid(status *kcmv1.TemplateStatusCommon) error {
	if !status.Valid {
		return fmt.Errorf("the template is not valid: %s", status.ValidationError)
	}

	return nil
}

func (v *ClusterDeploymentValidator) validateCredential(ctx context.Context, clusterDeployment *kcmv1.ClusterDeployment, template *kcmv1.ClusterTemplate) error {
	if len(template.Status.Providers) == 0 {
		return fmt.Errorf("template %q has no providers defined", template.Name)
	}

	hasInfra := false
	for _, v := range template.Status.Providers {
		if strings.HasPrefix(v, "infrastructure-") {
			hasInfra = true
			break
		}
	}

	if !hasInfra {
		return fmt.Errorf("template %q has no infrastructure providers defined", template.Name)
	}

	cred, err := v.getClusterDeploymentCredential(ctx, clusterDeployment.Namespace, clusterDeployment.Spec.Credential)
	if err != nil {
		return err
	}

	if !cred.Status.Ready {
		return errors.New("credential is not Ready")
	}

	return isCredMatchTemplate(cred, template)
}

func isCredMatchTemplate(cred *kcmv1.Credential, template *kcmv1.ClusterTemplate) error {
	idtyKind := cred.Spec.IdentityRef.Kind

	errMsg := func(provider string) error {
		return fmt.Errorf("wrong kind of the ClusterIdentity %q for provider %q", idtyKind, provider)
	}

	const secretKind = "Secret"

	for _, provider := range template.Status.Providers {
		if provider == providersloader.InfraPrefix+"internal" {
			if idtyKind != secretKind {
				return errMsg(provider)
			}

			continue
		}

		idtys, found := providersloader.GetClusterIdentityKinds(provider)
		if !found {
			if strings.HasPrefix(provider, providersloader.InfraPrefix) {
				return fmt.Errorf("unsupported infrastructure provider %s", provider)
			}

			continue
		}

		if !slices.Contains(idtys, idtyKind) {
			return errMsg(provider)
		}
	}

	return nil
}

func ValidateCrossNamespaceRefs(ctx context.Context, namespace string, serviceSpec *kcmv1.ServiceSpec) (errs error) {
	l := ctrl.LoggerFrom(ctx)

	l.Info(fmt.Sprintf("Validating that the references in .spec.serviceSpec.TemplateRefs do not refer to any resource outside the %s namespace", namespace))
	for _, ref := range serviceSpec.TemplateResourceRefs {
		// Sveltos will use same namespace as cluster if namespace is empty:
		// https://projectsveltos.github.io/sveltos/template/intro_template/#templateresourcerefs-namespace-and-name
		if ref.Resource.Namespace != "" && ref.Resource.Namespace != namespace {
			errs = errors.Join(errs, fmt.Errorf("%s %q is in namespace %s, cannot refer to a resource in a namespace other than %s in .spec.serviceSpec.templateResourceRefs", ref.Resource.Kind, ref.Resource.Name, ref.Resource.Namespace, namespace))
		}
	}

	if errs != nil {
		return errs
	}

	l.Info(fmt.Sprintf("Validating that the references in .spec.serviceSpec.services[].ValueFrom do not refer to any resource outside the %s namespace", namespace))
	for _, svc := range serviceSpec.Services {
		for _, v := range svc.ValuesFrom {
			// Sveltos will use same namespace as cluster if namespace is empty.
			if v.Namespace != "" && v.Namespace != namespace {
				errs = errors.Join(errs, fmt.Errorf("%s %q is in namespace %s, cannot refer to a resource in a namespace other than %s in .spec.serviceSpec.services[].valuesFrom", v.Kind, v.Name, v.Namespace, namespace))
			}
		}
	}

	return errs
}
