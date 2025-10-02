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
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func RegionClusterReference(ctx context.Context, mgmtClient client.Client, systemNamespace string, rgn *kcmv1.Region) error {
	if rgn.Spec.KubeConfig != nil {
		kubeConfSecret := &corev1.Secret{}
		secretKey := client.ObjectKey{Namespace: systemNamespace, Name: rgn.Spec.KubeConfig.Name}
		if err := mgmtClient.Get(ctx, secretKey, kubeConfSecret); err != nil {
			return fmt.Errorf("failed to get Secret %s: %w", secretKey, err)
		}
		if _, ok := kubeConfSecret.Data[rgn.Spec.KubeConfig.Key]; !ok {
			return fmt.Errorf("kubeConfig Secret does not have %s key defined", rgn.Spec.KubeConfig.Key)
		}
	}
	if rgn.Spec.ClusterDeployment != nil {
		cld := &metav1.PartialObjectMetadata{}
		cld.SetGroupVersionKind(kcmv1.GroupVersion.WithKind(kcmv1.ClusterDeploymentKind))
		cldKey := client.ObjectKey{
			Namespace: rgn.Spec.ClusterDeployment.Namespace,
			Name:      rgn.Spec.ClusterDeployment.Name,
		}
		if err := mgmtClient.Get(ctx, cldKey, cld); err != nil {
			return fmt.Errorf("failed to get ClusterDeployment %s: %w", cldKey, err)
		}
	}
	return nil
}

func RegionDeletionAllowed(ctx context.Context, mgmtClient client.Client, rgn *kcmv1.Region) error {
	creds := &kcmv1.CredentialList{}
	if err := mgmtClient.List(ctx, creds, client.MatchingFields{kcmv1.CredentialRegionIndexKey: rgn.Name}); err != nil {
		return fmt.Errorf("failed to list Credential for %s region: %w", rgn.Name, err)
	}

	for _, cred := range creds.Items {
		clusterDeployments := &kcmv1.ClusterDeploymentList{}
		if err := mgmtClient.List(ctx, clusterDeployments,
			client.InNamespace(cred.Namespace),
			client.MatchingFields{kcmv1.ClusterDeploymentCredentialIndexKey: cred.Name},
			client.Limit(1),
		); err != nil {
			return fmt.Errorf("failed to list ClusterDeployments in %s namespace with %s Credential: %w", cred.Namespace, cred.Name, err)
		}
		if len(clusterDeployments.Items) > 0 {
			return errors.New("the Region object can't be removed while any ClusterDeployment objects deployed in that Region still exist")
		}
	}
	return nil
}
