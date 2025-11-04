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

package auth

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// GetAuthenticationConfiguration retrieves the [github.com/K0rdent/kcm/api/v1beta1.AuthenticationConfiguration] object
// from the [github.com/K0rdent/kcm/api/v1beta1.ClusterAuthentication]
// and injects the CA certificate from the CASecret reference into it.
func GetAuthenticationConfiguration(ctx context.Context, mgmtClient client.Client, clAuth *kcmv1.ClusterAuthentication) (*kcmv1.AuthenticationConfiguration, error) {
	if clAuth.Spec.AuthenticationConfiguration == nil {
		return &kcmv1.AuthenticationConfiguration{}, nil
	}

	result := clAuth.Spec.AuthenticationConfiguration
	if clAuth.Spec.CASecret == nil {
		return result, nil
	}

	// set the CA from the caSecret as the certificateAuthority in the AuthenticationConfiguration
	namespace := clAuth.Namespace
	if clAuth.Spec.CASecret.Namespace != "" {
		namespace = clAuth.Spec.CASecret.Namespace
	}
	caSecretObjKey := client.ObjectKey{
		Namespace: namespace,
		Name:      clAuth.Spec.CASecret.Name,
	}
	caSecret := &corev1.Secret{}
	if err := mgmtClient.Get(ctx, caSecretObjKey, caSecret); err != nil {
		return nil, fmt.Errorf("failed to get ClusterAuthentication CA secret %s: %w", caSecretObjKey, err)
	}

	caCert, ok := caSecret.Data[clAuth.Spec.CASecret.Key]
	if !ok {
		return nil, fmt.Errorf("secret %s does not contain %s key", caSecretObjKey, clAuth.Spec.CASecret.Key)
	}

	if len(caCert) > 0 {
		caCertStr := string(caCert)
		for i := range result.JWT {
			result.JWT[i].Issuer.CertificateAuthority = caCertStr
		}
	}

	return result, nil
}
