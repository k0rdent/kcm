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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
)

const invalidClusterAuthenticationMsg = "the ClusterAuthentication is invalid"

type ClusterAuthenticationValidator struct {
	delegatingValidator[*kcmv1.ClusterAuthentication]
}

// NewClusterAuthenticationValidator builds a ClusterAuthenticationValidator wired to c.
func NewClusterAuthenticationValidator(c client.Client) *ClusterAuthenticationValidator {
	return &ClusterAuthenticationValidator{delegatingValidator[*kcmv1.ClusterAuthentication]{
		Client:          c,
		invalidMsg:      invalidClusterAuthenticationMsg,
		validate:        validationutil.ValidateClusterAuthentication,
		deletionAllowed: validationutil.ClusterAuthenticationDeletionAllowed,
	}}
}

func (v *ClusterAuthenticationValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	*v = *NewClusterAuthenticationValidator(mgr.GetClient())
	return ctrl.NewWebhookManagedBy(mgr, &kcmv1.ClusterAuthentication{}).
		WithValidator(v).
		Complete()
}

var _ admission.Validator[*kcmv1.ClusterAuthentication] = &ClusterAuthenticationValidator{}
