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
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/Masterminds/semver/v3"
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

func fillServiceVersions(ctx context.Context, c client.Client, namespace string, services []kcmv1.Service) error {
	for i := range services {
		if services[i].Version == "" && services[i].Template != "" {
			template := kcmv1.ServiceTemplate{}
			if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: services[i].Template}, &template); err != nil {
				return fmt.Errorf("failed to fetch template %s: %w", services[i].Template, err)
			}

			version := template.Spec.Version
			if version == "" && template.Spec.Helm != nil && template.Spec.Helm.ChartSpec != nil {
				version = template.Spec.Helm.ChartSpec.Version
			}
			services[i].Version = version

			if services[i].Version == "" {
				services[i].Version = services[i].Template
			}
		}
	}
	return nil
}

func fillServiceWithValueVersions(ctx context.Context, c client.Client, namespace string, services []kcmv1.ServiceWithValues) error {
	for i := range services {
		if services[i].Values == "" && services[i].Template != "" {
			template := kcmv1.ServiceTemplate{}
			if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: services[i].Template}, &template); err != nil {
				return fmt.Errorf("failed to fetch Template %s: %w", services[i].Template, err)
			}

			version := template.Spec.Version
			if version == "" && template.Spec.Helm != nil && template.Spec.Helm.ChartSpec != nil {
				version = template.Spec.Helm.ChartSpec.Version
			}
			services[i].Version = &version
			if services[i].Version == nil {
				services[i].Version = &services[i].Template
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
// It does so by fetching all ServiceSets associated with provided
// cd & mcs from cd's namespace or from system namespace if cd is nil.
//
// NOTE: This function works under the assumption that the spec
// is correct. Meaning that it would accept A<-B as correct even
// if A is not defined in the spec as a separate service.
//
// NOTE: This function works under the assumption that there will
// always be just 1 ServiceSet for every unique combination of CD & MCS.
//
// NOTE: This function depends solely on the ServiceSet to fetch the latest
// state of the services. Therefore, it works under the assumption that some
// other mechanism like the poller for the Sveltos adapter will update the
// ServiceSet by fetching the latest state from the specific state manager's objects.
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

	// Fetch serviceSet.
	// We can rely on the state of the services reported in the ServiceSet because:
	//
	// 1. We have configured a poller in the Sveltos ServiceSet controller which
	// polls the Sveltos ClusterSummary and triggers the ServiceSet Controller if
	// there is a change in the state of the services.
	//
	// 2. The ServiceSet Controller then captures the latest state of the services
	// from the ClusterSummary and updates the status of the relevant ServiceSet.
	//
	// 3. The change in the ServiceSet then triggers the ClusterDeployment or MultiClusterService
	// controller to reconcile in which this function is called and we can then can fetch the
	// latest state of the services directly from the relevant ServiceSet.
	//
	// 4. Without the poller triggering the ServiceSet controller, we would have to fetch
	// the state of the services directly from the Sveltos ClusterSummary objects here.
	//
	// 5. Therefore, it is important for any state management adapter to implement a
	// mechanism similar to the poller for the Sveltos ServiceSet controller for this
	// function to work as intended.
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

	// Map of services from the spec of fetched ServiceSets.
	servicesFromSpec := make(map[client.ObjectKey]struct{})
	// Map of services (with their states) from the status of fetched ServiceSets.
	servicesFromStatus := make(map[client.ObjectKey]string)
	for _, sset := range serviceSets.Items {
		for _, svc := range sset.Spec.Services {
			servicesFromSpec[ServiceKey(svc.Namespace, svc.Name)] = struct{}{}
		}
		for _, svc := range sset.Status.Services {
			servicesFromStatus[ServiceKey(svc.Namespace, svc.Name)] = svc.State
		}
	}

	// Find out which services have been successfully deployed.
	for svc, state := range servicesFromStatus {
		sKey := ServiceKey(svc.Namespace, svc.Name)
		if state == kcmv1.ServiceStateDeployed {
			deployedServices[sKey] = struct{}{}
		} else {
			// If state for svc is other than Deployed, then check if any of the
			// dependents of svc already exist in the spec of the fetched ServiceSet.
			// The mere presence of a dependent of svc in the fetched ServiceSet's spec
			// means that the svc was indeed successfully deployed sometime in the past
			// (even if it is currently !Deployed). Therefore, we will treat svc as deployed
			// by adding it to the map of deployed services so that it's dependents are
			// considered as potential services to be added to or retained in the ServiceSet's spec.
			if deps, ok := dependents[sKey]; ok {
				for _, d := range deps {
					if _, ok := servicesFromSpec[d]; ok {
						deployedServices[sKey] = struct{}{}
					}
				}
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
	// deploy having depends on count <= 0.
	var filtered []kcmv1.Service
	for svc, count := range dependsOnCount {
		if count <= 0 {
			idx := serviceIdx[ServiceKey(svc.Namespace, svc.Name)]
			filtered = append(filtered, desiredServices[idx])
		}
	}

	// Sort for deterministic ordering across reconcile cycles.
	slices.SortFunc(filtered, func(a, b kcmv1.Service) int {
		if n := cmp.Compare(effectiveNamespace(a.Namespace), effectiveNamespace(b.Namespace)); n != 0 {
			return n
		}
		return cmp.Compare(a.Name, b.Name)
	})

	return filtered, nil
}

func makeService(s kcmv1.Service, version, template string) kcmv1.ServiceWithValues {
	return kcmv1.ServiceWithValues{
		Name: s.Name,
		// We should always use effective namespace, because service namespace in
		// serviceSet's service definition is never empty while service namespace
		// in clusterDeployment's/multiClusterService's service definition can be empty.
		// This will lead to persistent discrepancy between service definitions and
		// lead to continuous serviceSet updates.
		Namespace:   effectiveNamespace(s.Namespace),
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
	serviceNamespace := effectiveNamespace(s.Namespace)
	exists := slices.ContainsFunc(services, func(c kcmv1.ServiceWithValues) bool {
		return c.Name == s.Name &&
			c.Namespace == serviceNamespace &&
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

	services := servicesToBeUpdated(
		serviceSet,
		desiredServices,
		deployedServiceVersions,
		desiredServiceVersions,
		upgradeAvailable,
		upgradePaths)

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

func normalizeVersion(v string) string {
	versionRegex := regexp.MustCompile(`\d+\.\d+\.\d+|\d+-\d+-\d+`)
	m := versionRegex.FindStringSubmatch(v)
	if len(m) != 4 {
		return ""
	}
	return m[1] + "." + m[2] + "." + m[3]
}

func isUpgradeAvailable(current, desired string) bool {
	cur := normalizeVersion(current)
	des := normalizeVersion(desired)

	if cur == "" || des == "" {
		return false
	}

	curVer, err := semver.NewVersion(cur)
	if err != nil {
		return false
	}

	desVer, err := semver.NewVersion(des)
	if err != nil {
		return false
	}

	return curVer.GreaterThan(desVer)
}

func servicesToBeUpdated(
	serviceSet *kcmv1.ServiceSet,
	desiredServices []kcmv1.Service,
	deployedServiceVersions map[client.ObjectKey]string,
	desiredServiceVersions map[client.ObjectKey]string,
	upgradeAvailable map[client.ObjectKey]bool,
	upgradePaths []kcmv1.ServiceUpgradePaths,
) []kcmv1.ServiceWithValues {
	services := make([]kcmv1.ServiceWithValues, 0)

	// we'll check whether deployed services could be upgraded to the desired version
	for _, svc := range serviceSet.Spec.Services {
		effectiveServiceNs := effectiveNamespace(svc.Namespace)
		key := client.ObjectKey{Namespace: effectiveServiceNs, Name: svc.Name}
		desiredVersion := desiredServiceVersions[key]
		// check upgrade availability
		upgradeAvailable[key] = svc.Version != nil && isUpgradeAvailable(*svc.Version, desiredVersion) ||
			desiredVersionInUpgradePaths(upgradePaths, svc, desiredVersion)
		for _, serviceState := range serviceSet.Status.Services {
			if serviceState.State == kcmv1.ServiceStateDeployed &&
				serviceState.Namespace == effectiveServiceNs && serviceState.Name == svc.Name && serviceState.Version != nil {
				deployedServiceVersions[key] = *serviceState.Version
			}
		}

		if svc.Version == nil || *svc.Version == deployedServiceVersions[key] {
			continue
		}

		// Merge mutable fields from the desired service spec (e.g. updated values after a
		// failed deploy) while preserving the in-flight version for upgrade-path tracking.
		for i := 0; i < len(desiredServices); {
			ds := desiredServices[i]
			if ds.Name == svc.Name && effectiveNamespace(ds.Namespace) == effectiveServiceNs {
				svc.Values = ds.Values
				svc.ValuesFrom = ds.ValuesFrom
				svc.HelmOptions = ds.HelmOptions
				svc.HelmAction = ds.HelmAction
				desiredServices = slices.Delete(desiredServices, i, i+1)
			} else {
				i++
			}
		}
		services = append(services, svc)
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
	ObjectKey       client.ObjectKey
	MCS             *kcmv1.MultiClusterService
	CD              *kcmv1.ClusterDeployment
	SystemNamespace string
}

// GetServiceSetWithOperation fetches or initialises the ServiceSet identified by
// operationReq.ObjectKey, runs the full services pipeline, and returns the resulting
// ServiceSet together with the operation that should be performed on it.
func GetServiceSetWithOperation(
	ctx context.Context,
	c client.Client,
	operationReq OperationRequisites,
) (*kcmv1.ServiceSet, kcmv1.ServiceSetOperation, error) {
	l := ctrl.LoggerFrom(ctx)

	// Determine desired services and service spec from MCS or CD.
	var desiredServices []kcmv1.Service
	var serviceSpec kcmv1.ServiceSpec
	if operationReq.MCS != nil {
		desiredServices = operationReq.MCS.Spec.ServiceSpec.Services
		serviceSpec = operationReq.MCS.Spec.ServiceSpec
	} else {
		desiredServices = operationReq.CD.Spec.ServiceSpec.Services
		serviceSpec = operationReq.CD.Spec.ServiceSpec
	}

	// Resolve the provider that backs this ServiceSet.
	providerSpec, err := StateManagementProviderConfigFromServiceSpec(serviceSpec)
	if err != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to convert ServiceSpec to provider config: %w", err)
	}
	provider := new(kcmv1.StateManagementProvider)
	if err := c.Get(ctx, client.ObjectKey{Name: providerSpec.Name}, provider); err != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to get StateManagementProvider %s: %w", providerSpec.Name, err)
	}

	// Get ServiceSet, create if it does not exist
	serviceSet := new(kcmv1.ServiceSet)
	op := kcmv1.ServiceSetOperationUpdate
	err = c.Get(ctx, operationReq.ObjectKey, serviceSet)
	if apierrors.IsNotFound(err) {
		l.V(1).Info("ServiceSet does not exist", "operation", kcmv1.ServiceSetOperationCreate)
		serviceSet.SetName(operationReq.ObjectKey.Name)
		serviceSet.SetNamespace(operationReq.ObjectKey.Namespace)
		op = kcmv1.ServiceSetOperationCreate
	} else if err != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to get ServiceSet %s: %w", operationReq.ObjectKey, err)
	}

	// Save current spec before Build() overwrites it in place
	// to compare spec and decide whether action is required.
	existingSpec := serviceSet.Spec.DeepCopy()

	templateNamespace := serviceSet.Namespace
	upgradePaths, err := ServicesUpgradePaths(
		ctx, c, ServicesWithDesiredChains(desiredServices, serviceSet.Spec.Services), templateNamespace)
	if err != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to determine upgrade paths: %w", err)
	}
	l.V(1).Info("Determined upgrade paths for services", "upgradePaths", upgradePaths)

	filteredServices, err := FilterServiceDependencies(
		ctx, c, operationReq.SystemNamespace, operationReq.MCS, operationReq.CD, desiredServices)
	if err != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to filter service dependencies: %w", err)
	}
	l.V(1).Info("Services after dependency filtering", "services", filteredServices)

	if err := fillServiceVersions(ctx, c, templateNamespace, filteredServices); err != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to resolve versions for filtered services: %w", err)
	}

	if err := fillServiceWithValueVersions(ctx, c, templateNamespace, serviceSet.Spec.Services); err != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to resolve versions for service set services: %w", err)
	}

	resultingServices := ServicesToDeploy(upgradePaths, filteredServices, serviceSet)
	l.V(1).Info("Services to deploy", "services", resultingServices)

	candidate, err := NewBuilder(operationReq.CD, serviceSet, provider.Spec.Selector).
		WithMultiClusterService(operationReq.MCS).
		WithServicesToDeploy(resultingServices).Build()
	if err != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to build ServiceSet: %w", err)
	}

	if op == kcmv1.ServiceSetOperationUpdate && equality.Semantic.DeepEqual(existingSpec, &candidate.Spec) {
		l.V(1).Info("No actions required, ServiceSet is up to date", "operation", kcmv1.ServiceSetOperationNone)
		return serviceSet, kcmv1.ServiceSetOperationNone, nil
	}

	l.V(1).Info("ServiceSet requires action", "operation", op)
	return candidate, op, nil
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
