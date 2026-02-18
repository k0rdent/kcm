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
	"encoding/json"
	"errors"
	"fmt"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8svalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/serviceset"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

var (
	errServicesHaveValidTemplates = errors.New("some services have invalid templates")
	errServicesDependency         = errors.New("some services have invalid dependencies")
)

func resolveCRDName(
	disco discovery.DiscoveryInterface,
	gvk kcmv1.GroupVersionKind,
) (string, error) {
	gv := schema.GroupVersion{
		Group:   gvk.Group,
		Version: gvk.Version,
	}.String()

	resources, err := disco.ServerResourcesForGroupVersion(gv)
	if err != nil {
		return "", fmt.Errorf("failed to discover resources for %s: %w", gv, err)
	}

	for _, r := range resources.APIResources {
		if r.Kind == gvk.Kind {
			crdName := fmt.Sprintf("%s.%s", r.Name, gvk.Group)
			return crdName, nil
		}
	}

	return "", fmt.Errorf("CRD not found for GVK %s/%s, Kind=%s", gvk.Group, gvk.Version, gvk.Kind)
}

func getProvider(
	ctx context.Context,
	cl client.Client,
	serviceSpec kcmv1.ServiceSpec,
	namespace string,
) (*kcmv1.StateManagementProvider, error) {
	providerName := kubeutil.DefaultStateManagementProvider
	if serviceSpec.Provider.Name != "" {
		providerName = serviceSpec.Provider.Name
	}

	provider := &kcmv1.StateManagementProvider{}
	if err := cl.Get(
		ctx,
		client.ObjectKey{Namespace: namespace, Name: providerName},
		provider,
	); err != nil {
		return nil, fmt.Errorf("failed to retrieve provider %q: %w", providerName, err)
	}

	return provider, nil
}

func resolveProviderSchema(
	ctx context.Context,
	cl client.Client,
	disco discovery.DiscoveryInterface,
	provider *kcmv1.StateManagementProvider,
) (*apiextensions.JSONSchemaProps, error) {
	ref := *provider.Spec.ConfigSchemaRef

	crdName, err := resolveCRDName(disco, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve CRD name: %w", err)
	}

	crd := &apiextv1.CustomResourceDefinition{}
	if err := cl.Get(ctx, client.ObjectKey{Name: crdName}, crd); err != nil {
		return nil, fmt.Errorf("failed to retrieve CRD %q: %w", crdName, err)
	}

	var externalSchema *apiextv1.JSONSchemaProps
	for _, v := range crd.Spec.Versions {
		if v.Name == ref.Version {
			if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
				return nil, fmt.Errorf("CRD %q version %q has no schema", crdName, ref.Version)
			}
			externalSchema = v.Schema.OpenAPIV3Schema
			break
		}
	}

	if externalSchema == nil {
		return nil, fmt.Errorf("CRD %q does not define version %q", crdName, ref.Version)
	}

	internalSchema := &apiextensions.JSONSchemaProps{}
	if err := apiextv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(
		externalSchema,
		internalSchema,
		nil,
	); err != nil {
		return nil, fmt.Errorf("failed to convert OpenAPI schema: %w", err)
	}

	return internalSchema, nil
}

// ServicesHaveValidProviderConfiguration validates that the provider config is valid against the specified CRD if provided.
func ServicesHaveValidProviderConfiguration(
	ctx context.Context,
	cl client.Client,
	disco discovery.DiscoveryInterface,
	serviceSpec kcmv1.ServiceSpec,
	namespace string,
) error {
	if serviceSpec.Provider.Config == nil {
		return nil
	}

	provider, err := getProvider(ctx, cl, serviceSpec, namespace)
	if err != nil {
		return err
	}

	if provider.Spec.ConfigSchemaRef == nil {
		return nil
	}

	providerSchema, err := resolveProviderSchema(ctx, cl, disco, provider)
	if err != nil {
		return err
	}

	return validateConfigAgainstSchema(
		serviceSpec.Provider.Config.Raw,
		providerSchema,
		provider.Name,
	)
}

func validateConfigAgainstSchema(
	raw json.RawMessage,
	jsonSchema *apiextensions.JSONSchemaProps,
	providerName string,
) error {
	if len(raw) == 0 {
		return fmt.Errorf("provider %q config is empty", providerName)
	}

	validator, _, err := k8svalidation.NewSchemaValidator(jsonSchema)
	if err != nil {
		return fmt.Errorf("failed to create schema validator: %w", err)
	}

	var obj any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("failed to unmarshal provider config: %w", err)
	}

	result := validator.Validate(obj)
	if result.IsValid() {
		return nil
	}

	return fmt.Errorf(
		"provider %q config validation failed: %w",
		providerName,
		errors.Join(result.Errors...),
	)
}

// ServicesHaveValidTemplates validates the given array of [github.com/K0rdent/kcm/api/v1beta1.Service] checking
// if referenced [github.com/K0rdent/kcm/api/v1beta1.ServiceTemplate] is valid and is ready to be consumed.
func ServicesHaveValidTemplates(ctx context.Context, cl client.Client, services []kcmv1.Service, ns string) error {
	var errs error
	for _, svc := range services {
		if svc.Namespace != "" {
			for _, msg := range validation.ValidateNamespaceName(svc.Namespace, false) {
				errs = errors.Join(errs, field.Invalid(field.NewPath("namespace"), svc.Namespace, msg))
			}
		}

		if svc.Name != "" {
			for _, msg := range validation.ValidateNamespaceName(svc.Name, false) {
				errs = errors.Join(errs, field.Invalid(field.NewPath("name"), svc.Name, msg))
			}
		}

		errs = errors.Join(errs, validateServiceTemplate(ctx, cl, svc, ns))
		if svc.TemplateChain == "" {
			continue
		}
		errs = errors.Join(errs, validateServiceTemplateChain(ctx, cl, svc, ns))
	}

	if errs != nil {
		return errors.Join(errServicesHaveValidTemplates, errs)
	}
	return nil
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

// ValidateServiceDependencyOverall calls all of the functions
// related to service dependency validation one by one.
func ValidateServiceDependencyOverall(services []kcmv1.Service) error {
	if err := validateServiceDependency(services); err != nil {
		return errors.Join(errServicesDependency, fmt.Errorf("failed service dependency validation: %w", err))
	}

	if err := validateServiceDependencyCycle(services); err != nil {
		return errors.Join(errServicesDependency, fmt.Errorf("failed service dependency cycle validation: %w", err))
	}

	return nil
}

// validateServiceDependency validates if all dependencies of services have been defined as well.
func validateServiceDependency(services []kcmv1.Service) error {
	if len(services) == 0 {
		return nil
	}

	dependsOnMap := make(map[client.ObjectKey][]client.ObjectKey)
	for _, svc := range services {
		k := serviceset.ServiceKey(svc.Namespace, svc.Name)
		dependsOnMap[k] = make([]client.ObjectKey, len(svc.DependsOn))

		for i := range svc.DependsOn {
			dependsOnMap[k][i] = serviceset.ServiceKey(svc.DependsOn[i].Namespace, svc.DependsOn[i].Name)
		}
	}

	var err error
	for svc, dependencies := range dependsOnMap {
		for _, d := range dependencies {
			if _, ok := dependsOnMap[d]; !ok {
				err = errors.Join(err, fmt.Errorf("dependency %s of service %s is not defined as a service", d, svc))
			}
		}
	}

	return err
}

// validateServiceDependencyCycle validates if there is a cycle in the services dependency graph.
func validateServiceDependencyCycle(services []kcmv1.Service) error {
	if len(services) == 0 {
		return nil
	}

	dependsOnMap := make(map[client.ObjectKey][]client.ObjectKey)
	for _, svc := range services {
		k := serviceset.ServiceKey(svc.Namespace, svc.Name)
		for _, d := range svc.DependsOn {
			dependsOnMap[k] = append(dependsOnMap[k], serviceset.ServiceKey(d.Namespace, d.Name))
		}
	}

	for key := range dependsOnMap {
		if err := hasDependencyCycle(key, nil, dependsOnMap); err != nil {
			return err
		}
	}

	return nil
}

// hasDependencyCycle uses DFS to check for cycles in the
// dependency graph and returns on the first occurrence of a cycle.
func hasDependencyCycle(key client.ObjectKey, visited map[client.ObjectKey]bool, dependsOnMap map[client.ObjectKey][]client.ObjectKey) error {
	if visited == nil {
		visited = make(map[client.ObjectKey]bool)
	}

	// Add current to visited.
	visited[key] = true

	dependsOn, ok := dependsOnMap[key]
	if !ok {
		return nil
	}

	for _, d := range dependsOn {
		if _, ok := visited[d]; ok {
			// No need to check other dependants because cycle was detected.
			return fmt.Errorf("dependency cycle detected from %s to %s", key, d)
		}

		if err := hasDependencyCycle(d, visited, dependsOnMap); err != nil {
			return err
		}
	}

	// Remove current from visited.
	visited[key] = false
	return nil
}
