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
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/K0rdent/kcm/api/v1alpha1"
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
		For(&v1alpha1.MultiClusterService{}).
		WithValidator(v).
		Complete()
}

var _ webhook.CustomValidator = &MultiClusterServiceValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (v *MultiClusterServiceValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	mcs, ok := obj.(*v1alpha1.MultiClusterService)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected MultiClusterService but got a %T", obj))
	}

	if err := validateServices(ctx, v.Client, v.SystemNamespace, mcs.Spec.ServiceSpec.Services); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidMultiClusterServiceMsg, err)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (v *MultiClusterServiceValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	mcs, ok := newObj.(*v1alpha1.MultiClusterService)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected MultiClusterService but got a %T", newObj))
	}

	if err := validateServices(ctx, v.Client, v.SystemNamespace, mcs.Spec.ServiceSpec.Services); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidMultiClusterServiceMsg, err)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (*MultiClusterServiceValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func getServiceTemplate(ctx context.Context, c client.Client, templateNamespace, templateName string) (tpl *v1alpha1.ServiceTemplate, err error) {
	tpl = new(v1alpha1.ServiceTemplate)
	return tpl, c.Get(ctx, client.ObjectKey{Namespace: templateNamespace, Name: templateName}, tpl)
}

func validateServices(ctx context.Context, c client.Client, namespace string, services []v1alpha1.Service) (errs error) {
	for _, svc := range services {
		tpl, err := getServiceTemplate(ctx, c, namespace, svc.Template)
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}

		errs = errors.Join(errs, isTemplateValid(tpl.GetCommonStatus()))
	}

	return errs
}
