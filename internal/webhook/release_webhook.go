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
	"slices"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

var errManagementIsNotFound = errors.New("no Management object found")

type ReleaseValidator struct {
	client.Client
}

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (v *ReleaseValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr, &kcmv1.Release{}).
		WithValidator(v).
		Complete()
}

var _ admission.Validator[*kcmv1.Release] = &ReleaseValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (*ReleaseValidator) ValidateCreate(_ context.Context, _ *kcmv1.Release) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (*ReleaseValidator) ValidateUpdate(_ context.Context, _, _ *kcmv1.Release) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (v *ReleaseValidator) ValidateDelete(ctx context.Context, obj *kcmv1.Release) (admission.Warnings, error) {
	mgmt, err := getManagement(ctx, v.Client)
	if err != nil {
		if errors.Is(err, errManagementIsNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if mgmt.Spec.Release == obj.Name {
		return nil, fmt.Errorf("release %s is still in use", obj.Name)
	}

	templates := obj.Templates()
	templatesInUse := []string{}
	for _, t := range mgmt.Templates() {
		if slices.Contains(templates, t) {
			templatesInUse = append(templatesInUse, t)
		}
	}
	if len(templatesInUse) > 0 {
		return nil, fmt.Errorf("the following ProviderTemplates associated with the Release are still in use: %s", strings.Join(templatesInUse, ", "))
	}
	return nil, nil
}

func getManagement(ctx context.Context, cl client.Client) (*kcmv1.Management, error) {
	mgmtList := &kcmv1.ManagementList{}
	if err := cl.List(ctx, mgmtList); err != nil {
		return nil, err
	}
	if len(mgmtList.Items) == 0 {
		return nil, errManagementIsNotFound
	}
	if len(mgmtList.Items) > 1 {
		return nil, fmt.Errorf("expected 1 Management object, got %d", len(mgmtList.Items))
	}
	return &mgmtList.Items[0], nil
}
