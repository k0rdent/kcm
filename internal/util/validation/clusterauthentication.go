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

package validation

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apiserver/pkg/apis/apiserver"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	apiservervalidation "k8s.io/apiserver/pkg/apis/apiserver/validation"
	authenticationcel "k8s.io/apiserver/pkg/authentication/cel"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	authutil "github.com/K0rdent/kcm/internal/util/auth"
)

func ValidateClusterAuthentication(ctx context.Context, mgmtClient client.Client, clAuth *kcmv1.ClusterAuthentication) error {
	authConf, err := authutil.GetAuthenticationConfiguration(ctx, mgmtClient, clAuth)
	if err != nil {
		return fmt.Errorf("failed to get AuthenticationConfiguration: %w", err)
	}

	apiServerAuthConf, err := toAPIServerAuthConfig(authConf)
	if err != nil {
		return fmt.Errorf("failed to convert auth config: %w", err)
	}

	if err := apiservervalidation.ValidateAuthenticationConfiguration(authenticationcel.NewDefaultCompiler(), apiServerAuthConf, []string{}).ToAggregate(); err != nil {
		return fmt.Errorf("invalid AuthenticationConfiguration provided: %w", err)
	}
	return nil
}

func toAPIServerAuthConfig(authConf *apiserverv1.AuthenticationConfiguration) (*apiserver.AuthenticationConfiguration, error) {
	if authConf == nil {
		return &apiserver.AuthenticationConfiguration{}, nil
	}

	outBytes, err := json.Marshal(authConf)
	if err != nil {
		return nil, fmt.Errorf("error marshaling auth config to JSON: %w", err)
	}

	apiserverAuthConfig := &apiserver.AuthenticationConfiguration{}
	if err := json.Unmarshal(outBytes, apiserverAuthConfig); err != nil {
		return nil, fmt.Errorf("error unmarshalling auth config JSON to apiserver auth config: %w", err)
	}

	return apiserverAuthConfig, nil
}

func ClusterAuthenticationDeletionAllowed(ctx context.Context, mgmtClient client.Client, clAuth *kcmv1.ClusterAuthentication) error {
	return deletionAllowedIfUnreferenced(ctx, mgmtClient, clAuth, kcmv1.ClusterDeploymentAuthenticationIndexKey, "ClusterAuthentication")
}
