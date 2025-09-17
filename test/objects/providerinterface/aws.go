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

package providerinterface

import (
	"slices"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func NewAWSProviderInterface(opts ...Opt) *kcmv1.ProviderInterface {
	p := &kcmv1.ProviderInterface{}

	preOpts := []Opt{
		WithName("aws"),
		WithLabel(kcmv1.FluxHelmChartNameKey, "cluster-api-provider-aws"),
		WithKCMComponentLabel(),
		WithClusterGVKs(
			kcmv1.GroupVersionKind{
				Group:   "infrastructure.cluster.x-k8s.io",
				Version: "v1beta2",
				Kind:    "AWSCluster",
			},
			kcmv1.GroupVersionKind{
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
