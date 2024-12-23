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

package providers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/go-logr/logr"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/K0rdent/kcm/internal/credspropagation"
)

// GVK represents the GroupVersionKind structure in YAML
type GVK struct {
	Group   string `yaml:"group"`
	Version string `yaml:"version"`
	Kind    string `yaml:"kind"`
}

// CredentialPropagation represents the credential propagation configuration
type CredentialPropagation struct {
	Lua     string `yaml:"lua"`
	Enabled bool   `yaml:"enabled"`
}

// YAMLProviderDefinition represents a YAML-based provider configuration
type YAMLProviderDefinition struct {
	Name                  string                `yaml:"name"`
	Title                 string                `yaml:"title"`
	ClusterGVK            GVK                   `yaml:"clusterGVK"`
	ClusterIdentityKinds  []string              `yaml:"clusterIdentityKinds"`
	CredentialPropagation CredentialPropagation `yaml:"credentialPropagation"`
}

var _ ProviderModule = (*YAMLProviderDefinition)(nil)

func (p *YAMLProviderDefinition) GetName() string {
	return p.Name
}

func (p *YAMLProviderDefinition) GetTitleName() string {
	return p.Title
}

func (p *YAMLProviderDefinition) GetClusterGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   p.ClusterGVK.Group,
		Version: p.ClusterGVK.Version,
		Kind:    p.ClusterGVK.Kind,
	}
}

func (p *YAMLProviderDefinition) GetClusterIdentityKinds() []string {
	return slices.Clone(p.ClusterIdentityKinds)
}

func (p *YAMLProviderDefinition) CredentialPropagationFunc() func(
	ctx context.Context,
	cfg *credspropagation.PropagationCfg,
	l logr.Logger,
) (enabled bool, err error) {
	return func(
		ctx context.Context,
		cfg *credspropagation.PropagationCfg,
		l logr.Logger,
	) (bool, error) {
		l.Info(p.GetTitleName() + " creds propagation start")
		return p.CredentialPropagation.Enabled,
			credspropagation.PropagateProviderObjects(ctx, cfg, p.CredentialPropagation.Lua)
	}
}

// RegisterFromYAML registers a provider from a YAML file
func RegisterFromYAML(yamlFile string) error {
	data, err := os.ReadFile(yamlFile)
	if err != nil {
		return fmt.Errorf("failed to read YAML file: %w", err)
	}

	var ypd YAMLProviderDefinition

	if err := yaml.Unmarshal(data, &ypd); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	Register(&ypd)

	return nil
}

// RegisterProvidersFromGlob loads and registers provider YAML files matching the glob pattern
func RegisterProvidersFromGlob(pattern string) error {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("failed to glob pattern %q: %w", pattern, err)
	}

	for _, file := range matches {
		if err := RegisterFromYAML(file); err != nil {
			return fmt.Errorf("provider %s: %w", filepath.Base(file), err)
		}
	}

	return nil
}
