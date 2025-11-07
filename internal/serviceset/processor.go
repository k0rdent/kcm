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

package serviceset

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// Processor is used to process ServiceSet objects.
type Processor struct {
	client.Client

	ProviderSpec kcmv1.StateManagementProviderConfig

	Services []kcmv1.Service
}

// NewProcessor creates a new Processor with the given client.
func NewProcessor(cl client.Client) *Processor {
	return &Processor{
		Client: cl,
	}
}

// CreateOrUpdateServiceSet manages the lifecycle of a ServiceSet based on the specified operation (create, update, or no-op).
// It attempts to create or update the given ServiceSet object and handles errors such as conflicts or already exists.
// Returns a requeue flag to indicate if the parent object should be requeued and an error if the operation fails.
func (p *Processor) CreateOrUpdateServiceSet(
	ctx context.Context,
	op kcmv1.ServiceSetOperation,
	serviceSet *kcmv1.ServiceSet,
) error {
	l := ctrl.LoggerFrom(ctx)
	serviceSetObjectKey := client.ObjectKeyFromObject(serviceSet)
	switch op {
	case kcmv1.ServiceSetOperationCreate:
		l.V(1).Info("creating ServiceSet", "namespaced_name", serviceSetObjectKey)
		err := p.Create(ctx, serviceSet)
		// we'll return an error in case ServiceSet creation fails due to any
		// error except AlreadyExists
		if client.IgnoreAlreadyExists(err) != nil {
			return fmt.Errorf("failed to create ServiceSet %s: %w", serviceSetObjectKey, err)
		}
		if apierrors.IsAlreadyExists(err) {
			// no-op, controllers reconciling [github.com/k0rdent/kcm/api/v1beta1.ClusterDeployment]
			// and [github.com/k0rdent/kcm/api/v1beta1.MultiClusterService] are watching for
			// [github.com/k0rdent/kcm/api/v1beta1.ServiceSet] hence corresponding objects
			// will be requeued on ServiceSet events occur.
			return nil
		}
		l.V(1).Info("Successfully created ServiceSet", "namespaced_name", serviceSetObjectKey)
		return nil
	case kcmv1.ServiceSetOperationUpdate:
		l.V(1).Info("updating ServiceSet", "namespaced_name", serviceSetObjectKey)
		err := p.Update(ctx, serviceSet)
		// we'll requeue if ServiceSet update fails due to a conflict
		if apierrors.IsConflict(err) {
			return nil
		}
		// otherwise we'll return the error if any occurred
		if err != nil {
			return fmt.Errorf("failed to update ServiceSet %s: %w", serviceSetObjectKey, err)
		}
		l.V(1).Info("Successfully updated ServiceSet", "namespaced_name", serviceSetObjectKey)
		return nil
	case kcmv1.ServiceSetOperationNone:
		// no-op
	}
	return nil
}
