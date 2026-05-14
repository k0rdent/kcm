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
				return fmt.Errorf("failed to fetch template %s/%s: %w", namespace, svc.Template, err)
			}

			version := template.Spec.Version
			if version == "" && template.Spec.Helm != nil && template.Spec.Helm.ChartSpec != nil {
				version = template.Spec.Helm.ChartSpec.Version
			}

			if version == "" {
				svc.Version = svc.Template
			} else {
				svc.Version = version
			}
		}
	}
	return nil
}

func fillServiceWithValueVersions(ctx context.Context, c client.Client, namespace string, services []*kcmv1.ServiceWithValues) error {
	for _, svc := range services {
		if (svc.Version == nil || *svc.Version == "") && svc.Template != "" {
			template := kcmv1.ServiceTemplate{}
			if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: svc.Template}, &template); err != nil {
				return fmt.Errorf("failed to fetch Template %s/%s: %w", namespace, svc.Template, err)
			}

			version := template.Spec.Version
			if version == "" && template.Spec.Helm != nil && template.Spec.Helm.ChartSpec != nil {
				version = template.Spec.Helm.ChartSpec.Version
			}

			if version == "" {
				svc.Version = new(svc.Template)
			} else {
				svc.Version = new(version)
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
// A service is eligible (its count reaches zero) only when every service it
// directly or transitively depends on is fully synced — all of:
//   - status is Deployed,
//   - Status.Version == Spec.Version (no in-flight upgrade),
//   - Spec.Version == user's desired version (no advancement queued).
//
// Services in a non-Deployed state (Failed, Provisioning, etc.) are NOT treated
// as deployed, even if their dependents are already in the ServiceSet spec.
// This guarantees that a failing dependency keeps its dependents locked at
// their stored version (via BuildServicesList) rather than allowing them to be
// upgraded or mutated. The version checks extend the same protection to upgrade
// scenarios: while a dependency is mid-upgrade or about to be advanced, its
// dependents stay locked at their stored versions, ensuring upgrades propagate
// down the dependency chain in the declared order rather than all at once.
//
// NOTE: Every service referenced in a DependsOn field must also appear in
// the desired services list; referencing an absent service is an error.
//
// NOTE: desiredServices is expected to have its Versions already resolved by
// the caller (e.g. via ResolveServiceVersions) so the version-aware gate can
// compare against the resolved form that previous reconciles persisted in
// ServiceSet.Spec. Passing unresolved services is tolerated — comparison
// falls back to Template — but mixing resolved Spec versions with unresolved
// desired versions will lock dependents.
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

	// Validate that every DependsOn reference resolves to a service present in
	// the desired list. Referring to an absent service is an error: the caller
	// must either add the missing service or remove the dependency.
	for _, svc := range desiredServices {
		for _, dep := range svc.DependsOn {
			depKey := ServiceKey(dep.Namespace, dep.Name)
			if _, ok := serviceIdx[depKey]; !ok {
				return nil, fmt.Errorf("service %s/%s depends on %s/%s which is not present in the desired services list",
					effectiveNamespace(svc.Namespace), svc.Name, depKey.Namespace, depKey.Name)
			}
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

	// Build version maps from the existing ServiceSet(s) so we can compare each
	// service's currently-promised spec step against what the cluster actually
	// runs and against the user's final desired version. Spec and status entries
	// are normalised the same way so the comparison is symmetric: prefer Version,
	// fall back to Template when Version is nil/empty.
	//
	// NOTE: When more than one ServiceSet matches (cd-only call with multiple
	// MCS targeting the same cluster, or analogous), values overwrite on key
	// collision. Current reconciler wiring guarantees at most one relevant
	// ServiceSet per call (the MCS reconciler scopes by both cluster and MCS;
	// the CD reconciler operates on the CD's own spec/ServiceSet only), so the
	// overwrite is not observable in practice. Revisit if that wiring changes.
	specVersion := make(map[client.ObjectKey]string)
	statusVersion := make(map[client.ObjectKey]string)
	statusState := make(map[client.ObjectKey]string)
	for _, sset := range serviceSets.Items {
		for _, svc := range sset.Spec.Services {
			v := ""
			if svc.Version != nil {
				v = *svc.Version
			}
			if v == "" {
				v = svc.Template
			}
			specVersion[ServiceKey(svc.Namespace, svc.Name)] = v
		}
		for _, svc := range sset.Status.Services {
			v := ""
			if svc.Version != nil {
				v = *svc.Version
			}
			if v == "" {
				v = svc.Template
			}
			k := ServiceKey(svc.Namespace, svc.Name)
			statusVersion[k] = v
			statusState[k] = svc.State
		}
	}

	// desiredServices is expected to have Versions resolved by the caller
	// (see ResolveServicesToApply). For each service, prefer Version; fall back
	// to Template — same normalisation used above for spec/status — so the
	// gate comparisons are like-for-like.
	desiredVersion := make(map[client.ObjectKey]string, len(desiredServices))
	for _, svc := range desiredServices {
		v := svc.Version
		if v == "" {
			v = svc.Template
		}
		desiredVersion[ServiceKey(svc.Namespace, svc.Name)] = v
	}

	// A service counts as "deployed" for the purpose of unlocking its dependents
	// only when all three hold:
	//   1. its status is Deployed (the current step actually runs on the cluster),
	//   2. Status.Version == Spec.Version (no in-flight upgrade), and
	//   3. Spec.Version == user's desired version (no advancement queued for this
	//      reconcile).
	// This restores correct dependency ordering on upgrades: a service whose user
	// just bumped the desired version stops satisfying its dependents until the
	// new version is both written into Spec and observed in Status.
	for k := range serviceIdx {
		if statusState[k] != kcmv1.ServiceStateDeployed {
			continue
		}
		if statusVersion[k] != specVersion[k] {
			continue
		}
		if specVersion[k] != desiredVersion[k] {
			continue
		}
		deployedServices[k] = struct{}{}
	}

	// For each of the successfully deployed services,
	// decrement the depends on count of its dependents.
	for svc := range deployedServices {
		for _, d := range dependents[ServiceKey(svc.Namespace, svc.Name)] {
			dependsOnCount[ServiceKey(d.Namespace, d.Name)]--
		}
	}

	// Create a new list of services whose dependencies are fully satisfied
	// and which are therefore eligible to be applied or upgraded right away.
	// Services that are already deployed but have unsatisfied dependencies
	// (locked) are intentionally excluded here; BuildServicesList preserves
	// them at their current version by carrying them over from the stored spec.
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
		Version:     new(version),
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

// minimumUpgradeStep returns the smallest available upgrade version for the named
// service that falls within [currentVersion, desiredVersion]. Returns a zero-value
// AvailableUpgrade if no matching step is found in upgradePaths.
func minimumUpgradeStep(upgradePaths []kcmv1.ServiceUpgradePaths, name, currentVersion, desiredVersion string) kcmv1.AvailableUpgrade {
	current, err := semver.NewVersion(currentVersion)
	if err != nil {
		return kcmv1.AvailableUpgrade{}
	}
	desired, err := semver.NewVersion(desiredVersion)
	if err != nil {
		return kcmv1.AvailableUpgrade{}
	}

	var result kcmv1.AvailableUpgrade
	var resultSV *semver.Version

	for _, path := range upgradePaths {
		if path.Name != name {
			continue
		}
		for _, upgrade := range path.AvailableUpgrades {
			for _, u := range upgrade.Versions {
				v, err := semver.NewVersion(u.Version)
				if err != nil {
					continue
				}
				if v.Compare(current) >= 0 && v.Compare(desired) <= 0 {
					if resultSV == nil || v.Compare(resultSV) < 0 {
						result = u
						resultSV = v
					}
				}
			}
		}
	}
	return result
}

// ServicesToDeploy computes the target ServiceWithValues for each service in
// filteredServices (i.e. services whose dependencies are already satisfied),
// taking into account in-flight upgrades and upgrade-path constraints.
// Services not in filteredServices are handled separately by BuildServicesList.
func ServicesToDeploy(
	upgradePaths []kcmv1.ServiceUpgradePaths,
	filteredServices []kcmv1.Service,
	serviceSet *kcmv1.ServiceSet,
) []kcmv1.ServiceWithValues {
	desiredVersions := make(map[client.ObjectKey]string)
	desiredTemplates := make(map[client.ObjectKey]string)
	deployedVersions := make(map[client.ObjectKey]string)
	upgradeAvailable := make(map[client.ObjectKey]bool)

	for _, s := range filteredServices {
		key := ServiceKey(s.Namespace, s.Name)
		ver := s.Version
		if ver == "" {
			ver = s.Template
		}
		desiredVersions[key] = ver
		desiredTemplates[key] = s.Template
		upgradeAvailable[key] = true // upgradeable by default; overridden below for stored services
	}

	// For stored services that are also in filteredServices: determine upgrade
	// availability and track the deployed version. If the stored version differs
	// from the deployed version the service is in-flight; emit it immediately with
	// mutable fields merged from the desired spec and skip it in the main loop.
	inFlight := make(map[client.ObjectKey]bool)
	var services []kcmv1.ServiceWithValues

	for _, svc := range serviceSet.Spec.Services {
		key := ServiceKey(svc.Namespace, svc.Name)
		if _, ok := desiredVersions[key]; !ok {
			continue // not in filteredServices; BuildServicesList handles it
		}

		desiredVersion := desiredVersions[key]
		upgradeAvailable[key] = svc.Version != nil && desiredVersion < *svc.Version ||
			desiredVersionInUpgradePaths(upgradePaths, svc, desiredVersion)

		for _, state := range serviceSet.Status.Services {
			if state.State == kcmv1.ServiceStateDeployed &&
				effectiveNamespace(state.Namespace) == effectiveNamespace(svc.Namespace) &&
				state.Name == svc.Name && state.Version != nil {
				deployedVersions[key] = *state.Version
			}
		}

		if svc.Version == nil || *svc.Version == deployedVersions[key] {
			continue // not in-flight
		}

		// In-flight: merge mutable fields from the desired spec while preserving
		// the in-flight version so upgrade-path tracking is not disrupted.
		for _, ds := range filteredServices {
			if ds.Name == svc.Name && effectiveNamespace(ds.Namespace) == effectiveNamespace(svc.Namespace) {
				svc.Values = ds.Values
				svc.ValuesFrom = ds.ValuesFrom
				svc.HelmOptions = ds.HelmOptions
				svc.HelmAction = ds.HelmAction
				break
			}
		}
		services = append(services, svc)
		inFlight[key] = true
	}

	// Process remaining filteredServices (not in-flight).
	for _, s := range filteredServices {
		key := ServiceKey(s.Namespace, s.Name)

		if inFlight[key] {
			continue
		}

		// skip disabled services (not deleted from target cluster)
		if s.Disable {
			continue
		}

		desiredVersion := desiredVersions[key]
		desiredTemplate := desiredTemplates[key]

		// if upgrade is not available, keep the current stored version
		if !upgradeAvailable[key] {
			idx := slices.IndexFunc(serviceSet.Spec.Services, func(svc kcmv1.ServiceWithValues) bool {
				return svc.Name == s.Name && effectiveNamespace(svc.Namespace) == key.Namespace
			})
			if idx >= 0 {
				services = append(services, makeService(s, s.Version, s.Template))
			}
			continue
		}

		// if no upgrade paths defined, deploy the desired version directly
		if len(upgradePaths) == 0 {
			services = append(services, makeService(s, desiredVersion, desiredTemplate))
			continue
		}

		// find the minimum valid upgrade step towards the desired version
		currentVersion := deployedVersions[key]
		minimumUpgrade := minimumUpgradeStep(upgradePaths, s.Name, currentVersion, desiredVersion)
		if minimumUpgrade.Version == "" {
			minimumUpgrade = kcmv1.AvailableUpgrade{
				Name:    desiredTemplate,
				Version: desiredVersion,
			}
		}

		services = appendIfNotPresent(services, s, minimumUpgrade)
	}

	return services
}

// BuildServicesList produces the final list of services for the ServiceSet spec by:
//  1. Including all services from filtered (with their computed target versions).
//  2. Preserving services from stored that are still present in desired but were
//     not included in filtered (locked — dependencies not yet satisfied).
//  3. Dropping services from stored that are no longer present in desired
//     (explicitly removed by the user).
func BuildServicesList(
	stored []kcmv1.ServiceWithValues,
	filtered []kcmv1.ServiceWithValues,
	desired []kcmv1.Service,
) []kcmv1.ServiceWithValues {
	desiredSet := make(map[client.ObjectKey]struct{}, len(desired))
	for _, svc := range desired {
		desiredSet[ServiceKey(svc.Namespace, svc.Name)] = struct{}{}
	}

	filteredSet := make(map[client.ObjectKey]struct{}, len(filtered))
	for _, svc := range filtered {
		filteredSet[ServiceKey(svc.Namespace, svc.Name)] = struct{}{}
	}

	result := make([]kcmv1.ServiceWithValues, 0, len(filtered)+len(stored))
	result = append(result, filtered...)

	for _, svc := range stored {
		key := ServiceKey(svc.Namespace, svc.Name)
		if _, ok := desiredSet[key]; !ok {
			continue // no longer desired — drop it
		}
		if _, ok := filteredSet[key]; ok {
			continue // already covered by filtered — skip
		}
		result = append(result, svc) // locked — preserve at current version
	}

	return result
}

// ResolveServicesToApply resolves which services from desiredServices are eligible
// to be applied now (all dependencies satisfied) and computes the correct target
// version for each, taking into account upgrade paths and any in-flight upgrades
// already present in the ServiceSet spec.
func ResolveServicesToApply(
	ctx context.Context,
	c client.Client,
	systemNamespace string,
	mcs *kcmv1.MultiClusterService,
	cd *kcmv1.ClusterDeployment,
	desiredServices []kcmv1.Service,
	serviceSet *kcmv1.ServiceSet,
) ([]kcmv1.ServiceWithValues, error) {
	templateNamespace := serviceSet.Namespace

	// Resolve desiredServices' Versions once up front, on a copy to avoid
	// mutating the caller's MCS/CD CR. FilterServiceDependencies and the
	// downstream version-aware logic both consume the resolved slice, so the
	// previous double-resolve (once inside FilterServiceDependencies, once on
	// the returned filteredServices) is avoided.
	resolvedDesired := make([]kcmv1.Service, len(desiredServices))
	copy(resolvedDesired, desiredServices)
	if err := ResolveServiceVersions(ctx, c, templateNamespace, resolvedDesired); err != nil {
		return nil, fmt.Errorf("failed to resolve versions for desired services: %w", err)
	}

	filteredServices, err := FilterServiceDependencies(ctx, c, systemNamespace, mcs, cd, resolvedDesired)
	if err != nil {
		return nil, fmt.Errorf("failed to filter service dependencies: %w", err)
	}

	storedServices := serviceSet.Spec.Services
	if err := ResolveServiceVersions(ctx, c, templateNamespace, storedServices); err != nil {
		return nil, fmt.Errorf("failed to resolve versions for stored services: %w", err)
	}

	upgradePaths, err := ServicesUpgradePaths(
		ctx, c, ServicesWithDesiredChains(resolvedDesired, storedServices), templateNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to determine upgrade paths: %w", err)
	}

	return ServicesToDeploy(upgradePaths, filteredServices, serviceSet), nil
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
// operationReq.ObjectKey, runs the full services resolution pipeline, and returns
// the resulting ServiceSet together with the operation that should be performed on it.
//
// # Pipeline overview
//
// The function resolves which services to include in the ServiceSet spec through
// three stages:
//
//  1. Dependency filtering ([FilterServiceDependencies]): services whose dependencies
//     are not yet fully deployed are excluded from the "eligible" set. Only services
//     with all dependencies in [kcmv1.ServiceStateDeployed] state are eligible to be
//     applied or upgraded in this reconcile cycle.
//
//  2. Version resolution ([ServicesToDeploy]): for each eligible service the correct
//     target version is computed, respecting any in-flight upgrades already present in
//     the stored spec and honouring step-wise upgrade paths defined in
//     [kcmv1.ServiceTemplateChain].
//
//  3. List merging ([BuildServicesList]): the final spec is assembled from the eligible
//     services (with their computed versions) plus any stored services that are still
//     desired but were excluded from the eligible set (locked at their current version).
//     Services no longer present in the desired list are dropped.
//
// # Corner cases
//
// Unsatisfied dependency — if service B depends on A and A has not reached
// [kcmv1.ServiceStateDeployed] yet (e.g. it is still Provisioning), B is excluded
// from the eligible set. B is preserved in the ServiceSet spec at its current stored
// version (locked) and will become eligible once A is deployed.
//
// Failing dependency — if A is in a Failed state, B remains locked for as long as A
// stays non-Deployed. Critically, B is never upgraded or mutated while its dependency
// is failing; it is carried over from the stored spec verbatim. This prevents
// accidental mutation of a running service because one of its upstream dependencies
// entered a degraded state.
//
// Removed service with dependents — if a service is removed from the desired list
// while other desired services still reference it via DependsOn, the function returns
// an error. The controller will not reconcile until the spec is consistent (either
// the removed service is added back or all DependsOn references to it are cleaned up).
// This prevents accidental cascade-deletion of a dependent service stack.
//
// New service added as dependency of an existing deployed service — the existing
// service gains an unsatisfied dependency and is locked until the newly-added
// dependency reaches Deployed. The existing service continues to run at its current
// version on the managed cluster.
//
// In-flight upgrade — if the stored spec version for a service differs from its
// deployed version, the upgrade is already in progress. The service is emitted with
// its current in-flight version (mutable fields such as values are merged from the
// desired spec) and is not advanced to the next upgrade step until the in-flight
// version is fully deployed.
//
// No-op — if the resolved spec is identical to the existing ServiceSet spec,
// [kcmv1.ServiceSetOperationNone] is returned and no write is performed.
//
// New ServiceSet — if no ServiceSet exists at operationReq.ObjectKey, a new one is
// initialised and [kcmv1.ServiceSetOperationCreate] is returned.
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

	// Get ServiceSet and define the initial operation state.
	// Update by default, create if ServiceSet does not exist.
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

	filteredServices, err := ResolveServicesToApply(
		ctx, c, operationReq.SystemNamespace, operationReq.MCS, operationReq.CD, desiredServices, serviceSet)
	if err != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to resolve services to apply: %w", err)
	}
	l.V(1).Info("Resolved services to apply", "services", filteredServices)

	resultingServices := BuildServicesList(serviceSet.Spec.Services, filteredServices, desiredServices)
	l.V(1).Info("Services to deploy", "services", resultingServices)

	// Save current spec before Build() overwrites it in place
	// to compare spec and decide whether action is required.
	existingSpec := serviceSet.Spec

	candidate, err := NewBuilder(operationReq.CD, serviceSet, provider.Spec.Selector).
		WithMultiClusterService(operationReq.MCS).
		WithServicesToDeploy(resultingServices).Build()
	if err != nil {
		return nil, kcmv1.ServiceSetOperationNone, fmt.Errorf("failed to build ServiceSet: %w", err)
	}

	if op == kcmv1.ServiceSetOperationUpdate && equality.Semantic.DeepEqual(existingSpec, candidate.Spec) {
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
