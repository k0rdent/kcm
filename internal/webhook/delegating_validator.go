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

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// delegatingValidator implements admission.Validator[T] by delegating to externally-supplied check
// functions: validate for create/update, deletionAllowed for delete.
type delegatingValidator[T client.Object] struct {
	client.Client

	validate        func(ctx context.Context, c client.Client, obj T) error
	deletionAllowed func(ctx context.Context, c client.Client, obj T) error
	invalidMsg      string
}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (v *delegatingValidator[T]) ValidateCreate(ctx context.Context, obj T) (admission.Warnings, error) {
	if err := v.validate(ctx, v.Client, obj); err != nil {
		return nil, fmt.Errorf("%s: %w", v.invalidMsg, err)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (v *delegatingValidator[T]) ValidateUpdate(ctx context.Context, _, newObj T) (admission.Warnings, error) {
	if err := v.validate(ctx, v.Client, newObj); err != nil {
		return nil, fmt.Errorf("%s: %w", v.invalidMsg, err)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *delegatingValidator[T]) ValidateDelete(ctx context.Context, obj T) (admission.Warnings, error) {
	if err := v.deletionAllowed(ctx, v.Client, obj); err != nil {
		return nil, err
	}

	return nil, nil
}
