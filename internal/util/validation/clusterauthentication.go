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
	"fmt"

	apiservervalidation "k8s.io/apiserver/pkg/apis/apiserver/validation"
	authenticationcel "k8s.io/apiserver/pkg/authentication/cel"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	clusterauth "github.com/K0rdent/kcm/internal/authentication"
)

func ClusterAuthenticationValid(ctx context.Context, mgmtClient client.Client, clAuth *kcmv1.ClusterAuthentication) error {
	authConf, err := clusterauth.GetAuthenticationConfiguration(ctx, mgmtClient, clAuth)
	if err != nil {
		return err // already wrapped
	}

	apiServerAuthConf, err := authConf.ToAPIServerAuthConfig()
	if err != nil {
		return fmt.Errorf("failed to convert auth config: %w", err)
	}

	if err := apiservervalidation.ValidateAuthenticationConfiguration(authenticationcel.NewDefaultCompiler(), apiServerAuthConf, []string{}).ToAggregate(); err != nil {
		return fmt.Errorf("invalid AuthenticationConfiguration provided: %w", err)
	}
	return nil
}

func ClusterAuthenticationDeletionAllowed(ctx context.Context, mgmtClient client.Client, clAuth *kcmv1.ClusterAuthentication) error {
	clds := &kcmv1.ClusterDeploymentList{}
	err := mgmtClient.List(ctx, clds,
		client.MatchingFields{kcmv1.ClusterDeploymentAuthenticationIndexKey: clAuth.Name},
		client.InNamespace(clAuth.Namespace),
		client.Limit(1),
	)
	key := client.ObjectKeyFromObject(clAuth)
	if err != nil {
		return fmt.Errorf("failed to list ClusterDeployments referencing ClusterAuthentication %s: %w", key, err)
	}
	if len(clds.Items) > 0 {
		return fmt.Errorf("cannot delete ClusterAuthentication %s: it is still referenced by one or more ClusterDeployments", key)
	}
	return nil
}
