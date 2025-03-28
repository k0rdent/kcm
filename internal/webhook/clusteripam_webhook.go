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

	kcmv1 "github.com/K0rdent/kcm/api/v1alpha1"
)

type ClusterIPAMClaimValidator struct {
	client.Client
}

func (v *ClusterIPAMClaimValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&kcmv1.ClusterIPAMClaim{}).
		WithValidator(v).
		WithDefaulter(v).
		Complete()
}

var (
	_ webhook.CustomValidator = &ClusterIPAMClaimValidator{}
	_ webhook.CustomDefaulter = &ClusterIPAMClaimValidator{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (*ClusterIPAMClaimValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	clusterIpamClaim, ok := obj.(*kcmv1.ClusterIPAMClaim)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected ClusterIPAMClaim but got a %T", obj))
	}

	if clusterIpamClaim.Spec.ClusterIPPool.Count < 0 {
		return nil, fmt.Errorf("invalid ClusterIPPool count: %d", clusterIpamClaim.Spec.ClusterIPPool.Count)
	}

	if clusterIpamClaim.Spec.ExternalIPPool.Count < 0 {
		return nil, fmt.Errorf("invalid ExternalIPPool count: %d", clusterIpamClaim.Spec.ExternalIPPool.Count)
	}

	if clusterIpamClaim.Spec.NodeIPPool.Count < 0 {
		return nil, fmt.Errorf("invalid NodeIPPool count: %d", clusterIpamClaim.Spec.NodeIPPool.Count)
	}
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (v *ClusterIPAMClaimValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	return v.ValidateCreate(ctx, newObj)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (*ClusterIPAMClaimValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (*ClusterIPAMClaimValidator) Default(_ context.Context, obj runtime.Object) error {
	_, ok := obj.(*kcmv1.ClusterDeployment)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected ClusterIPAMClaim but got a %T", obj))
	}

	return nil
}
