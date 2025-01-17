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

type ProviderOpenStack struct{}

var _ ProviderModule = (*ProviderOpenStack)(nil)

func init() {
	Register(&ProviderOpenStack{})
}

func (*ProviderOpenStack) GetName() string {
	return "openstack"
}

func (*ProviderOpenStack) GetTitleName() string {
	return "OpenStack"
}

func (*ProviderOpenStack) GetClusterGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{}
}

func (*ProviderOpenStack) GetClusterIdentityKinds() []string {
	return []string{"Secret"}
}

func (*ProviderOpenStack) CredentialPropagationFunc() func(
	_ context.Context,
	_ *credspropagation.PropagationCfg,
	_ logr.Logger,
) (enabled bool, err error) {
	return func(
		_ context.Context,
		_ *credspropagation.PropagationCfg,
		_ logr.Logger,
	) (enabled bool, err error) {
		return enabled, err
	}
}
