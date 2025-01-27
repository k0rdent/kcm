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
	"maps"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1alpha1"
)

type ManagementValidator struct {
	client.Client
}

var errManagementDeletionForbidden = errors.New("management deletion is forbidden")

func (v *ManagementValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&kcmv1.Management{}).
		WithValidator(v).
		WithDefaulter(v).
		Complete()
}

var (
	_ webhook.CustomValidator = &ManagementValidator{}
	_ webhook.CustomDefaulter = &ManagementValidator{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (v *ManagementValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	mgmt, ok := obj.(*kcmv1.Management)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected Management but got a %T", obj))
	}
	if err := validateRelease(ctx, v.Client, mgmt.Spec.Release); err != nil {
		return nil,
			apierrors.NewInvalid(mgmt.GroupVersionKind().GroupKind(), mgmt.Name, field.ErrorList{
				field.Forbidden(field.NewPath("spec", "release"), err.Error()),
			})
	}
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (v *ManagementValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	const invalidMgmtMsg = "the Management is invalid"

	newMgmt, ok := newObj.(*kcmv1.Management)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected Management but got a %T", newObj))
	}

	oldMgmt, ok := oldObj.(*kcmv1.Management)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected Management but got a %T", oldObj))
	}

	if oldMgmt.Spec.Release != newMgmt.Spec.Release {
		if err := validateRelease(ctx, v.Client, newMgmt.Spec.Release); err != nil {
			return nil,
				apierrors.NewInvalid(newMgmt.GroupVersionKind().GroupKind(), newMgmt.Name, field.ErrorList{
					field.Forbidden(field.NewPath("spec", "release"), err.Error()),
				})
		}
	}

	release := &kcmv1.Release{}
	if err := v.Client.Get(ctx, client.ObjectKey{Name: newMgmt.Spec.Release}, release); err != nil {
		return nil, fmt.Errorf("failed to get Release %s: %w", newMgmt.Spec.Release, err)
	}

	if err := checkComponentsRemoval(ctx, v.Client, release, oldMgmt, newMgmt); err != nil {
		return admission.Warnings{"Some of the providers cannot be removed"},
			apierrors.NewInvalid(newMgmt.GroupVersionKind().GroupKind(), newMgmt.Name, field.ErrorList{
				field.Forbidden(field.NewPath("spec", "providers"), err.Error()),
			})
	}

	incompatibleContracts, err := getIncompatibleContracts(ctx, v, release, newMgmt)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", invalidMgmtMsg, err)
	}

	if incompatibleContracts != "" {
		return admission.Warnings{"The Management object has incompatible CAPI contract versions in ProviderTemplates"}, fmt.Errorf("%s: %s", invalidMgmtMsg, incompatibleContracts)
	}

	return nil, nil
}

func checkComponentsRemoval(ctx context.Context, cl client.Client, release *kcmv1.Release, oldMgmt, newMgmt *kcmv1.Management) error {
	removedComponents := []kcmv1.Provider{}
	for _, oldComp := range oldMgmt.Spec.Providers {
		if !slices.ContainsFunc(newMgmt.Spec.Providers, func(newComp kcmv1.Provider) bool { return oldComp.Name == newComp.Name }) {
			removedComponents = append(removedComponents, oldComp)
		}
	}

	if len(removedComponents) == 0 {
		return nil
	}

	inUseProviders := make(map[string]struct{})
	for _, m := range removedComponents {
		tplRef := m.Template
		if tplRef == "" {
			tplRef = release.ProviderTemplate(m.Name)
		}

		if tplRef == "" {
			continue
		}

		prTpl := new(kcmv1.ProviderTemplate)
		if err := cl.Get(ctx, client.ObjectKey{Name: tplRef}, prTpl); err != nil {
			return fmt.Errorf("failed to get ProviderTemplate %s: %w", tplRef, err)
		}

		providers, err := getInUseProvidersWithContracts(ctx, cl, prTpl)
		if err != nil {
			return fmt.Errorf("failed to get in-use providers for the template %s: %w", prTpl.Name, err)
		}
		if len(providers) == 0 {
			continue
		}

		for provider := range providers {
			inUseProviders[provider] = struct{}{}
		}
	}

	inUseProviderNames := slices.Collect(maps.Keys(inUseProviders))
	switch len(inUseProviderNames) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("provider %s is required by at least one ClusterDeployment and cannot be removed from the Management %s", inUseProviderNames[0], newMgmt.Name)
	default:
		return fmt.Errorf("providers %s are required by at least one ClusterDeployment and cannot be removed from the Management %s", strings.Join(inUseProviderNames, ","), newMgmt.Name)
	}
}

func getIncompatibleContracts(ctx context.Context, cl client.Client, release *kcmv1.Release, mgmt *kcmv1.Management) (string, error) {
	capiTplName := release.Spec.CAPI.Template
	if mgmt.Spec.Core != nil && mgmt.Spec.Core.CAPI.Template != "" {
		capiTplName = mgmt.Spec.Core.CAPI.Template
	}

	capiTpl := new(kcmv1.ProviderTemplate)
	if err := cl.Get(ctx, client.ObjectKey{Name: capiTplName}, capiTpl); err != nil {
		return "", fmt.Errorf("failed to get ProviderTemplate %s: %w", capiTplName, err)
	}

	if len(capiTpl.Status.CAPIContracts) > 0 && !capiTpl.Status.Valid {
		return "", fmt.Errorf("not valid ProviderTemplate %s", capiTpl.Name)
	}

	incompatibleContracts := strings.Builder{}
	for _, p := range mgmt.Spec.Providers {
		tplName := p.Template
		if tplName == "" {
			tplName = release.ProviderTemplate(p.Name)
		}

		if tplName == capiTpl.Name || tplName == "" {
			continue
		}

		pTpl := new(kcmv1.ProviderTemplate)
		if err := cl.Get(ctx, client.ObjectKey{Name: tplName}, pTpl); err != nil {
			return "", fmt.Errorf("failed to get ProviderTemplate %s: %w", tplName, err)
		}

		if len(pTpl.Status.CAPIContracts) == 0 {
			continue
		}

		if !pTpl.Status.Valid {
			return "", fmt.Errorf("not valid ProviderTemplate %s", tplName)
		}

		inUseProviders, err := getInUseProvidersWithContracts(ctx, cl, pTpl)
		if err != nil {
			return "", fmt.Errorf("failed to get in-use providers for the template %s: %w", pTpl.Name, err)
		}

		exposedContracts := make(map[string]struct{})
		for capiVersion, providerContracts := range pTpl.Status.CAPIContracts {
			for _, contract := range strings.Split(providerContracts, "_") {
				exposedContracts[contract] = struct{}{}
			}
			if len(capiTpl.Status.CAPIContracts) > 0 {
				if _, ok := capiTpl.Status.CAPIContracts[capiVersion]; !ok {
					_, _ = incompatibleContracts.WriteString(fmt.Sprintf("core CAPI contract versions does not support %s version in the ProviderTemplate %s, ", capiVersion, pTpl.Name))
				}
			}
		}

		if len(inUseProviders) == 0 {
			continue
		}
		for provider, contracts := range inUseProviders {
			for _, contract := range contracts {
				if _, ok := exposedContracts[contract]; !ok {
					_, _ = incompatibleContracts.WriteString(fmt.Sprintf("missing contract version %s for %s provider that is required by one or more ClusterDeployment, ", contract, provider))
				}
			}
		}
	}

	return strings.TrimSuffix(incompatibleContracts.String(), ", "), nil
}

func getInUseProvidersWithContracts(ctx context.Context, cl client.Client, pTpl *kcmv1.ProviderTemplate) (map[string][]string, error) {
	inUseProviders := make(map[string][]string)
	for _, providerName := range pTpl.Status.Providers {
		clusterTemplates := new(kcmv1.ClusterTemplateList)
		if err := cl.List(ctx, clusterTemplates, client.MatchingFields{kcmv1.ClusterTemplateProvidersIndexKey: providerName}); err != nil {
			return nil, fmt.Errorf("failed to list ClusterTemplates: %w", err)
		}

		if len(clusterTemplates.Items) == 0 {
			continue
		}

		for _, cltpl := range clusterTemplates.Items {
			mcls := new(kcmv1.ClusterDeploymentList)
			if err := cl.List(ctx, mcls,
				client.MatchingFields{kcmv1.ClusterDeploymentTemplateIndexKey: cltpl.Name},
				client.Limit(1)); err != nil {
				return nil, fmt.Errorf("failed to list ClusterDeployments: %w", err)
			}

			if len(mcls.Items) == 0 {
				continue
			}
			inUseProviders[providerName] = append(inUseProviders[providerName], cltpl.Status.ProviderContracts[providerName])
		}
	}
	return inUseProviders, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *ManagementValidator) ValidateDelete(ctx context.Context, _ runtime.Object) (admission.Warnings, error) {
	clusterDeployments := &kcmv1.ClusterDeploymentList{}
	err := v.Client.List(ctx, clusterDeployments, client.Limit(1))
	if err != nil {
		return nil, err
	}
	if len(clusterDeployments.Items) > 0 {
		return admission.Warnings{"The Management object can't be removed if ClusterDeployment objects still exist"}, errManagementDeletionForbidden
	}
	return nil, nil
}

func validateRelease(ctx context.Context, cl client.Client, releaseName string) error {
	release := &kcmv1.Release{}
	if err := cl.Get(ctx, client.ObjectKey{Name: releaseName}, release); err != nil {
		return err
	}
	if !release.Status.Ready {
		return fmt.Errorf("release \"%s\" status is not ready", releaseName)
	}
	return nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (*ManagementValidator) Default(context.Context, runtime.Object) error {
	return nil
}
