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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
)

const invalidClusterCertificateAuthorityMsg = "the ClusterCertificateAuthority is invalid"

type ClusterCertificateAuthorityValidator struct {
	delegatingValidator[*kcmv1.ClusterCertificateAuthority]
}

// NewClusterCertificateAuthorityValidator builds a ClusterCertificateAuthorityValidator wired to c.
func NewClusterCertificateAuthorityValidator(c client.Client) *ClusterCertificateAuthorityValidator {
	return &ClusterCertificateAuthorityValidator{delegatingValidator[*kcmv1.ClusterCertificateAuthority]{
		Client:          c,
		invalidMsg:      invalidClusterCertificateAuthorityMsg,
		validate:        validationutil.ValidateClusterCertificateAuthority,
		deletionAllowed: validationutil.ClusterCertificateAuthorityDeletionAllowed,
	}}
}

func (v *ClusterCertificateAuthorityValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	*v = *NewClusterCertificateAuthorityValidator(mgr.GetClient())
	return ctrl.NewWebhookManagedBy(mgr, &kcmv1.ClusterCertificateAuthority{}).
		WithValidator(v).
		Complete()
}

var _ admission.Validator[*kcmv1.ClusterCertificateAuthority] = &ClusterCertificateAuthorityValidator{}
