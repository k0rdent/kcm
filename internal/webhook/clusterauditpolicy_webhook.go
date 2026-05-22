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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
)

type ClusterAuditPolicyValidator struct {
	client.Client
}

const invalidClusterAuditPolicyMsg = "the ClusterAuditPolicy is invalid"

func (v *ClusterAuditPolicyValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr, &kcmv1.ClusterAuditPolicy{}).
		WithValidator(v).
		Complete()
}

var _ admission.Validator[*kcmv1.ClusterAuditPolicy] = &ClusterAuditPolicyValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (*ClusterAuditPolicyValidator) ValidateCreate(_ context.Context, obj *kcmv1.ClusterAuditPolicy) (admission.Warnings, error) {
	if err := validationutil.ValidateClusterAuditPolicy(obj); err != nil {
		return nil, fmt.Errorf("%s: %w", invalidClusterAuditPolicyMsg, err)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (*ClusterAuditPolicyValidator) ValidateUpdate(_ context.Context, _, _ *kcmv1.ClusterAuditPolicy) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (v *ClusterAuditPolicyValidator) ValidateDelete(ctx context.Context, obj *kcmv1.ClusterAuditPolicy) (admission.Warnings, error) {
	if err := validationutil.ClusterAuditPolicyDeletionAllowed(ctx, v.Client, obj); err != nil {
		return nil, err
	}

	return nil, nil
}
