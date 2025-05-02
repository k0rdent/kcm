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

package v1beta1

import (
	"fmt"
	"slices"
)

// TemplateChainSpec defines the desired state of *TemplateChain
type TemplateChainSpec struct {
	// +patchMergeKey=name
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=name

	// SupportedTemplates is the list of supported Templates definitions and all available upgrade sequences for it.
	SupportedTemplates []SupportedTemplate `json:"supportedTemplates,omitempty"`
}

// TemplateChainStatus defines the observed state of *TemplateChain
type TemplateChainStatus struct {
	// ValidationError provides information regarding issues encountered during templatechain validation.
	ValidationError string `json:"validationError,omitempty"`
	// Valid indicates whether the chain is valid and can be considered when calculating available
	// upgrade paths.
	Valid bool `json:"valid,omitempty"`
}

// SupportedTemplate is the supported Template definition and all available upgrade sequences for it
type SupportedTemplate struct {
	// Name is the name of the Template.
	Name string `json:"name"`
	// AvailableUpgrades is the list of available upgrades for the specified Template.
	AvailableUpgrades []AvailableUpgrade `json:"availableUpgrades,omitempty"`
}

// AvailableUpgrade is the definition of the available upgrade for the Template
type AvailableUpgrade struct {
	// Name is the name of the Template to which the upgrade is available.
	Name string `json:"name"`
}

// IsValid checks if the [TemplateChainSpec] is valid, otherwise provides warning messages.
func (s *TemplateChainSpec) IsValid() (warnings []string, ok bool) {
	supportedTemplates := make(map[string]struct{}, len(s.SupportedTemplates))
	availableForUpgrade := make(map[string]struct{}, len(s.SupportedTemplates))
	for _, supportedTemplate := range s.SupportedTemplates {
		supportedTemplates[supportedTemplate.Name] = struct{}{}
		for _, template := range supportedTemplate.AvailableUpgrades {
			availableForUpgrade[template.Name] = struct{}{}
		}
	}

	for template := range availableForUpgrade {
		if _, ok := supportedTemplates[template]; !ok {
			warnings = append(warnings, fmt.Sprintf("template %s is allowed for upgrade but is not present in the list of '.spec.supportedTemplates'", template))
		}
	}

	if len(warnings) > 0 {
		slices.Sort(warnings)
	}

	return warnings, len(warnings) == 0
}

// findAllUpgradePaths returns all possible upgrade paths from the given template
func (s *TemplateChainSpec) findAllUpgradePaths(templateName string) ([][]string, error) {
	// Build a map for lookup of supported templates by name
	templateMap := make(map[string]SupportedTemplate)
	for _, template := range s.SupportedTemplates {
		templateMap[template.Name] = template
	}

	// Check if the starting template exists
	_, exists := templateMap[templateName]
	if !exists {
		return nil, fmt.Errorf("template %s not found", templateName)
	}

	var (
		result    [][]string
		findPaths func(current string, path []string, visited map[string]bool)
	)
	visited := make(map[string]bool)
	findPaths = func(current string, path []string, visited map[string]bool) {
		// Skip if we've already visited this template in the current path
		if visited[current] {
			return
		}

		// Mark as visited for this path
		visited[current] = true
		defer func() { visited[current] = false }()

		template, exists := templateMap[current]
		if !exists {
			return
		}

		// Iterate through available upgrades and find subsequent available upgrades
		for _, upgrade := range template.AvailableUpgrades {
			upgradePath := make([]string, len(path))
			copy(upgradePath, path)
			upgradePath = append(upgradePath, upgrade.Name)
			result = append(result, upgradePath)
			findPaths(upgrade.Name, upgradePath, visited)
		}
	}

	findPaths(templateName, []string{}, visited)
	return result, nil
}

// UpgradePaths returns shortest upgrade paths for the given template.
func (s *TemplateChainSpec) UpgradePaths(templateName string) ([]UpgradePath, error) {
	allPaths, err := s.findAllUpgradePaths(templateName)
	if err != nil {
		return nil, err
	}

	uniquePaths := make(map[string][]string)
	// Filter out duplicate paths and ensure we have all unique paths
	for _, path := range allPaths {
		if len(path) == 0 {
			continue
		}

		// Use the last element as the key to ensure we have paths to all possible destinations
		key := path[len(path)-1]

		// If we haven't seen this destination or this path is shorter
		existingPath, exists := uniquePaths[key]
		if !exists || len(path) < len(existingPath) {
			uniquePaths[key] = path
		}
	}

	// Convert map back to slice
	result := make([]UpgradePath, 0, len(uniquePaths))
	for _, path := range uniquePaths {
		result = append(result, UpgradePath{Versions: path})
	}

	return result, nil
}
