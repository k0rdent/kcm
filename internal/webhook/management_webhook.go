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

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/registry"
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
)

type ManagementValidator struct {
	client.Client
	SystemNamespace string
}

var errManagementDeletionForbidden = errors.New("management deletion is forbidden")

func (v *ManagementValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&kcmv1.Management{}).
		WithValidator(v).
		Complete()
}

var _ webhook.CustomValidator = &ManagementValidator{}

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
	
	// Validate registry credentials if provided
	if err := v.validateRegistryCredentials(ctx, mgmt); err != nil {
		return nil,
			apierrors.NewInvalid(mgmt.GroupVersionKind().GroupKind(), mgmt.Name, field.ErrorList{
				field.Forbidden(field.NewPath("spec", "registryConfig", "credentials"), err.Error()),
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
	if !newMgmt.DeletionTimestamp.IsZero() {
		return nil, nil
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
	
	// Validate registry credentials if they changed
	oldRegCfg := oldMgmt.Spec.RegistryConfig
	newRegCfg := newMgmt.Spec.RegistryConfig
	registryCredsChanged := (oldRegCfg == nil && newRegCfg != nil) ||
		(oldRegCfg != nil && newRegCfg == nil) ||
		(oldRegCfg != nil && newRegCfg != nil && oldRegCfg.Credentials != newRegCfg.Credentials)
	
	if registryCredsChanged {
		if err := v.validateRegistryCredentials(ctx, newMgmt); err != nil {
			return nil,
				apierrors.NewInvalid(newMgmt.GroupVersionKind().GroupKind(), newMgmt.Name, field.ErrorList{
					field.Forbidden(field.NewPath("spec", "registryConfig", "credentials"), err.Error()),
				})
		}
	}

	release := &kcmv1.Release{}
	if err := v.Get(ctx, client.ObjectKey{Name: newMgmt.Spec.Release}, release); err != nil {
		return nil, fmt.Errorf("failed to get Release %s: %w", newMgmt.Spec.Release, err)
	}

	if err := checkComponentsRemoval(ctx, v.Client, release, oldMgmt, newMgmt); err != nil {
		return admission.Warnings{"Some of the providers cannot be removed"},
			apierrors.NewInvalid(newMgmt.GroupVersionKind().GroupKind(), newMgmt.Name, field.ErrorList{
				field.Forbidden(field.NewPath("spec", "providers"), err.Error()),
			})
	}

	incompatibleContracts, err := validationutil.GetIncompatibleContracts(ctx, v, release, newMgmt)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", invalidMgmtMsg, err)
	}

	if incompatibleContracts != "" {
		return admission.Warnings{"The Management object has incompatible CAPI contract versions in ProviderTemplates"}, fmt.Errorf("%s: %s", invalidMgmtMsg, incompatibleContracts)
	}

	return nil, nil
}

func checkComponentsRemoval(ctx context.Context, cl client.Client, release *kcmv1.Release, oldObj, newObj validationutil.ComponentsManager) error {
	removedComponents := []kcmv1.Provider{}
	components := oldObj.Components()
	for _, oldComp := range components.Providers {
		if !slices.ContainsFunc(newObj.Components().Providers, func(newComp kcmv1.Provider) bool { return oldComp.Name == newComp.Name }) {
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
			// the template has already been removed, so no reason to prevent deletion from the list of providers
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("failed to get ProviderTemplate %s: %w", tplRef, err)
		}

		providers, err := validationutil.ProvidersInUseFor(ctx, cl, prTpl, newObj)
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

	parentKind := newObj.GetObjectKind().GroupVersionKind().Kind
	inUseProviderNames := slices.Collect(maps.Keys(inUseProviders))
	switch len(inUseProviderNames) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("provider %s is required by at least one ClusterDeployment and cannot be removed from the %s %s", inUseProviderNames[0], parentKind, newObj.GetName())
	default:
		return fmt.Errorf("providers %s are required by at least one ClusterDeployment and cannot be removed from the %s %s", strings.Join(inUseProviderNames, ","), parentKind, newObj.GetName())
	}
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *ManagementValidator) ValidateDelete(ctx context.Context, _ runtime.Object) (admission.Warnings, error) {
	err := validationutil.ManagementDeletionAllowed(ctx, v.Client)
	if err != nil {
		warning := strings.ToUpper(err.Error()[:1]) + err.Error()[1:]
		return admission.Warnings{warning}, errManagementDeletionForbidden
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

// validateRegistryCredentials validates registry credentials if configured.
func (v *ManagementValidator) validateRegistryCredentials(ctx context.Context, mgmt *kcmv1.Management) error {
	// If no registry config, nothing to validate
	if mgmt.Spec.RegistryConfig == nil || mgmt.Spec.RegistryConfig.Credentials == nil {
		return nil
	}

	registryConfig := mgmt.Spec.RegistryConfig
	credentials := registryConfig.Credentials

	// Basic validation: ensure either inline credentials or secret ref is provided
	hasInlineCredentials := credentials.Username != "" || credentials.Password != ""
	hasSecretRef := credentials.SecretRef != nil

	if !hasInlineCredentials && !hasSecretRef {
		return fmt.Errorf("registry credentials must provide either inline credentials (username/password) or a secretRef")
	}

	if hasInlineCredentials && hasSecretRef {
		return fmt.Errorf("registry credentials cannot have both inline credentials and secretRef; choose one")
	}

	// If using secret ref, verify the secret exists
	if hasSecretRef {
		namespace := v.SystemNamespace
		if namespace == "" {
			namespace = "kcm-system" // Default fallback
		}

		secretManager := registry.NewSecretManager(v.Client)
		exists, err := secretManager.SecretExists(ctx, credentials.SecretRef.Name, namespace)
		if err != nil {
			return fmt.Errorf("failed to check if credentials secret exists: %w", err)
		}
		if !exists {
			return fmt.Errorf("credentials secret %s/%s does not exist", namespace, credentials.SecretRef.Name)
		}
	}

	// Validate credentials by attempting registry authentication
	registryURL := registryConfig.Registry
	if registryURL == "" {
		// Registry might be set at global level, skip validation here
		return nil
	}

	// Get credentials
	var username, password string
	var err error

	secretManager := registry.NewSecretManager(v.Client)
	if hasSecretRef {
		namespace := v.SystemNamespace
		if namespace == "" {
			namespace = "kcm-system"
		}
		username, password, err = secretManager.GetCredentialsFromSpec(ctx, credentials, namespace)
		if err != nil {
			return fmt.Errorf("failed to get credentials from secret: %w", err)
		}
	} else {
		username = credentials.Username
		password = credentials.Password
	}

	// Validate credentials against the registry
	validator := registry.NewValidator()
	result := validator.ValidateCredentials(ctx, registryURL, username, password, registryConfig.InsecureRegistry)
	if !result.Valid {
		return fmt.Errorf("registry credential validation failed: %s", result.Message)
	}

	return nil
}
