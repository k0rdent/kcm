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

package helm

import (
	"context"
	"fmt"

	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcm "github.com/K0rdent/kcm/api/v1beta1"
)

type DefaultRegistryConfig struct {
	// RepoType is the type specified by default in HelmRepository
	// objects.  Valid types are 'default' for http/https repositories, and
	// 'oci' for OCI repositories.  The RepositoryType is set in main based on
	// the URI scheme of the DefaultRegistryURL.
	RepoType          string
	URL               string
	CredentialsSecret string
	CertSecret        string
	Insecure          bool
}

func (r *DefaultRegistryConfig) HelmRepositorySpec() sourcev1.HelmRepositorySpec {
	return sourcev1.HelmRepositorySpec{
		Type:     r.RepoType,
		URL:      r.URL,
		Interval: metav1.Duration{Duration: DefaultReconcileInterval},
		Insecure: r.Insecure,
		SecretRef: func() *meta.LocalObjectReference {
			if r.CredentialsSecret != "" {
				return &meta.LocalObjectReference{
					Name: r.CredentialsSecret,
				}
			}
			return nil
		}(),
		CertSecretRef: func() *meta.LocalObjectReference {
			if r.CertSecret != "" {
				return &meta.LocalObjectReference{
					Name: r.CertSecret,
				}
			}
			return nil
		}(),
	}
}

func ReconcileHelmRepository(ctx context.Context, cl client.Client, name, namespace string, spec sourcev1.HelmRepositorySpec) error {
	helmRepo := &sourcev1.HelmRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	operation, err := ctrl.CreateOrUpdate(ctx, cl, helmRepo, func() error {
		if helmRepo.Labels == nil {
			helmRepo.Labels = make(map[string]string)
		}

		helmRepo.Labels[kcm.KCMManagedLabelKey] = kcm.KCMManagedLabelValue
		helmRepo.Spec = spec
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update HelmRepository %s: %w", client.ObjectKeyFromObject(helmRepo), err)
	}
	if operation == controllerutil.OperationResultCreated || operation == controllerutil.OperationResultUpdated {
		ctrl.LoggerFrom(ctx).Info("Successfully mutated HelmRepository", "HelmRepository", client.ObjectKeyFromObject(helmRepo), "operation_result", operation)
	}
	return nil
}
