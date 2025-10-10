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
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
)

type RegionValidator struct {
	client.Client

	SystemNamespace string
}

var errRegionDeletionForbidden = errors.New("region deletion is forbidden")

func (v *RegionValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&kcmv1.Region{}).
		WithValidator(v).
		Complete()
}

var _ webhook.CustomValidator = &RegionValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (v *RegionValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	rgn, ok := obj.(*kcmv1.Region)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected Region but got a %T", obj))
	}
	return nil, validationutil.RegionClusterReference(ctx, v.Client, v.SystemNamespace, rgn)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (v *RegionValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	newRegion, ok := newObj.(*kcmv1.Region)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected Region but got a %T", newObj))
	}
	if !newRegion.DeletionTimestamp.IsZero() {
		return nil, nil
	}

	oldRegion, ok := oldObj.(*kcmv1.Region)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected Region but got a %T", oldObj))
	}

	mgmt := &kcmv1.Management{}
	if err := v.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, mgmt); err != nil {
		return nil, fmt.Errorf("failed to get Management: %w", err)
	}

	release := &kcmv1.Release{}
	if err := v.Get(ctx, client.ObjectKey{Name: mgmt.Spec.Release}, release); err != nil {
		return nil, fmt.Errorf("failed to get Release %s: %w", mgmt.Spec.Release, err)
	}

	if err := checkComponentsRemoval(ctx, v.Client, release, oldRegion, newRegion); err != nil {
		return admission.Warnings{"Some of the providers cannot be removed"},
			apierrors.NewInvalid(newRegion.GroupVersionKind().GroupKind(), newRegion.Name, field.ErrorList{
				field.Forbidden(field.NewPath("spec", "providers"), err.Error()),
			})
	}

	invalidRegionMsg := fmt.Sprintf("the Region %s is invalid", newRegion.Name)
	incompatibleContracts, err := validationutil.GetIncompatibleContracts(ctx, v, release, newRegion)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", invalidRegionMsg, err)
	}

	if incompatibleContracts != "" {
		return admission.Warnings{"The Region object has incompatible CAPI contract versions in ProviderTemplates"}, fmt.Errorf("%s: %s", invalidRegionMsg, incompatibleContracts)
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *RegionValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	rgn, ok := obj.(*kcmv1.Region)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected Region but got a %T", obj))
	}

	err := validationutil.RegionDeletionAllowed(ctx, v.Client, rgn)
	if err != nil {
		warning := strings.ToUpper(err.Error()[:1]) + err.Error()[1:]
		return admission.Warnings{warning}, errRegionDeletionForbidden
	}
	return nil, nil
}
