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

package pluggableprovider

import (
	"slices"

	"github.com/K0rdent/kcm/api/v1alpha1"
)

func NewAWSPluggableProvider(opts ...Opt) *v1alpha1.PluggableProvider {
	p := &v1alpha1.PluggableProvider{}

	preOpts := []Opt{
		WithName("aws"),
		WithKCMComponentLabel(),
		WithClusterGVKs(
			v1alpha1.GroupVersionKind{
				Group:   "infrastructure.cluster.x-k8s.io",
				Version: "v1beta2",
				Kind:    "AWSCluster",
			},
			v1alpha1.GroupVersionKind{
				Group:   "infrastructure.cluster.x-k8s.io",
				Version: "v1beta2",
				Kind:    "AWSManagedCluster",
			},
		),
		WithClusterIdentityKinds(
			"AWSClusterStaticIdentity",
			"AWSClusterRoleIdentity",
			"AWSClusterControllerIdentity",
		),
	}

	newOpts := slices.Concat(preOpts, opts)

	for _, opt := range newOpts {
		opt(p)
	}

	return p
}
