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

package vsphere

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/K0rdent/kcm/pkg/credspropagation"
	"github.com/K0rdent/kcm/pkg/providers"
)

type Provider struct{}

var _ providers.ProviderModule = (*Provider)(nil)

func init() {
	providers.Register(&Provider{})
}

func (*Provider) GetName() string {
	return "vsphere"
}

func (*Provider) GetTitleName() string {
	return "vSphere"
}

func (*Provider) GetClusterGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{}
}

func (*Provider) GetClusterIdentityKinds() []string {
	return []string{"VSphereClusterIdentity"}
}

func (p *Provider) CredentialPropagationFunc() func(
	ctx context.Context,
	propnCfg *credspropagation.PropagationCfg,
	l logr.Logger,
) (enabled bool, err error) {
	return func(
		ctx context.Context,
		propnCfg *credspropagation.PropagationCfg,
		l logr.Logger,
	) (enabled bool, err error) {
		l.Info(p.GetTitleName() + " creds propagation start")
		enabled, err = true, PropagateSecrets(ctx, propnCfg)
		return enabled, err
	}
}
