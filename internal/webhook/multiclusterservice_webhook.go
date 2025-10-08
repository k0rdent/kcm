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

package webhook

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/utils/validation"
)

type MultiClusterServiceValidator struct {
	client.Client
	SystemNamespace string
}

const invalidMultiClusterServiceMsg = "the MultiClusterService is invalid"

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (v *MultiClusterServiceValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&kcmv1.MultiClusterService{}).
		WithValidator(v).
		Complete()
}

var _ webhook.CustomValidator = &MultiClusterServiceValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (v *MultiClusterServiceValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	mcs, ok := obj.(*kcmv1.MultiClusterService)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected MultiClusterService but got a %T", obj))
	}

	if err := validation.ServicesHaveValidTemplates(ctx, v.Client, mcs.Spec.ServiceSpec.Services, v.SystemNamespace); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidMultiClusterServiceMsg, err)
	}

	if err := validation.ValidateServiceDependencyOverall(mcs.Spec.ServiceSpec.Services); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidMultiClusterServiceMsg, err)
	}

	if err := validation.ValidateMCSDependencyOverall(ctx, v.Client, mcs); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidMultiClusterServiceMsg, err)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (v *MultiClusterServiceValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	mcs, ok := newObj.(*kcmv1.MultiClusterService)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected MultiClusterService but got a %T", newObj))
	}

	if err := validation.ServicesHaveValidTemplates(ctx, v.Client, mcs.Spec.ServiceSpec.Services, v.SystemNamespace); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidMultiClusterServiceMsg, err)
	}

	if err := validation.ValidateServiceDependencyOverall(mcs.Spec.ServiceSpec.Services); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidMultiClusterServiceMsg, err)
	}

	if err := validation.ValidateMCSDependencyOverall(ctx, v.Client, mcs); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidMultiClusterServiceMsg, err)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *MultiClusterServiceValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	mcs, ok := obj.(*kcmv1.MultiClusterService)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected MultiClusterService but got a %T", obj))
	}

	if err := validation.ValidateMCSDelete(ctx, v.Client, mcs); err != nil {
		return nil, err
	}

	return nil, nil
}
