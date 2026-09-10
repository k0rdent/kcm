// Copyright 2026
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

	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// deletionAllowedIfUnreferenced returns an error if any ClusterDeployment in obj's namespace
// references obj by name via indexKey, since deleting obj would leave those ClusterDeployments
// pointing at a missing object. kind is used only for the error message.
func deletionAllowedIfUnreferenced(ctx context.Context, mgmtClient client.Client, obj client.Object, indexKey, kind string) error {
	key := client.ObjectKeyFromObject(obj)

	clds := new(kcmv1.ClusterDeploymentList)
	if err := mgmtClient.List(ctx, clds,
		client.MatchingFields{indexKey: obj.GetName()},
		client.InNamespace(obj.GetNamespace()),
		client.Limit(1),
	); err != nil {
		return fmt.Errorf("failed to list ClusterDeployments referencing %s %s: %w", kind, key, err)
	}

	if len(clds.Items) > 0 {
		return fmt.Errorf("cannot delete %s %s: it is still referenced by one or more ClusterDeployments", kind, key)
	}

	return nil
}
