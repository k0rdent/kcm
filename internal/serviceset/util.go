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

package serviceset

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

// ObjectKey generates a unique key for a ServiceSet given the input and returns it.
func ObjectKey(systemNamespace string, cd *kcmv1.ClusterDeployment, mcs *kcmv1.MultiClusterService) client.ObjectKey {
	// We'll use the following pattern to build ServiceSet name:
	// <ClusterDeploymentName>-<MultiClusterServiceNameHash>
	// this will guarantee that the ServiceSet produced by MultiClusterService
	// has name unique for each ClusterDeployment. If the clusterDeployment is nil,
	// then serviceSet with "management" prefix will be created and system namespace.
	var serviceSetNamespace, serviceSetName string

	mcsNameHash := sha256.Sum256([]byte(mcs.Name))
	if cd == nil {
		serviceSetName = fmt.Sprintf("management-%x", mcsNameHash[:4])
		serviceSetNamespace = systemNamespace
	} else {
		serviceSetName = fmt.Sprintf("%s-%x", cd.Name, mcsNameHash[:4])
		serviceSetNamespace = cd.Namespace
	}

	return client.ObjectKey{
		Namespace: serviceSetNamespace,
		Name:      serviceSetName,
	}
}

func ResolveServiceVersions(ctx context.Context, c client.Client, namespace string, services any) error {
	switch s := services.(type) {
	case []kcmv1.Service:
		ptrs := make([]*kcmv1.Service, len(s))
		for i := range s {
			ptrs[i] = &s[i]
		}
		return fillServiceVersions(ctx, c, namespace, ptrs)

	case []kcmv1.ServiceWithValues:
		ptrs := make([]*kcmv1.ServiceWithValues, len(s))
		for i := range s {
			ptrs[i] = &s[i]
		}
		return fillServiceWithValueVersions(ctx, c, namespace, ptrs)

	default:
		return fmt.Errorf("unsupported slice type %T", services)
	}
}

func fillServiceVersions(ctx context.Context, c client.Client, namespace string, services []*kcmv1.Service) error {
	for _, svc := range services {
		if svc.Version == "" && svc.Template != "" {
			template := kcmv1.ServiceTemplate{}
			if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: svc.Template}, &template); err != nil {
				return fmt.Errorf("failed to fetch template %s: %w", svc.Template, err)
			}

			version := template.Spec.Version
			if version == "" && template.Spec.Helm != nil && template.Spec.Helm.ChartSpec != nil {
				version = template.Spec.Helm.ChartSpec.Version
			}
			svc.Version = version

			if svc.Version == "" {
				svc.Version = svc.Template
			}
		}
	}
	return nil
}

func fillServiceWithValueVersions(ctx context.Context, c client.Client, namespace string, services []*kcmv1.ServiceWithValues) error {
	for _, svc := range services {
		if svc.Values == "" && svc.Template != "" {
			template := kcmv1.ServiceTemplate{}
			if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: svc.Template}, &template); err != nil {
				return fmt.Errorf("failed to fetch Template %s: %w", svc.Template, err)
			}

			version := template.Spec.Version
			if version == "" && template.Spec.Helm != nil && template.Spec.Helm.ChartSpec != nil {
				version = template.Spec.Helm.ChartSpec.Version
			}
			svc.Version = &version
			if svc.Version == nil {
				svc.Version = &svc.Template
			}
		}
	}
	return nil
}

// ServicesWithDesiredChains takes out the templateChain from desiredServices for each service
// and plugs it into matching service in deployedServices and returns the new list of services.
func ServicesWithDesiredChains(
	desiredServices []kcmv1.Service,
	deployedServices []kcmv1.ServiceWithValues,
) []kcmv1.Service {
	res := make([]kcmv1.Service, 0, len(deployedServices))
	chainMap := make(map[client.ObjectKey]string)

	for _, svc := range desiredServices {
		chainMap[client.ObjectKey{
			Namespace: effectiveNamespace(svc.Namespace),
			Name:      svc.Name,
		}] = svc.TemplateChain
	}

	for _, svc := range deployedServices {
		chain := chainMap[client.ObjectKey{
			Namespace: effectiveNamespace(svc.Namespace),
			Name:      svc.Name,
		}]
		res = append(res, kcmv1.Service{
			Name:          svc.Name,
			Namespace:     effectiveNamespace(svc.Namespace),
			Template:      svc.Template,
			TemplateChain: chain,
		})
	}
	return res
}

func ServicesUpgradePaths(
	ctx context.Context,
	c client.Client,
	services []kcmv1.Service,
	namespace string,
) ([]kcmv1.ServiceUpgradePaths, error) {
	l := ctrl.LoggerFrom(ctx)
	l.V(1).Info("Reconciling services upgrade paths")

	var errs error
	servicesUpgradePaths := make([]kcmv1.ServiceUpgradePaths, 0, len(services))
	for _, svc := range services {
		serviceNamespace := effectiveNamespace(svc.Namespace)

		serviceUpgradePaths := kcmv1.ServiceUpgradePaths{
			Name:      svc.Name,
			Namespace: serviceNamespace,
			Template:  svc.Template,
		}

		if svc.TemplateChain == "" {
			// Add service as an available upgrade for itself.
			// E.g., if the service needs to be upgraded with new helm values.
			if svc.Version == "" {
				svc.Version = svc.Template
			}
			serviceUpgradePaths.AvailableUpgrades = append(serviceUpgradePaths.AvailableUpgrades, kcmv1.UpgradePath{
				Versions: []kcmv1.AvailableUpgrade{{Name: svc.Template, Version: svc.Version}},
			})
			servicesUpgradePaths = append(servicesUpgradePaths, serviceUpgradePaths)
			continue
		}

		serviceTemplateChain := new(kcmv1.ServiceTemplateChain)
		key := client.ObjectKey{Name: svc.TemplateChain, Namespace: namespace}
		if err := c.Get(ctx, key, serviceTemplateChain); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to get ServiceTemplateChain %s to fetch upgrade paths: %w", key.String(), err))
			continue
		}
		upgradePaths, err := serviceTemplateChain.Spec.UpgradePaths(svc.Template)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to get upgrade paths for ServiceTemplate %s: %w", svc.Template, err))
			continue
		}
		serviceUpgradePaths.AvailableUpgrades = upgradePaths
		servicesUpgradePaths = append(servicesUpgradePaths, serviceUpgradePaths)
	}
	return servicesUpgradePaths, errs
}

// FilterServiceDependencies filters out & returns the services
// from desired services that are NOT dependent on any other service.
// It does so by fetching all ServiceSets associated with provided cd & mcs
// from cd's namespace or from system namespace if cd is nil.
func FilterServiceDependencies(
	ctx context.Context,
	c client.Client,
	systemNamespace string,
	mcs *kcmv1.MultiClusterService,
	cd *kcmv1.ClusterDeployment,
	desiredServices []kcmv1.Service,
) ([]kcmv1.Service, error) {
	mcsName := ""
	if mcs != nil {
		mcsName = mcs.GetName()
	}

	cdName := ""
	namespace := systemNamespace
	if cd != nil {
		cdName = cd.GetName()
		namespace = cd.GetNamespace()
	}

	// Map of services with their indexes.
	serviceIdx := make(map[client.ObjectKey]int)
	// Map of services with the count of other services they depend on.
	dependsOnCount := make(map[client.ObjectKey]int)
	// Map of services with their dependents.
	dependents := make(map[client.ObjectKey][]client.ObjectKey)
	// Map of successfully deployed services across all servicesets of this clusterdeployment.
	deployedServices := make(map[client.ObjectKey]struct{})

	// Populate the maps.
	for i, svc := range desiredServices {
		svcKey := ServiceKey(svc.Namespace, svc.Name)
		serviceIdx[svcKey] = i
		dependsOnCount[svcKey] = len(svc.DependsOn)

		for _, d := range svc.DependsOn {
			dKey := ServiceKey(d.Namespace, d.Name)
			dependents[dKey] = append(dependents[dKey], svcKey)
		}
	}

	serviceSets := new(kcmv1.ServiceSetList)
	sel := fields.Everything()
	if cdName != "" {
		sel = fields.AndSelectors(sel, fields.OneTermEqualSelector(kcmv1.ServiceSetClusterIndexKey, cdName))
	}
	if mcsName != "" {
		sel = fields.AndSelectors(sel, fields.OneTermEqualSelector(kcmv1.ServiceSetMultiClusterServiceIndexKey, mcsName))
	}
	if err := c.List(ctx, serviceSets, client.InNamespace(namespace), client.MatchingFieldsSelector{Selector: sel}); err != nil {
		return nil, fmt.Errorf("failed to list ServiceSets: %w", err)
	}

	for _, sset := range serviceSets.Items {
		for _, svc := range sset.Status.Services {
			if svc.State == kcmv1.ServiceStateDeployed {
				deployedServices[ServiceKey(svc.Namespace, svc.Name)] = struct{}{}
			}
		}
	}

	// For each of the successfully deployed services,
	// decrement the depends on count of its dependents.
	for svc := range deployedServices {
		for _, d := range dependents[ServiceKey(svc.Namespace, svc.Name)] {
			dependsOnCount[ServiceKey(d.Namespace, d.Name)]--
		}
	}

	// Create a new list of services to
	// deploy having depends on count <= 0
	var filtered []kcmv1.Service
	for svc, count := range dependsOnCount {
		if count <= 0 {
			idx := serviceIdx[ServiceKey(svc.Namespace, svc.Name)]
			filtered = append(filtered, desiredServices[idx])
		}
	}

	return filtered, nil
}

func makeService(s kcmv1.Service, version, template string) kcmv1.ServiceWithValues {
	return kcmv1.ServiceWithValues{
		Name:        s.Name,
		Namespace:   s.Namespace,
		Version:     &version,
		Template:    template,
		Values:      s.Values,
		ValuesFrom:  s.ValuesFrom,
		HelmOptions: s.HelmOptions,
		HelmAction:  s.HelmAction,
	}
}

func appendIfNotPresent(
	services []kcmv1.ServiceWithValues,
	s kcmv1.Service,
	minimumUpgrade kcmv1.AvailableUpgrade,
) []kcmv1.ServiceWithValues {
	exists := slices.ContainsFunc(services, func(c kcmv1.ServiceWithValues) bool {
		return c.Name == s.Name &&
			c.Namespace == s.Namespace &&
			c.Version != nil &&
			*c.Version == minimumUpgrade.Version
	})

	if !exists {
		return append(services, makeService(s, minimumUpgrade.Version, minimumUpgrade.Name))
	}
	return services
}

// ServicesToDeploy returns the services to deploy based on the ClusterDeployment spec,
// taking into account already deployed services, and versioning.
func ServicesToDeploy(
	upgradePaths []kcmv1.ServiceUpgradePaths,
	desiredServices []kcmv1.Service,
	serviceSet *kcmv1.ServiceSet,
) []kcmv1.ServiceWithValues {
	// to determine, whether service could be upgraded, we need to compute upgrade paths for
	// desired state of services in [github.com/k0rdent/kcm/api/v1beta1.ClusterDeployment] or
	// [github.com/k0rdent/kcm/api/v1beta1.MultiClusterService] and ensure that services can
	// be upgraded from the version defined in [github.com/k0rdent/kcm/api/v1beta1.ServiceSet]
	// to the desired version.
	desiredServiceVersions := make(map[client.ObjectKey]string)
	desiredServiceTemplates := make(map[client.ObjectKey]string)
	deployedServiceVersions := make(map[client.ObjectKey]string)
	upgradeAvailable := make(map[client.ObjectKey]bool)

	// Build desired services map
	for _, s := range desiredServices {
		key := client.ObjectKey{Namespace: effectiveNamespace(s.Namespace), Name: s.Name}
		if s.Version == "" {
			s.Version = s.Template
		}
		desiredServiceVersions[key] = s.Version
		desiredServiceTemplates[key] = s.Template
		// mark all services upgradeable by default (so new ones won't be skipped)
		upgradeAvailable[key] = true
	}

	services := make([]kcmv1.ServiceWithValues, 0)

	// we'll check whether deployed services could be upgraded to the desired version
	for _, svc := range serviceSet.Spec.Services {
		key := client.ObjectKey{Namespace: effectiveNamespace(svc.Namespace), Name: svc.Name}
		desiredVersion := desiredServiceVersions[key]
		// check upgrade availability
		upgradeAvailable[key] = svc.Version != nil && desiredVersion < *svc.Version ||
			desiredVersionInUpgradePaths(upgradePaths, svc, desiredVersion)
		for _, serviceState := range serviceSet.Status.Services {
			if serviceState.State == kcmv1.ServiceStateDeployed &&
				serviceState.Namespace == svc.Namespace && serviceState.Name == svc.Name && serviceState.Version != nil {
				deployedServiceVersions[key] = *serviceState.Version
			}
		}

		if svc.Version != nil && *svc.Version != deployedServiceVersions[key] {
			services = append(services, svc)

			for i := 0; i < len(desiredServices); {
				if desiredServices[i].Name == svc.Name {
					desiredServices = slices.Delete(desiredServices, i, i+1)
				} else {
					i++
				}
			}
		}
	}

	// Process desired services
	for _, s := range desiredServices {
		key := client.ObjectKey{Namespace: effectiveNamespace(s.Namespace), Name: s.Name}

		// skip disabled services (not deleted from target cluster)
		if s.Disable {
			continue
		}

		// if upgrade is not available, keep the deployed version
		if !upgradeAvailable[key] {
			idx := slices.IndexFunc(serviceSet.Spec.Services, func(svc kcmv1.ServiceWithValues) bool {
				return svc.Name == s.Name && effectiveNamespace(svc.Namespace) == key.Namespace
			})
			if idx >= 0 {
				services = append(services, makeService(s, s.Version, s.Template))
			}
			continue
		}

		desiredVersion := desiredServiceVersions[key]
		desiredTemplate := desiredServiceTemplates[key]
		// if no upgrade paths defined, just deploy desired version
		if len(upgradePaths) == 0 {
			services = append(services, makeService(s, desiredVersion, desiredTemplate))
		}

		// process upgrade paths (assume ordered lowest → highest)
		currentVersion := deployedServiceVersions[key]
		minimumUpgrade := kcmv1.AvailableUpgrade{}

		for _, path := range upgradePaths {
			if path.Name != s.Name {
				continue
			}

			for _, upgrade := range path.AvailableUpgrades {
				for _, availableUpgrade := range upgrade.Versions {
					// Check if it's in the valid upgrade range
					if availableUpgrade.Version >= currentVersion &&
						availableUpgrade.Version <= desiredVersion {
						// If we haven't found any valid upgrade yet, set it
						if minimumUpgrade.Version == "" {
							minimumUpgrade = availableUpgrade
							continue
						}

						// Otherwise, see if this one is smaller than the current minimum
						if availableUpgrade.Version < minimumUpgrade.Version {
							minimumUpgrade = availableUpgrade
						}
					}
				}
			}
		}

		if minimumUpgrade.Version == "" {
			minimumUpgrade = kcmv1.AvailableUpgrade{
				Name:    s.Template,
				Version: desiredVersion,
			}
		}

		services = appendIfNotPresent(services, s, minimumUpgrade)
	}

	return services
}

func desiredVersionInUpgradePaths(
	upgradePaths []kcmv1.ServiceUpgradePaths,
	svc kcmv1.ServiceWithValues,
	desiredVersion string,
) bool {
	var res bool
	for _, upgradePath := range upgradePaths {
		if upgradePath.Name != svc.Name || upgradePath.Namespace != effectiveNamespace(svc.Namespace) {
			continue
		}
		// we'll consider existing version can't be upgraded to the desired version
		// in case existing version does not match version upgrade paths were computed for.
		if upgradePath.Template != svc.Template {
			return false
		}
		for _, upgradeList := range upgradePath.AvailableUpgrades {
			if slices.ContainsFunc(upgradeList.Versions, func(c kcmv1.AvailableUpgrade) bool {
				return c.Version == desiredVersion
			}) {
				return true
			}
		}
		return false
	}
	return res
}

type OperationRequisites struct {
	ObjectKey            client.ObjectKey
	Services             []kcmv1.Service
	ProviderSpec         kcmv1.StateManagementProviderConfig
	PropagateCredentials bool
}

// GetServiceSetWithOperation returns the ServiceSetOperation to perform and the ServiceSet object,
// depending on the existence of the ServiceSet object and the services to deploy.
func GetServiceSetWithOperation(
	ctx context.Context,
	c client.Client,
	operationReq OperationRequisites,
) (*kcmv1.ServiceSet, kcmv1.ServiceSetOperation, error) {
	l := ctrl.LoggerFrom(ctx)
	serviceSet := new(kcmv1.ServiceSet)
	err := c.Get(ctx, operationReq.ObjectKey, serviceSet)
	// if err is not nil and an error is NotFound err,
	// then we'll create serviceSet
	if apierrors.IsNotFound(err) {
		l.V(1).Info("ServiceSet does not exist", "operation", kcmv1.ServiceSetOperationCreate)
		serviceSet.SetName(operationReq.ObjectKey.Name)
		serviceSet.SetNamespace(operationReq.ObjectKey.Namespace)
		return serviceSet, kcmv1.ServiceSetOperationCreate, client.IgnoreNotFound(err)
	}
	// otherwise return an error
	if err != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to get ServiceSet %s: %w", operationReq.ObjectKey, err)
	}

	update, err := needsUpdate(serviceSet, operationReq.ProviderSpec, operationReq.Services)
	if err != nil {
		return nil, "", err
	}
	if update {
		l.V(1).Info("Pending changes, ServiceSet exists", "operation", kcmv1.ServiceSetOperationUpdate)
		return serviceSet, kcmv1.ServiceSetOperationUpdate, nil
	}

	// default case
	l.V(1).Info("No actions required, ServiceSet exists", "operation", kcmv1.ServiceSetOperationNone)
	return serviceSet, kcmv1.ServiceSetOperationNone, nil
}

// needsUpdate checks if the ServiceSet needs to be updated based on the ClusterDeployment spec.
// It first compares the ServiceSet's provider configuration with the ClusterDeployment's service provider configuration.
// Then it compares the ServiceSet's observed services' state with its desired state, and after that it compares
// the ServiceSet's observed services' state with ClusterDeployment's desired services state.
func needsUpdate(
	serviceSet *kcmv1.ServiceSet,
	providerSpec kcmv1.StateManagementProviderConfig,
	services []kcmv1.Service,
) (bool, error) {
	eq, err := areProvidersEqual(&providerSpec, &serviceSet.Spec.Provider)
	if err != nil {
		return false, err
	}
	if !eq {
		// we'll need to update provider configuration if it was changed.
		return true, nil
	}

	desiredServicesMap := make(map[client.ObjectKey]kcmv1.ServiceWithValues)
	for _, s := range serviceSet.Spec.Services {
		desiredServicesMap[client.ObjectKey{Name: s.Name, Namespace: s.Namespace}] = kcmv1.ServiceWithValues{
			Name:        s.Name,
			Namespace:   s.Namespace,
			Template:    s.Template,
			Values:      s.Values,
			ValuesFrom:  s.ValuesFrom,
			HelmOptions: s.HelmOptions,
			HelmAction:  s.HelmAction,
			Version:     s.Version,
		}
	}

	// Compare to the desired services spec of ClusterDeployment/MultiClusterService.
	servicesMap := make(map[client.ObjectKey]kcmv1.ServiceWithValues)
	for _, s := range services {
		svcNamespace := effectiveNamespace(s.Namespace)
		if s.Version == "" {
			s.Version = s.Template
		}
		servicesMap[client.ObjectKey{Name: s.Name, Namespace: svcNamespace}] = kcmv1.ServiceWithValues{
			Name:        s.Name,
			Namespace:   svcNamespace,
			Template:    s.Template,
			Values:      s.Values,
			ValuesFrom:  s.ValuesFrom,
			HelmOptions: s.HelmOptions,
			Version:     &s.Version,
			HelmAction:  s.HelmAction,
		}
	}

	// difference between services defined in ClusterDeployment and ServiceSet means that ServiceSet needs to be updated.
	return !equality.Semantic.DeepEqual(desiredServicesMap, servicesMap), nil
}

func areProvidersEqual(p1, p2 *kcmv1.StateManagementProviderConfig) (bool, error) {
	if p1 == nil && p2 == nil {
		return true, nil
	}
	if p1 == nil || p2 == nil {
		return false, nil
	}

	// We don't compare .Name because it is immutable.

	if p1.SelfManagement != p2.SelfManagement {
		return false, nil
	}

	if p1.Config == nil && p2.Config == nil {
		return true, nil
	}
	if p1.Config == nil || p2.Config == nil {
		return false, nil
	}

	var cfg1 any
	if err := json.Unmarshal(p1.Config.Raw, &cfg1); err != nil {
		return false, fmt.Errorf("failed to unmarshal .spec.serviceSpec.provider.config: %w", err)
	}
	var cfg2 any
	if err := json.Unmarshal(p2.Config.Raw, &cfg2); err != nil {
		return false, fmt.Errorf("failed to unmarshal .spec.serviceSpec.provider.config: %w", err)
	}

	return reflect.DeepEqual(cfg1, cfg2), nil
}

// effectiveNamespace falls back to "default" namespace in case provided service namespace is empty.
func effectiveNamespace(serviceNamespace string) string {
	if serviceNamespace == "" {
		return metav1.NamespaceDefault
	}
	return serviceNamespace
}

// ServiceKey returns a unique identifier for a service
// within [github.com/K0rdent/kcm/api/v1beta1.ServiceSpec].
func ServiceKey(namespace, name string) client.ObjectKey {
	return client.ObjectKey{
		Namespace: effectiveNamespace(namespace),
		Name:      name,
	}
}

// StateManagementProviderConfigFromServiceSpec converts ServiceSpec to StateManagementProviderConfig.
func StateManagementProviderConfigFromServiceSpec(serviceSpec kcmv1.ServiceSpec) (kcmv1.StateManagementProviderConfig, error) {
	type config struct {
		SyncMode             string                                       `json:"syncMode,omitempty"`
		TemplateResourceRefs []addoncontrollerv1beta1.TemplateResourceRef `json:"templateResourceRefs,omitempty"`
		PolicyRefs           []addoncontrollerv1beta1.PolicyRef           `json:"policyRefs,omitempty"`
		DriftIgnore          []libsveltosv1beta1.PatchSelector            `json:"driftIgnore,omitempty"`
		DriftExclusions      []libsveltosv1beta1.DriftExclusion           `json:"driftExclusions,omitempty"`
		Priority             int32                                        `json:"priority,omitempty"`
		StopOnConflict       bool                                         `json:"stopOnConflict,omitempty"`
		Reload               bool                                         `json:"reloader,omitempty"`
		ContinueOnError      bool                                         `json:"continueOnError,omitempty"`
	}

	providerConfig := kcmv1.StateManagementProviderConfig{
		SelfManagement: serviceSpec.Provider.SelfManagement,
	}

	//nolint:staticcheck // SA1019: Deprecated but used for legacy support.
	cfg := config{
		SyncMode:             serviceSpec.SyncMode,
		TemplateResourceRefs: serviceSpec.TemplateResourceRefs,
		PolicyRefs:           serviceSpec.PolicyRefs,
		DriftIgnore:          serviceSpec.DriftIgnore,
		DriftExclusions:      serviceSpec.DriftExclusions,
		Priority:             serviceSpec.Priority,
		StopOnConflict:       serviceSpec.StopOnConflict,
		Reload:               serviceSpec.Reload,
		ContinueOnError:      serviceSpec.ContinueOnError,
	}

	switch {
	// if neither provider name nor config is set, we'll use the
	// default provider and config defined in deprecated fields
	case serviceSpec.Provider.Name == "" && serviceSpec.Provider.Config == nil:
		providerConfig.Name = kubeutil.DefaultStateManagementProvider
		raw, err := json.Marshal(cfg)
		if err != nil {
			return kcmv1.StateManagementProviderConfig{}, fmt.Errorf("failed to marshal config: %w", err)
		}
		providerConfig.Config = &apiextv1.JSON{Raw: raw}

	// if provider name is not set, but config is defined, we'll
	// use the default provider name and defined configuration
	case serviceSpec.Provider.Name == "" && serviceSpec.Provider.Config != nil:
		providerConfig.Name = kubeutil.DefaultStateManagementProvider
		providerConfig.Config = serviceSpec.Provider.Config.DeepCopy()

	// if provider name is set, but config is not defined, we'll
	// use the defined provider name and config falling back to default provider values
	// no-op
	case serviceSpec.Provider.Name != "" && serviceSpec.Provider.Config == nil:
		providerConfig.Name = serviceSpec.Provider.Name

	// if both provider name and config are set, we'll use the defined provider name and config
	// no-op
	case serviceSpec.Provider.Name != "" && serviceSpec.Provider.Config != nil:
		providerConfig = serviceSpec.Provider
		providerConfig.Config = serviceSpec.Provider.Config.DeepCopy()
	}
	return providerConfig, nil
}
