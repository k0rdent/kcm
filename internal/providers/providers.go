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
	"fmt"
	"slices"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
)

const (
	// InfraPrefix is the prefix used for infrastructure provider names
	InfraPrefix = "infrastructure-"
	// ProviderPrefix is the prefix used for cluster API provider names
	ProviderPrefix = "cluster-api-provider-"
)

var (
	mu sync.RWMutex

	providers = []kcm.Provider{
		{
			Name: kcm.ProviderK0smotronName,
		},
		{
			Name: kcm.ProviderSveltosName,
		},
	}

	registry map[string]ProviderModule
)

type ProviderModule interface {
	// GetName returns the short name of the provider
	GetName() string
	// GetClusterGVK returns the GroupVersionKind for the provider's cluster resource
	GetClusterGVK() schema.GroupVersionKind
	// GetClusterIdentityKinds returns a list of supported cluster identity kinds
	GetClusterIdentityKinds() []string
}

// Register adds a new provider module to the registry
func Register(p ProviderModule) {
	mu.Lock()
	defer mu.Unlock()

	if registry == nil {
		registry = make(map[string]ProviderModule)
	}

	shortName := p.GetName()

	if _, exists := registry[shortName]; exists {
		panic(fmt.Sprintf("provider %q already registered", shortName))
	}

	providers = append(providers,
		kcm.Provider{
			Name: ProviderPrefix + p.GetName(),
		},
	)

	registry[shortName] = p
}

// List returns a copy of all registered providers
func List() []kcm.Provider {
	return slices.Clone(providers)
}

// GetClusterGVK returns the GroupVersionKind for a provider's cluster resource
func GetClusterGVK(shortName string) schema.GroupVersionKind {
	mu.RLock()
	defer mu.RUnlock()

	module, ok := registry[shortName]
	if !ok {
		return schema.GroupVersionKind{}
	}

	return module.GetClusterGVK()
}

// GetClusterIdentityKinds returns the supported identity kinds for a given infrastructure provider
func GetClusterIdentityKinds(infraName string) ([]string, bool) {
	mu.RLock()
	defer mu.RUnlock()

	shortName := strings.TrimPrefix(infraName, InfraPrefix)

	module, ok := registry[shortName]
	if !ok {
		return nil, false
	}

	list := slices.Clone(module.GetClusterIdentityKinds())

	return list, len(list) > 0
}
