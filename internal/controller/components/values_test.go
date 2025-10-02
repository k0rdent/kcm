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

package components

import (
	"testing"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/chartutil"
)

func Test_getRegionalComponentValues(t *testing.T) {
	const (
		certManagerComponentName  = "cert-manager"
		capiOperatorComponentName = "cluster-api-operator"
		veleroComponentName       = "velero"

		registryCertSecretName = "registry-cert-secret"
	)

	tests := []struct {
		name           string
		currentValues  chartutil.Values
		opts           ReconcileComponentsOpts
		expectedResult map[string]any
	}{
		{
			name:          "no values provided, cert manager is not yet installed",
			currentValues: chartutil.Values{},
			opts:          ReconcileComponentsOpts{CertManagerInstalled: false},
			expectedResult: map[string]any{
				certManagerComponentName:  map[string]any{},
				capiOperatorComponentName: map[string]any{},
				veleroComponentName:       map[string]any{},
			},
		},
		{
			name:          "no values provided, cert manager is installed: should enable subcomponents",
			currentValues: chartutil.Values{},
			opts:          ReconcileComponentsOpts{CertManagerInstalled: true},
			expectedResult: map[string]any{
				certManagerComponentName: map[string]any{},
				capiOperatorComponentName: map[string]any{
					"enabled": true,
				},
				veleroComponentName: map[string]any{
					"enabled": true,
				},
			},
		},
		{
			name: "velero is explicitly disabled, cert manager is installed: should enable subcomponents except velero",
			currentValues: chartutil.Values{
				veleroComponentName: map[string]any{
					"enabled": false,
				},
			},
			opts: ReconcileComponentsOpts{CertManagerInstalled: true},
			expectedResult: map[string]any{
				certManagerComponentName: map[string]any{},
				capiOperatorComponentName: map[string]any{
					"enabled": true,
				},
				veleroComponentName: map[string]any{
					"enabled": false,
				},
			},
		},
		{
			name: "capi operator is explicitly disabled, cert manager is installed: should enable subcomponents except capi operator",
			currentValues: chartutil.Values{
				capiOperatorComponentName: map[string]any{
					"enabled": false,
				},
				veleroComponentName: map[string]any{
					"labels": map[string]any{
						"testKey": "testValue",
					},
				},
			},
			opts: ReconcileComponentsOpts{CertManagerInstalled: true},
			expectedResult: map[string]any{
				certManagerComponentName: map[string]any{},
				capiOperatorComponentName: map[string]any{
					"enabled": false,
				},
				veleroComponentName: map[string]any{
					"enabled": true,
					"labels": map[string]any{
						"testKey": "testValue",
					},
				},
			},
		},
		{
			name:          "registry cert secret is configured: should add registry cert volume mount",
			currentValues: chartutil.Values{},
			opts:          ReconcileComponentsOpts{CertManagerInstalled: true, RegistryCertSecretName: registryCertSecretName},
			expectedResult: map[string]any{
				certManagerComponentName: map[string]any{},
				capiOperatorComponentName: map[string]any{
					"enabled": true,
					"volumeMounts": map[string]any{
						"manager": []any{
							map[string]any{
								"mountPath": "/tmp/k8s-webhook-server/serving-certs",
								"name":      "cert",
							},
							map[string]any{
								"mountPath": "/etc/ssl/certs/registry-ca.pem",
								"name":      "registry-cert",
								"subPath":   "registry-ca.pem",
							},
						},
					},
					"volumes": []any{
						map[string]any{
							"name": "cert",
							"secret": map[string]any{
								"defaultMode": 420,
								"secretName":  "capi-operator-webhook-service-cert",
							},
						},
						map[string]any{
							"name": "registry-cert",
							"secret": map[string]any{
								"defaultMode": 420,
								"secretName":  registryCertSecretName,
								"items": []any{
									map[string]any{
										"key":  "ca.crt",
										"path": "registry-ca.pem",
									},
								},
							},
						},
					},
				},
				veleroComponentName: map[string]any{
					"enabled": true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := getRegionalComponentValues(t.Context(), tt.currentValues, tt.opts)
			require.NoError(t, err)
			require.Equal(t, tt.expectedResult, result)
		})
	}
}
