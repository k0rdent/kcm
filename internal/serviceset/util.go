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
	"errors"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// ServiceSetObjectKey generates a unique key for a ServiceSet given the input and returns it.
func ServiceSetObjectKey(systemNamespace string, cd *kcmv1.ClusterDeployment, mcs *kcmv1.MultiClusterService) client.ObjectKey {
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
			Namespace: svc.Namespace,
			Name:      svc.Name,
		}] = svc.TemplateChain
	}

	for _, svc := range deployedServices {
		chain := chainMap[client.ObjectKey{
			Namespace: svc.Namespace,
			Name:      svc.Name,
		}]
		res = append(res, kcmv1.Service{
			Name:          svc.Name,
			Namespace:     svc.Namespace,
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
	var errs error
	servicesUpgradePaths := make([]kcmv1.ServiceUpgradePaths, 0, len(services))

	for _, svc := range services {
		serviceNamespace := svc.Namespace
		if serviceNamespace == "" {
			serviceNamespace = metav1.NamespaceDefault
		}

		serviceUpgradePaths := kcmv1.ServiceUpgradePaths{
			Name:      svc.Name,
			Namespace: serviceNamespace,
			Template:  svc.Template,
		}

		if svc.TemplateChain == "" {
			// Add service as an available upgrade for itself.
			// E.g., if the service needs to be upgraded with new helm values.
			serviceUpgradePaths.AvailableUpgrades = append(serviceUpgradePaths.AvailableUpgrades, kcmv1.UpgradePath{
				Versions: []string{svc.Template},
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

// ServicesToDeploy returns the services to deploy based on the ClusterDeployment spec,
// taking into account already deployed services, and versioning.
func ServicesToDeploy(
	upgradePaths []kcmv1.ServiceUpgradePaths,
	desiredServices []kcmv1.Service,
	deployedServices []kcmv1.ServiceWithValues,
) []kcmv1.ServiceWithValues {
	// todo: implement sequential version updates, taking into account observed services state

	// to determine, whether service could be upgraded, we need to compute upgrade paths for
	// desired state of services in [github.com/k0rdent/kcm/api/v1beta1.ClusterDeployment] or
	// [github.com/k0rdent/kcm/api/v1beta1.MultiClusterService] and ensure that services can
	// be upgraded from the version defined in [github.com/k0rdent/kcm/api/v1beta1.ServiceSet]
	// to the desired version.
	desiredServiceVersionsMap := make(map[client.ObjectKey]string)
	upgradeAvailableMap := make(map[client.ObjectKey]bool)
	for _, s := range desiredServices {
		serviceKey := ServiceKey(s.Namespace, s.Name)
		desiredServiceVersionsMap[serviceKey] = s.Template
		// we'll fill the upgrade availability map with "true"
		// for all services. This is needed to not to check
		// new services which will absent in service set
		upgradeAvailableMap[serviceKey] = true
	}

	// we'll check whether deployed services could be upgraded to the desired version
	for _, svc := range deployedServices {
		svcNamespace := effectiveNamespace(svc.Namespace)
		desiredVersion := desiredServiceVersionsMap[client.ObjectKey{
			Namespace: svcNamespace,
			Name:      svc.Name,
		}]
		upgradeAvailableMap[client.ObjectKey{
			Namespace: svcNamespace,
			Name:      svc.Name,
		}] = desiredVersionInUpgradePaths(upgradePaths, svc, desiredVersion)
	}

	services := make([]kcmv1.ServiceWithValues, 0)
	for _, s := range desiredServices {
		// disable field defines whether service should be processed or not,
		// setting it to "true" should not result into service deletion on
		// the target cluster, thus we'll just continue
		if s.Disable {
			continue
		}
		svcNamespace := effectiveNamespace(s.Namespace)
		var serviceToDeploy kcmv1.ServiceWithValues
		if !upgradeAvailableMap[client.ObjectKey{
			Namespace: svcNamespace,
			Name:      s.Name,
		}] {
			// if upgrade is not available for service we should keep existing version
			idx := slices.IndexFunc(deployedServices, func(svc kcmv1.ServiceWithValues) bool {
				return svc.Name == s.Name && svc.Namespace == svcNamespace
			})
			if idx < 0 {
				continue
			}
			// NOTE: If we do not add a service as an available upgrade for itself in ServiceUpgradePaths func,
			// the new service spec will be ignored and the old (already deployed) spec will be used.
			// This creates a bug where any update to helm values without changing the service is not reflected.
			serviceToDeploy = deployedServices[idx]
		} else {
			serviceToDeploy = kcmv1.ServiceWithValues{
				Name:        s.Name,
				Namespace:   svcNamespace,
				Template:    s.Template,
				Values:      s.Values,
				ValuesFrom:  s.ValuesFrom,
				HelmOptions: s.HelmOptions,
			}
		}
		services = append(services, serviceToDeploy)
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
		if upgradePath.Name != svc.Name || upgradePath.Namespace != svc.Namespace {
			continue
		}
		// we'll consider existing version can't be upgraded to the desired version
		// in case existing version does not match version upgrade paths were computed for.
		if upgradePath.Template != svc.Template {
			return false
		}
		for _, upgradeList := range upgradePath.AvailableUpgrades {
			if slices.Contains(upgradeList.Versions, desiredVersion) {
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
	if client.IgnoreNotFound(err) != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to get ServiceSet %s: %w", operationReq.ObjectKey, err)
	}

	serviceSetRequired := len(operationReq.Services) > 0 || operationReq.PropagateCredentials
	switch {
	case err != nil:
		if serviceSetRequired {
			l.V(1).Info("Pending services to deploy, ServiceSet does not exist", "operation", kcmv1.ServiceSetOperationCreate)
			serviceSet.SetName(operationReq.ObjectKey.Name)
			serviceSet.SetNamespace(operationReq.ObjectKey.Namespace)
			return serviceSet, kcmv1.ServiceSetOperationCreate, nil
		}
		l.V(1).Info("No services to deploy, ServiceSet does not exist", "operation", kcmv1.ServiceSetOperationNone)
		return nil, kcmv1.ServiceSetOperationNone, nil
	case !serviceSetRequired:
		l.V(1).Info("No services to deploy, ServiceSet exists", "operation", kcmv1.ServiceSetOperationDelete)
		return serviceSet, kcmv1.ServiceSetOperationDelete, nil
	case needsUpdate(serviceSet, operationReq.ProviderSpec, operationReq.Services):
		l.V(1).Info("Pending services to deploy, ServiceSet exists", "operation", kcmv1.ServiceSetOperationUpdate)
		return serviceSet, kcmv1.ServiceSetOperationUpdate, nil
	default:
		l.V(1).Info("No actions required, ServiceSet exists", "operation", kcmv1.ServiceSetOperationNone)
		return serviceSet, kcmv1.ServiceSetOperationNone, nil
	}
}

// needsUpdate checks if the ServiceSet needs to be updated based on the ClusterDeployment spec.
// It first compares the ServiceSet's provider configuration with the ClusterDeployment's service provider configuration.
// Then it compares the ServiceSet's observed services' state with its desired state, and after that it compares
// the ServiceSet's observed services' state with ClusterDeployment's desired services state.
func needsUpdate(
	serviceSet *kcmv1.ServiceSet,
	providerSpec kcmv1.StateManagementProviderConfig,
	services []kcmv1.Service,
) bool {
	// we'll need to update provider configuration if it was changed.
	if !equality.Semantic.DeepEqual(providerSpec, serviceSet.Spec.Provider) {
		return true
	}

	// we'll need to compare observed services' state with desired state to ensure
	// ServiceSet was already reconciled and services are properly deployed.
	// we won't update ServiceSet until that.
	observedServiceStateMap := make(map[client.ObjectKey]kcmv1.ServiceState)
	for _, s := range serviceSet.Status.Services {
		observedServiceStateMap[client.ObjectKey{Name: s.Name, Namespace: s.Namespace}] = kcmv1.ServiceState{
			Name:      s.Name,
			Namespace: s.Namespace,
			Template:  s.Template,
			State:     s.State,
		}
	}
	desiredServiceStateMap := make(map[client.ObjectKey]kcmv1.ServiceState)
	desiredServicesMap := make(map[client.ObjectKey]kcmv1.ServiceWithValues)
	for _, s := range serviceSet.Spec.Services {
		desiredServiceStateMap[client.ObjectKey{Name: s.Name, Namespace: s.Namespace}] = kcmv1.ServiceState{
			Name:      s.Name,
			Namespace: s.Namespace,
			Template:  s.Template,
			State:     kcmv1.ServiceStateDeployed,
		}
		desiredServicesMap[client.ObjectKey{Name: s.Name, Namespace: s.Namespace}] = kcmv1.ServiceWithValues{
			Name:        s.Name,
			Namespace:   s.Namespace,
			Template:    s.Template,
			Values:      s.Values,
			ValuesFrom:  s.ValuesFrom,
			HelmOptions: s.HelmOptions,
		}
	}

	// This is comparing the spec to the status of the ServiceSet fetched from kubernetes.
	// The difference between observed and desired services state means that ServiceSet was not fully
	// deployed yet. Therefore we won't update ServiceSet until that.
	if !equality.Semantic.DeepEqual(observedServiceStateMap, desiredServiceStateMap) {
		return false
	}

	// now, since ServiceSet is fully deployed, we can compare it with ClusterDeployment's desired services state.
	clusterDeploymentServicesMap := make(map[client.ObjectKey]kcmv1.ServiceWithValues)
	for _, s := range services {
		svcNamespace := effectiveNamespace(s.Namespace)
		clusterDeploymentServicesMap[client.ObjectKey{Name: s.Name, Namespace: svcNamespace}] = kcmv1.ServiceWithValues{
			Name:        s.Name,
			Namespace:   svcNamespace,
			Template:    s.Template,
			Values:      s.Values,
			ValuesFrom:  s.ValuesFrom,
			HelmOptions: s.HelmOptions,
		}
	}

	// difference between services defined in ClusterDeployment and ServiceSet means that ServiceSet needs to be updated.
	return !equality.Semantic.DeepEqual(desiredServicesMap, clusterDeploymentServicesMap)
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
