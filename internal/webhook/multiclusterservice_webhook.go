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

package webhook

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
)

type MultiClusterServiceValidator struct{}

const invalidMultiClusterServiceMsg = "the MultiClusterService is invalid"

func (v *MultiClusterServiceValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &kcmv1.MultiClusterService{}).
		WithValidator(v).
		Complete()
}

var _ admission.Validator[*kcmv1.MultiClusterService] = &MultiClusterServiceValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (*MultiClusterServiceValidator) ValidateCreate(_ context.Context, mcs *kcmv1.MultiClusterService) (admission.Warnings, error) {
	if err := validationutil.MultiClusterServiceReload(mcs); err != nil {
		return admission.Warnings{"Failed to validate reload for provided MultiClusterService"}, fmt.Errorf("%s: %w", invalidMultiClusterServiceMsg, err)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
//
// Mirrors ClusterDeploymentValidator.ValidateUpdate's reload handling: reject only a reload
// request newly introduced by this update, and skip the check entirely once the object is
// being deleted. Otherwise a MultiClusterService that already requested the unsupported
// reload feature before this webhook existed would become stuck - every future update to
// it, including the controller's own finalizer removal, would be rejected.
func (*MultiClusterServiceValidator) ValidateUpdate(_ context.Context, oldMCS, newMCS *kcmv1.MultiClusterService) (admission.Warnings, error) {
	if newMCS.DeletionTimestamp.IsZero() {
		if newErr := validationutil.MultiClusterServiceReload(newMCS); newErr != nil {
			if oldErr := validationutil.MultiClusterServiceReload(oldMCS); oldErr == nil {
				return admission.Warnings{"Failed to validate reload for provided MultiClusterService"}, fmt.Errorf("%s: %w", invalidMultiClusterServiceMsg, newErr)
			}
		}
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (*MultiClusterServiceValidator) ValidateDelete(context.Context, *kcmv1.MultiClusterService) (admission.Warnings, error) {
	return nil, nil
}
