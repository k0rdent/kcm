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

package adapter

import (
	"context"
	"encoding/json"
	"fmt"

	infobloxv1alpha1 "github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	InfobloxIPPoolKind = "InfobloxIPPool"
)

type InfobloxAdapter struct{}

func NewInfobloxAdapter() *InfobloxAdapter {
	return &InfobloxAdapter{}
}

func (InfobloxAdapter) BindAddress(ctx context.Context, config IPAMConfig, c client.Client) (kcmv1.ClusterIPAMProviderData, error) {
	pool := infobloxv1alpha1.InfobloxIPPool{
		ObjectMeta: metav1.ObjectMeta{Name: config.ClusterIPAMClaim.Name, Namespace: config.ClusterIPAMClaim.Namespace},
	}

	_, err := ctrl.CreateOrUpdate(ctx, c, &pool, func() error {
		pool.Spec = infobloxv1alpha1.InfobloxIPPoolSpec{
			Subnets: []infobloxv1alpha1.Subnet{
				{
					CIDR: config.ClusterIPAMClaim.Spec.NodeNetwork.CIDR,
				},
			},
		}
		return controllerutil.SetOwnerReference(config.ClusterIPAMClaim, &pool, c.Scheme())
	})
	if err != nil {
		return kcmv1.ClusterIPAMProviderData{}, fmt.Errorf("failed to create or update ip pool resource: %w", err)
	}

	poolAPIGroup := infobloxv1alpha1.GroupVersion.String()
	poolRef := corev1.TypedLocalObjectReference{
		APIGroup: &poolAPIGroup,
		Kind:     InfobloxIPPoolKind,
		Name:     config.ClusterIPAMClaim.Name,
	}

	poolData, err := json.Marshal(poolRef)
	if err != nil {
		return kcmv1.ClusterIPAMProviderData{}, fmt.Errorf("failed to marshal ip pool data: %w", err)
	}

	for _, condition := range pool.Status.Conditions {
		if condition.Status != metav1.StatusSuccess {
			return kcmv1.ClusterIPAMProviderData{Name: ClusterDeploymentConfigKeyName, Data: &apiextv1.JSON{Raw: poolData}, Ready: false}, nil
		}
	}

	return kcmv1.ClusterIPAMProviderData{Name: ClusterDeploymentConfigKeyName, Data: &apiextv1.JSON{Raw: poolData}, Ready: true}, nil
}
