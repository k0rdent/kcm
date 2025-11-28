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

package registry

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

// Propagator handles distribution of registry secrets across namespaces.
type Propagator struct {
	client client.Client
}

// NewPropagator creates a new Propagator instance.
func NewPropagator(cl client.Client) *Propagator {
	return &Propagator{client: cl}
}

// PropagateSecret copies a registry secret from source to target namespace.
func (p *Propagator) PropagateSecret(
	ctx context.Context,
	sourceNamespace, secretName string,
	targetNamespaces []string,
	owner client.Object,
) error {
	l := log.FromContext(ctx).WithName("registry-propagator")

	// Get the source secret
	sourceSecret := &corev1.Secret{}
	err := p.client.Get(ctx, client.ObjectKey{Name: secretName, Namespace: sourceNamespace}, sourceSecret)
	if err != nil {
		return fmt.Errorf("failed to get source secret %s/%s: %w", sourceNamespace, secretName, err)
	}

	// Copy to each target namespace
	for _, targetNS := range targetNamespaces {
		if targetNS == sourceNamespace {
			// Skip if target is the same as source
			continue
		}

		l.V(1).Info("Propagating registry secret", "secretName", secretName, "targetNamespace", targetNS)

		err := kubeutil.CopySecret(
			ctx,
			p.client,
			p.client,
			client.ObjectKey{Namespace: sourceNamespace, Name: secretName},
			targetNS,
			"", // Use same name
			owner,
			map[string]string{
				RegistrySecretLabel: RegistrySecretLabelValue,
			},
		)
		if err != nil {
			l.Error(err, "Failed to propagate secret", "secretName", secretName, "targetNamespace", targetNS)
			return fmt.Errorf("failed to propagate secret to namespace %s: %w", targetNS, err)
		}
	}

	return nil
}

// ListManagedRegistrySecrets returns all registry secrets managed by KCM.
func (p *Propagator) ListManagedRegistrySecrets(ctx context.Context, namespace string) ([]corev1.Secret, error) {
	secretList := &corev1.SecretList{}
	err := p.client.List(ctx, secretList, client.InNamespace(namespace), client.MatchingLabels{
		RegistrySecretLabel: RegistrySecretLabelValue,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list registry secrets: %w", err)
	}

	return secretList.Items, nil
}

// CleanupOrphanedSecrets removes registry secrets from namespaces that no longer need them.
func (p *Propagator) CleanupOrphanedSecrets(ctx context.Context, validNamespaces []string) error {
	l := log.FromContext(ctx).WithName("registry-propagator")

	// Get all namespaces
	nsList := &corev1.NamespaceList{}
	if err := p.client.List(ctx, nsList); err != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	// Create a map of valid namespaces for quick lookup
	validNS := make(map[string]bool)
	for _, ns := range validNamespaces {
		validNS[ns] = true
	}

	// Check each namespace for orphaned secrets
	for _, ns := range nsList.Items {
		if validNS[ns.Name] {
			continue // This namespace should have the secrets
		}

		// List registry secrets in this namespace
		secrets, err := p.ListManagedRegistrySecrets(ctx, ns.Name)
		if err != nil {
			l.Error(err, "Failed to list secrets in namespace", "namespace", ns.Name)
			continue
		}

		// Delete orphaned secrets
		for _, secret := range secrets {
			l.V(1).Info("Deleting orphaned registry secret", "namespace", secret.Namespace, "name", secret.Name)
			if err := p.client.Delete(ctx, &secret); err != nil && !apierrors.IsNotFound(err) {
				l.Error(err, "Failed to delete orphaned secret", "namespace", secret.Namespace, "name", secret.Name)
			}
		}
	}

	return nil
}

// GetNamespacesRequiringCredentials returns a list of namespaces that need registry credentials.
func (p *Propagator) GetNamespacesRequiringCredentials(ctx context.Context, systemNamespace string) ([]string, error) {
	namespaces := []string{systemNamespace}

	// Get all ClusterDeployments that need credentials
	cdList := &kcmv1.ClusterDeploymentList{}
	if err := p.client.List(ctx, cdList); err != nil {
		return nil, fmt.Errorf("failed to list ClusterDeployments: %w", err)
	}

	// Add unique namespaces from ClusterDeployments
	nsMap := make(map[string]bool)
	nsMap[systemNamespace] = true

	for _, cd := range cdList.Items {
		if cd.Spec.InheritRegistryCredentials && cd.Namespace != "" {
			nsMap[cd.Namespace] = true
		}
	}

	// Convert map to slice
	namespaces = make([]string, 0, len(nsMap))
	for ns := range nsMap {
		namespaces = append(namespaces, ns)
	}

	return namespaces, nil
}

