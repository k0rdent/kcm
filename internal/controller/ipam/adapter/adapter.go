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

package adapter

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const ClusterDeploymentConfigKeyName = "ipPool"

type IPAMAdapter interface {
	BindAddress(ctx context.Context, config IPAMConfig, c client.Client) (kcmv1.ClusterIPAMProviderData, error)
}

type IPAMConfig struct {
	ClusterIPAMClaim *kcmv1.ClusterIPAMClaim
}

func Builder(name string) (IPAMAdapter, error) {
	switch name {
	case kcmv1.InClusterProviderName:
		return NewInClusterAdapter(), nil
	case kcmv1.InfobloxProviderName:
		return NewInfobloxAdapter(), nil
	default:
		return nil, fmt.Errorf("unknown provider name '%s'", name)
	}
}
