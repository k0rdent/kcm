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
func ValidateMCSDependencyOverall(ctx context.Context, c client.Client, mcs kcmv1.MultiClusterServiceCommon) error {
	if kcmv1.IsMCSNil(mcs) {
		return nil
	}

	mcsList, err := fetchMCSCommon(ctx, c, mcs)
	if err != nil {
		return err
	}

	if err := validateMCSDependency(mcs, mcsList); err != nil {
		return fmt.Errorf("failed MCS dependency validation: %w", err)
	}
	if err := validateMCSDependencyCycle(mcs, mcsList); err != nil {
		return fmt.Errorf("failed MCS dependency cycle validation: %w", err)
	}

	return nil
}

// ValidateMCSDelete validates if it is safe to delete provided MCS.
func ValidateMCSDelete(ctx context.Context, c client.Client, mcs kcmv1.MultiClusterServiceCommon) error {
	if kcmv1.IsMCSNil(mcs) {
		return nil
	}

	mcsList, err := fetchMCSCommon(ctx, c, mcs)
	if err != nil {
		return err
	}

	graph := generateReverseMCSDependencyGraph(mcsList)
	// We are deliberately ignoring namespace because `.spec.dependsOn` only contains name.
	key := client.ObjectKey{Name: mcs.GetName()}

	dependents := graph[key]
	if len(dependents) > 0 {
		return fmt.Errorf("failed to delete MultiClusterService/NamespacedMultiClusterService %s because %d other MultiClusterServices/NamespacedMultiClusterServices depend on it", mcs.GetFullname(), len(dependents))
	}

	return nil
}

// fetchMCSCommon returns a list of either:
// 1. All NamespacedMultiClusterServices within the same namespace if provided mcs is namespaced.
// 2. All MultiClusterServices within the cluster if the provided mcs is global.
func fetchMCSCommon(ctx context.Context, c client.Client, mcs kcmv1.MultiClusterServiceCommon) ([]kcmv1.MultiClusterServiceCommon, error) {
	if _, ok := mcs.(*kcmv1.NamespacedMultiClusterService); ok {
		list := new(kcmv1.NamespacedMultiClusterServiceList)
		if err := c.List(ctx, list, client.InNamespace(mcs.GetNamespace())); err != nil {
			return nil, fmt.Errorf("failed to list NamespacedMultiClusterServices: %w", err)
		}

		mcsList := make([]kcmv1.MultiClusterServiceCommon, len(list.Items))
		for i := range list.Items {
			mcsList[i] = &list.Items[i]
		}

		return mcsList, nil
	}

	list := new(kcmv1.MultiClusterServiceList)
	if err := c.List(ctx, list); err != nil {
		return nil, fmt.Errorf("failed to list MultiClusterServices: %w", err)
	}

	mcsList := make([]kcmv1.MultiClusterServiceCommon, len(list.Items))
	for i := range list.Items {
		mcsList[i] = &list.Items[i]
	}

	return mcsList, nil
}

// validateMCSDependency validates if all dependencies of a MultiClusterService already exist.
func validateMCSDependency(mcs kcmv1.MultiClusterServiceCommon, mcsList []kcmv1.MultiClusterServiceCommon) error {
	if kcmv1.IsMCSNil(mcs) {
		return nil
	}
	spec := mcs.GetMultiClusterServiceSpec()
	if len(spec.DependsOn) == 0 {
		return nil
	}

	graph := generateMCSDependencyGraph(mcsList)

	var err error
	for _, d := range spec.DependsOn {
		if _, ok := graph[client.ObjectKey{Name: d}]; ok {
			continue
		}

		// The graph keys deliberately omit the namespace, but a dependency of a
		// NamespacedMultiClusterService always lives in that same namespace, so
		// qualify it here to keep the message unambiguous.
		dep := formatObjectKeyForMessage(client.ObjectKey{Namespace: mcs.GetNamespace(), Name: d})
		err = errors.Join(err, fmt.Errorf("dependency %s of %s is not defined", dep, mcs.GetFullname()))
	}

	return err
}

// formatObjectKey renders k for user-facing messages. A cluster-scoped
// MultiClusterService has no namespace, and client.ObjectKey.String()
// unconditionally joins on "/", which would render such a key as "/name".
func formatObjectKeyForMessage(k client.ObjectKey) string {
	if k.Namespace == "" {
		return k.Name
	}

	return k.String()
}

// validateServiceDependencyCycle validates if there is a cycle in the MultiClusterService dependency graph.
func validateMCSDependencyCycle(mcs kcmv1.MultiClusterServiceCommon, mcsList []kcmv1.MultiClusterServiceCommon) error {
	if kcmv1.IsMCSNil(mcs) {
		return nil
	}
	spec := mcs.GetMultiClusterServiceSpec()
	if len(spec.DependsOn) == 0 {
		return nil
	}

	if mcsList == nil {
		mcsList = make([]kcmv1.MultiClusterServiceCommon, 0)
	}

	// Provided mcs is our starting point to the dependency
	// graph so adding it to the list of MultiClusterServices.
	mcsList = append(mcsList, mcs)
	graph := generateMCSDependencyGraph(mcsList)

	// We only want to look for a cycle in the MCS dependency sub-graph starting at the current MCS as opposed to
	// looking for the cycle in the entire MCS dependency graph which may contain MCS unrelated to the current MCS.
	// For example with the validation webhooks disabled:
	// 1. The user creates MCS B and C depending on each other C<->B.
	// 2. Both B and C will not reconcile & will have failed validation in their statuses.
	// 3. Now user creates A and D and makes A dependsOn D and B depends on A, so C<->B->A->D.
	// 4. Now even though the entire graph has a cycle (C<->B), we don't want to stop reconciliation
	// for A & D because there is no cycle in the subgraph occupied by A & D.
	// 5. So when A & D are created, the starting node to hasCycleFrom will be A & D respectively and
	// therefore it will only validate the subgraph of A & D respectively and not return error in either case.
	return hasCycleFrom(client.ObjectKey{Name: mcs.GetName()}, graph)
}

// generateMCSDependencyGraph returns a mapping of each MCS with the MCS it depends on as values.
func generateMCSDependencyGraph(mcsList []kcmv1.MultiClusterServiceCommon) map[client.ObjectKey][]client.ObjectKey {
	graph := make(map[client.ObjectKey][]client.ObjectKey)
	if mcsList == nil {
		return graph
	}

	for _, m := range mcsList {
		// We ignore Namespace when creating key because `.spec.dependsOn` only contains name,
		// which is fine because even for a NamespacedMultiClusterService we only need the name
		// of the other NamespacedMultiClusterServices in depends on because it is understood that
		// they will exist within the same namespace.
		k := client.ObjectKey{Name: m.GetName()}
		// Adding to the graph here so that every MCS object
		// exists as a key even if it has 0 dependents.
		graph[k] = nil
		for _, d := range m.GetMultiClusterServiceSpec().DependsOn {
			graph[k] = append(graph[k], client.ObjectKey{Name: d})
		}
	}

	return graph
}

// generateReverseMCSDependencyGraph returns a mapping of each MCS with the MCS dependent on it as values.
func generateReverseMCSDependencyGraph(mcsList []kcmv1.MultiClusterServiceCommon) map[client.ObjectKey][]client.ObjectKey {
	graph := make(map[client.ObjectKey][]client.ObjectKey)

	if mcsList == nil {
		return graph
	}

	for _, m := range mcsList {
		// We are deliberately ignoring namespace because `.spec.dependsOn` only contains name.
		mkey := client.ObjectKey{Name: m.GetName()}
		// Adding to the graph here so that every mcs object exists
		// as a key even if it is not dependent on any other MCS.
		if _, ok := graph[mkey]; !ok {
			graph[mkey] = nil
		}

		for _, d := range m.GetMultiClusterServiceSpec().DependsOn {
			dkey := client.ObjectKey{Name: d}
			graph[dkey] = append(graph[dkey], client.ObjectKey{Name: m.GetName()})
		}
	}

	return graph
}

// hasCycleFrom uses DFS to check for cycles in only the portion of the
// graph starting from start and returns on the first occurrence of a cycle.
func hasCycleFrom(start client.ObjectKey, graph map[client.ObjectKey][]client.ObjectKey) error {
	visited := make(map[client.ObjectKey]bool)
	inStack := make(map[client.ObjectKey]bool)

	return dfs(start, visited, inStack, graph)
}
