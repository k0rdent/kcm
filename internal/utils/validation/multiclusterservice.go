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
		return fmt.Errorf("failed service dependency validation: %w", err)
	}

	if err := validateMCSDependencyCycle(mcs, mcsList); err != nil {
		return fmt.Errorf("failed service dependency cycle validation: %w", err)
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

	dependsOnMap := make(map[client.ObjectKey][]client.ObjectKey)
	for _, m := range mcsList.Items {
		k := client.ObjectKey{Name: m.GetName()}
		for _, d := range m.Spec.DependsOn {
			dependsOnMap[k] = append(dependsOnMap[k], client.ObjectKey{Name: d})
		}
	}

	var err error
	for _, d := range mcs.Spec.DependsOn {
		k := client.ObjectKey{Name: d}
		if _, ok := dependsOnMap[k]; !ok {
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

	mcsList.Items = append(mcsList.Items, *mcs)
	dependsOnMap := make(map[client.ObjectKey][]client.ObjectKey)
	for _, m := range mcsList.Items {
		k := client.ObjectKey{Name: m.GetName()}
		for _, d := range m.Spec.DependsOn {
			dependsOnMap[k] = append(dependsOnMap[k], client.ObjectKey{Name: d})
		}
	}

	return hasDependencyCycle(client.ObjectKey{Name: mcs.GetName()}, nil, dependsOnMap)
}
