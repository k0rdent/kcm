// Copyright 2025
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

package validation

import (
	"context"
	"errors"
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// ServicesHaveValidTemplates validates the given array of [github.com/K0rdent/kcm/api/v1beta1.Service] checking
// if referenced [github.com/K0rdent/kcm/api/v1beta1.ServiceTemplate] is valid and is ready to be consumed.
func ServicesHaveValidTemplates(ctx context.Context, cl client.Client, services []kcmv1.Service, ns string) error {
	var errs error
	for _, svc := range services {
		errs = errors.Join(errs, validateServiceTemplate(ctx, cl, svc, ns))
		if svc.TemplateChain == "" {
			continue
		}
		errs = errors.Join(errs, validateServiceTemplateChain(ctx, cl, svc, ns))
	}

	return errs
}

// validateServiceTemplate validates the given [github.com/K0rdent/kcm/api/v1beta1.ServiceTemplate] checking if it is valid
func validateServiceTemplate(ctx context.Context, cl client.Client, svc kcmv1.Service, ns string) error {
	svcTemplate := new(kcmv1.ServiceTemplate)
	key := client.ObjectKey{Namespace: ns, Name: svc.Template}
	if err := cl.Get(ctx, key, svcTemplate); err != nil {
		return fmt.Errorf("failed to get ServiceTemplate %s: %w", key, err)
	}

	if !svcTemplate.Status.Valid {
		return fmt.Errorf("the ServiceTemplate %s is invalid with the error: %s", key, svcTemplate.Status.ValidationError)
	}

	return nil
}

// validateServiceTemplateChain validates the given [github.com/K0rdent/kcm/api/v1beta1.ServiceTemplateChain] checking if
// it contains valid [github.com/K0rdent/kcm/api/v1beta1.ServiceTemplate] with matching version.
func validateServiceTemplateChain(ctx context.Context, cl client.Client, svc kcmv1.Service, ns string) error {
	templateChain := new(kcmv1.ServiceTemplateChain)
	key := client.ObjectKey{Namespace: ns, Name: svc.TemplateChain}
	if err := cl.Get(ctx, key, templateChain); err != nil {
		return fmt.Errorf("failed to get ServiceTemplateChain %s: %w", key, err)
	}

	if !templateChain.Status.Valid {
		return fmt.Errorf("the ServiceTemplateChain %s is invalid with the error: %s", key, templateChain.Status.ValidationError)
	}

	var errs error
	matchingTemplateFound := false
	for _, t := range templateChain.Spec.SupportedTemplates {
		if t.Name != svc.Template {
			continue
		}
		template := new(kcmv1.ServiceTemplate)
		key = client.ObjectKey{Namespace: ns, Name: t.Name}
		if err := cl.Get(ctx, key, template); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to get ServiceTemplate %s: %w", key, err))
			continue
		}
		// this error should never happen, but we check it anyway
		if !template.Status.Valid {
			errs = errors.Join(errs, fmt.Errorf("the ServiceTemplate %s is invalid with the error: %s", key, template.Status.ValidationError))
			continue
		}
		matchingTemplateFound = true
		break
	}
	if !matchingTemplateFound {
		errs = errors.Join(errs, fmt.Errorf("the ServiceTemplateChain %s does not support ServiceTemplate %s", key, svc.Template))
	}

	return errs
}

func ValidateUpgradePaths(services []kcmv1.Service, upgradePaths []kcmv1.ServiceUpgradePaths) error {
	observedTemplatesMap := make(map[string]struct {
		Template     string
		UpgradePaths []kcmv1.UpgradePath
	}, len(upgradePaths))
	for _, observedService := range upgradePaths {
		observedServiceNamespacedName := client.ObjectKey{Name: observedService.Name, Namespace: observedService.Namespace}
		observedTemplatesMap[observedServiceNamespacedName.String()] = struct {
			Template     string
			UpgradePaths []kcmv1.UpgradePath
		}{Template: observedService.Template, UpgradePaths: observedService.AvailableUpgrades}
	}
	var errs error
	for _, svc := range services {
		serviceNamespacedName := client.ObjectKey{Name: svc.Name, Namespace: svc.Namespace}
		if svc.Namespace == "" {
			serviceNamespacedName.Namespace = metav1.NamespaceDefault
		}
		currentTemplate, ok := observedTemplatesMap[serviceNamespacedName.String()]
		// if the desired service namespaced name does not exist in observed installed
		// services, then it means it is a new service, therefore nothing to validate.
		if !ok {
			continue
		}
		// if the desired service template matches observed template of the service with
		// given namespaced name, then it means there are no changes to the service,
		// therefore nothing to validate.
		if svc.Template == currentTemplate.Template {
			continue
		}
		// if desired service template is not in observed upgrade paths, then return false
		// otherwise continue validation
		canUpgrade := false
		for _, upgradePath := range currentTemplate.UpgradePaths {
			if slices.Contains(upgradePath.Versions, svc.Template) {
				canUpgrade = true
				break
			}
		}
		if !canUpgrade {
			errs = errors.Join(errs, fmt.Errorf("service %s can't be upgraded from %s to %s", serviceNamespacedName, currentTemplate.Template, svc.Template))
		}
	}
	return errs
}
