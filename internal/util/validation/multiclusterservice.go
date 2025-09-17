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

	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// ValidateMCSDependencyOverall calls all of the functions
// related to MultiClusterService dependency validation one by one.
func ValidateMCSDependencyOverall(ctx context.Context, c client.Client, mcs *kcmv1.MultiClusterService) error {
	mcsList := new(kcmv1.MultiClusterServiceList)
	if err := c.List(ctx, mcsList); err != nil {
		return fmt.Errorf("failed to list MultiClusterServices: %w", err)
	}

	if err := validateMCSDependency(mcs, mcsList); err != nil {
		return fmt.Errorf("failed MCS dependency validation: %w", err)
	}

	if err := validateMCSDependencyCycle(mcs, mcsList); err != nil {
		return fmt.Errorf("failed service dependency cycle validation: %w", err)
	}

	return nil
}

// ValidateMCSDelete validates if it is safe to delete provided MCS.
func ValidateMCSDelete(ctx context.Context, c client.Client, mcs *kcmv1.MultiClusterService) error {
	mcsList := new(kcmv1.MultiClusterServiceList)
	if err := c.List(ctx, mcsList); err != nil {
		return fmt.Errorf("failed to list MultiClusterServices: %w", err)
	}

	graph := generateReserveMCSDependencyGraph(mcsList)
	key := client.ObjectKey{Name: mcs.GetName()}

	dependents := graph[key]
	if len(dependents) > 0 {
		return fmt.Errorf("failed to delete MultiClusterService %s because %d other MultiClusterServices depend on it", key, len(dependents))
	}

	return nil
}

// validateMCSDependency validates if all dependencies of a MultiClusterService already exist.
func validateMCSDependency(mcs *kcmv1.MultiClusterService, mcsList *kcmv1.MultiClusterServiceList) error {
	if mcs == nil || len(mcs.Spec.DependsOn) == 0 {
		return nil
	}
	if mcsList == nil {
		mcsList = new(kcmv1.MultiClusterServiceList)
	}

	graph := generateMCSDependencyGraph(mcsList)

	var err error
	for _, d := range mcs.Spec.DependsOn {
		k := client.ObjectKey{Name: d}
		if _, ok := graph[k]; !ok {
			err = errors.Join(err, fmt.Errorf("dependency %s of %s is not defined", k, client.ObjectKeyFromObject(mcs)))
		}
	}

	return err
}

// validateServiceDependencyCycle validates if there is a cycle in the MultiClusterService dependency graph.
func validateMCSDependencyCycle(mcs *kcmv1.MultiClusterService, mcsList *kcmv1.MultiClusterServiceList) error {
	if mcs == nil || len(mcs.Spec.DependsOn) == 0 {
		return nil
	}
	if mcsList == nil {
		mcsList = new(kcmv1.MultiClusterServiceList)
	}

	// Provided mcs is our starting point to the dependency
	// graph so adding it to the list of MultiClusterServices.
	mcsList.Items = append(mcsList.Items, *mcs)
	graph := generateMCSDependencyGraph(mcsList)

	return hasDependencyCycle(client.ObjectKey{Name: mcs.GetName()}, nil, graph)
}

// generateMCSDependencyGraph returns a mapping of each MCS with the MCS it depends on as values.
func generateMCSDependencyGraph(mcsList *kcmv1.MultiClusterServiceList) map[client.ObjectKey][]client.ObjectKey {
	if mcsList == nil {
		return nil
	}

	graph := make(map[client.ObjectKey][]client.ObjectKey)
	for _, m := range mcsList.Items {
		k := client.ObjectKey{Name: m.GetName()}
		// Adding to the graph here so that every MCS object
		// exists as a key even if it has 0 dependents.
		graph[k] = nil
		for _, d := range m.Spec.DependsOn {
			graph[k] = append(graph[k], client.ObjectKey{Name: d})
		}
	}

	return graph
}

// generateReserveMCSDependencyGraph returns a mapping of each MCS with the MCS dependent on it as values.
func generateReserveMCSDependencyGraph(mcsList *kcmv1.MultiClusterServiceList) map[client.ObjectKey][]client.ObjectKey {
	if mcsList == nil {
		return nil
	}

	graph := make(map[client.ObjectKey][]client.ObjectKey)
	for _, m := range mcsList.Items {
		mkey := client.ObjectKey{Name: m.GetName()}
		// Adding to the graph here so that every mcs object exists
		// as a key even if it is not dependent on any other MCS.
		if _, ok := graph[mkey]; !ok {
			graph[mkey] = nil
		}

		for _, d := range m.Spec.DependsOn {
			dkey := client.ObjectKey{Name: d}
			graph[dkey] = append(graph[dkey], client.ObjectKey{Name: m.GetName()})
		}
	}

	return graph
}
