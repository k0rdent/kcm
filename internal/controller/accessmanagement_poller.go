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

package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// accessManagementPollEnqueue is the [pollerutil.EnqueueFunc] driving the periodic
// re-reconciliation of the singleton AccessManagement/kcm object. No per-GVK watch is registered
// for dynamically-referenced Kinds (built-in or custom): the set of Kinds is unbounded and only
// known at runtime, so this poller is what picks up source object drift for those Kinds instead
// of a dedicated informer per Kind (see defaultPollInterval).
func (r *AccessManagementReconciler) accessManagementPollEnqueue(ctx context.Context) ([]*kcmv1.AccessManagement, error) {
	am := &kcmv1.AccessManagement{}
	if err := r.Get(ctx, client.ObjectKey{Name: kcmv1.AccessManagementName}, am); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get AccessManagement: %w", err)
	}

	return []*kcmv1.AccessManagement{am}, nil
}
