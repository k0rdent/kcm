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
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
)

type ClusterAuthenticationValidator struct {
	client.Client
}

const invalidClusterAuthenticationMsg = "the ClusterAuthentication is invalid"

func (v *ClusterAuthenticationValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&kcmv1.ClusterAuthentication{}).
		WithValidator(v).
		Complete()
}

var _ webhook.CustomValidator = &ClusterAuthenticationValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (v *ClusterAuthenticationValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	clAuth, ok := obj.(*kcmv1.ClusterAuthentication)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected ClusterAuthentication but got a %T", obj))
	}

	if err := validationutil.ValidateClusterAuthentication(ctx, v.Client, clAuth); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterAuthenticationMsg, err)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (v *ClusterAuthenticationValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	newClAuth, ok := newObj.(*kcmv1.ClusterAuthentication)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected ClusterAuthentication but got a %T", newObj))
	}

	if err := validationutil.ValidateClusterAuthentication(ctx, v.Client, newClAuth); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterAuthenticationMsg, err)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *ClusterAuthenticationValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	clAuth, ok := obj.(*kcmv1.ClusterAuthentication)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected clusterAuthentication but got a %T", obj))
	}

	if err := validationutil.ClusterAuthenticationDeletionAllowed(ctx, v.Client, clAuth); err != nil {
		return nil, err
	}

	return nil, nil
}
