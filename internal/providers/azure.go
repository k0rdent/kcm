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

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/K0rdent/kcm/internal/credspropagation"
)

type ProviderAzure struct{}

var _ ProviderModule = (*ProviderAzure)(nil)

func init() {
	Register(&ProviderAzure{})
}

func (*ProviderAzure) GetName() string {
	return "azure"
}

func (*ProviderAzure) GetTitleName() string {
	return "Azure"
}

func (*ProviderAzure) GetClusterGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{}
}

func (*ProviderAzure) GetClusterIdentityKinds() []string {
	return []string{"AzureClusterIdentity"}
}

func (p *ProviderAzure) CredentialPropagationFunc() func(
	ctx context.Context,
	cfg *credspropagation.PropagationCfg,
	l logr.Logger,
) (enabled bool, err error) {
	return func(
		ctx context.Context,
		cfg *credspropagation.PropagationCfg,
		l logr.Logger,
	) (enabled bool, err error) {
		l.Info(p.GetTitleName() + " creds propagation start")
		enabled, err = true, credspropagation.PropagateAzureProviderObjects(ctx, cfg)
		return enabled, err
	}
}
