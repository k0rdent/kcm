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

package aws

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/K0rdent/kcm/pkg/credspropagation"
)

// Tier-1 provider, always registered.

type Provider struct{}

func (*Provider) GetName() string {
	return "aws"
}

func (*Provider) GetTitleName() string {
	return "AWS"
}

func (*Provider) GetClusterGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   "infrastructure.cluster.x-k8s.io",
		Version: "v1beta2",
		Kind:    "AWSCluster",
	}
}

func (*Provider) GetClusterIdentityKinds() []string {
	return []string{"AWSClusterStaticIdentity", "AWSClusterRoleIdentity", "AWSClusterControllerIdentity"}
}

func (p *Provider) CredentialPropagationFunc() func(
	ctx context.Context,
	propnCfg *credspropagation.PropagationCfg,
	l logr.Logger,
) (enabled bool, err error) {
	return func(
		_ context.Context,
		_ *credspropagation.PropagationCfg,
		l logr.Logger,
	) (enabled bool, err error) {
		l.Info("Skipping creds propagation for " + p.GetTitleName())
		return enabled, err
	}
}
