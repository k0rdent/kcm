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

package validation

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

type ClusterParent interface {
	client.Object

	HelmReleaseName(string) string
	GetComponentsStatus() *kcmv1.ComponentsCommonStatus
}

// getParent returns the parent object (either a Region or the Management object
// if the region is unset) for given Credential.
func getParent(ctx context.Context, mgmtClient client.Client, cred *kcmv1.Credential) (ClusterParent, error) {
	if cred.Spec.Region != "" {
		rgn := &kcmv1.Region{}
		err := mgmtClient.Get(ctx, client.ObjectKey{Name: cred.Spec.Region}, rgn)
		if err != nil {
			return nil, fmt.Errorf("failed to get %s Region: %w", cred.Spec.Region, err)
		}
		return rgn, nil
	}
	mgmt := &kcmv1.Management{}
	err := mgmtClient.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, mgmt)
	if err != nil {
		return nil, fmt.Errorf("failed to get Management: %w", err)
	}
	return mgmt, nil
}
