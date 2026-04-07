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
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/chartutil"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

func Test_getRegionalComponentValues(t *testing.T) {
	const (
		certManagerComponentName  = "cert-manager"
		capiOperatorComponentName = "cluster-api-operator"
		veleroComponentName       = "velero"
		telemetryComponentName    = "telemetry"

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
				telemetryComponentName:    map[string]any{},
			},
		},
		{
			name:          "no values provided, cert manager is installed: should enable subcomponents",
			currentValues: chartutil.Values{},
			opts:          ReconcileComponentsOpts{CertManagerInstalled: true},
			expectedResult: map[string]any{
				certManagerComponentName: map[string]any{},
				telemetryComponentName:   map[string]any{},
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
				telemetryComponentName:   map[string]any{},
				capiOperatorComponentName: map[string]any{
					"enabled": true,
				},
				veleroComponentName: map[string]any{
					"enabled": false,
				},
			},
		},
		{
			name: "ensure telemetry is set as it was provided",
			currentValues: chartutil.Values{
				telemetryComponentName: map[string]any{
					"mode":     "disabled",
					"interval": "1h",
				},
			},
			opts: ReconcileComponentsOpts{CertManagerInstalled: true},
			expectedResult: map[string]any{
				certManagerComponentName: map[string]any{},
				telemetryComponentName: map[string]any{
					"mode":     "disabled",
					"interval": "1h",
				},
				capiOperatorComponentName: map[string]any{
					"enabled": true,
				},
				veleroComponentName: map[string]any{
					"enabled": true,
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
				telemetryComponentName:   map[string]any{},
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
				telemetryComponentName:   map[string]any{},
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

func Test_getComponentValues_EnableProvidersReload(t *testing.T) {
	tests := []struct {
		name                           string
		setEnv                         bool
		enableProvidersReloadEnvValue  string
		expectGlobal                   bool
		expectEnableProvidersReload    bool
		expectEnableProvidersReloadKey bool
	}{
		{
			name:                           "enableProvidersReload is true when env var is true",
			setEnv:                         true,
			enableProvidersReloadEnvValue:  "true",
			expectGlobal:                   true,
			expectEnableProvidersReload:    true,
			expectEnableProvidersReloadKey: true,
		},
		{
			name:                           "enableProvidersReload is false when env var is false",
			setEnv:                         true,
			enableProvidersReloadEnvValue:  "false",
			expectGlobal:                   true,
			expectEnableProvidersReload:    false,
			expectEnableProvidersReloadKey: true,
		},
		{
			name:         "enableProvidersReload is omitted when env var is not set",
			setEnv:       false,
			expectGlobal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(kubeutil.EnableProvidersReloadEnvName, tt.enableProvidersReloadEnvValue)
				t.Cleanup(func() { _ = os.Unsetenv(kubeutil.EnableProvidersReloadEnvName) })
			}

			componentValues, err := getComponentValues(
				t.Context(),
				kcmv1.CoreCAPIName,
				nil,
				ReconcileComponentsOpts{},
			)
			require.NoError(t, err)

			values := make(map[string]any)
			require.NoError(t, json.Unmarshal(componentValues.Raw, &values))

			globalRaw, globalExists := values["global"]
			require.Equal(t, tt.expectGlobal, globalExists)
			if !tt.expectGlobal {
				return
			}

			global, ok := globalRaw.(map[string]any)
			require.True(t, ok)

			if !tt.expectEnableProvidersReloadKey {
				_, exists := global["enableProvidersReload"]
				require.False(t, exists)
				return
			}

			enableProvidersReload, ok := global["enableProvidersReload"].(bool)
			require.True(t, ok)
			require.Equal(t, tt.expectEnableProvidersReload, enableProvidersReload)
		})
	}
}

func Test_getComponentValues_ProviderSveltosReloadAndImagePatch(t *testing.T) {
	trueValue := "true"
	falseValue := "false"

	tests := []struct {
		name                string
		envValue            *string
		imagePullSecretName string
		wantImagePatch      bool
		wantReloader        *string
	}{
		{
			name:                "sets only image patch when image pull secret is configured",
			imagePullSecretName: "registry-secret",
			wantImagePatch:      true,
		},
		{
			name:         "sets reloader patch to true when enable providers reload is true",
			envValue:     &trueValue,
			wantReloader: &trueValue,
		},
		{
			name:         "sets reloader patch to false when enable providers reload is false",
			envValue:     &falseValue,
			wantReloader: &falseValue,
		},
		{
			name:                "sets both image patch and reloader patch when both options are configured",
			envValue:            &trueValue,
			imagePullSecretName: "registry-secret",
			wantImagePatch:      true,
			wantReloader:        &trueValue,
		},
		{
			name:                "sets image patch and explicit false reloader patch",
			envValue:            &falseValue,
			imagePullSecretName: "registry-secret",
			wantImagePatch:      true,
			wantReloader:        &falseValue,
		},
	}

	//nolint:govet // shadow does not make sense here
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != nil {
				t.Setenv(kubeutil.EnableProvidersReloadEnvName, *tt.envValue)
				t.Cleanup(func() { _ = os.Unsetenv(kubeutil.EnableProvidersReloadEnvName) })
			}

			componentValues, err := getComponentValues(
				t.Context(),
				kcmv1.ProviderSveltosName,
				nil,
				ReconcileComponentsOpts{ImagePullSecretName: tt.imagePullSecretName},
			)
			require.NoError(t, err)

			values := make(map[string]any)
			require.NoError(t, json.Unmarshal(componentValues.Raw, &values))

			projectsveltosRaw, ok := values["projectsveltos"]
			require.True(t, ok)
			projectsveltos, ok := projectsveltosRaw.(map[string]any)
			require.True(t, ok)

			getNestedValue := func(root map[string]any, keys ...string) (any, bool) {
				current := any(root)
				for _, key := range keys {
					currentMap, ok := current.(map[string]any)
					if !ok {
						return nil, false
					}

					var exists bool
					current, exists = currentMap[key]
					if !exists {
						return nil, false
					}
				}

				return current, true
			}

			patchesConfigured := tt.wantImagePatch || tt.wantReloader != nil

			addonControllerRaw, hasAddonController := projectsveltos["addonController"]
			if patchesConfigured {
				require.True(t, hasAddonController)
			} else {
				require.False(t, hasAddonController)
			}

			var addonController map[string]any
			if hasAddonController {
				addonController, ok = addonControllerRaw.(map[string]any)
				require.True(t, ok)
			}

			assertPatchData := func(data map[string]any) {
				imagePatchRaw, hasImagePatch := data["image-patch"]
				if tt.wantImagePatch {
					require.True(t, hasImagePatch)
					imagePatch, ok := imagePatchRaw.(string)
					require.True(t, ok)
					require.Contains(t, imagePatch, "patch: |-")
					require.NotContains(t, imagePatch, "target:")
					require.Contains(t, imagePatch, "path: /spec/template/spec/imagePullSecrets")
					require.Contains(t, imagePatch, tt.imagePullSecretName)
				} else {
					require.False(t, hasImagePatch)
				}

				reloaderPatchRaw, hasReloaderPatch := data["reloader-annotation-patch"]
				if tt.wantReloader == nil {
					require.False(t, hasReloaderPatch)
					return
				}

				require.True(t, hasReloaderPatch)
				reloaderPatch, ok := reloaderPatchRaw.(string)
				require.True(t, ok)
				require.Contains(t, reloaderPatch, "patch: |-")
				require.NotContains(t, reloaderPatch, "target:")
				require.Contains(t, reloaderPatch, "kind: Deployment")
				require.Contains(t, reloaderPatch, "reloader.stakater.com/auto")
				require.Contains(t, reloaderPatch, "\""+*tt.wantReloader+"\"")
			}

			if tt.wantReloader != nil {
				expectedAnnotationValue := *tt.wantReloader
				annotationPaths := [][]string{
					{"addonController", "annotations"},
					{"accessManager", "manager", "annotations"},
					{"accessManager", "annotations"},
					{"scManager", "annotations"},
					{"hcManager", "annotations"},
					{"eventManager", "annotations"},
					{"shardController", "annotations"},
					{"techsupportController", "annotations"},
					{"mcpServer", "annotations"},
				}

				for _, path := range annotationPaths {
					annotationMapRaw, found := getNestedValue(projectsveltos, path...)
					require.True(t, found, "missing annotations at path: %v", path)

					annotationMap, ok := annotationMapRaw.(map[string]any)
					require.True(t, ok)

					autoAnnotationRaw, ok := annotationMap["reloader.stakater.com/auto"]
					require.True(t, ok)

					autoAnnotationValue, ok := autoAnnotationRaw.(string)
					require.True(t, ok)
					require.Equal(t, expectedAnnotationValue, autoAnnotationValue)
				}
			} else if hasAddonController {
				_, hasAddonControllerAnnotations := addonController["annotations"]
				require.False(t, hasAddonControllerAnnotations)
			}

			classifierManagerRaw, hasClassifierManager := projectsveltos["classifierManager"]
			if !patchesConfigured {
				require.False(t, hasClassifierManager)
				return
			}

			require.True(t, hasClassifierManager)
			classifierManager, ok := classifierManagerRaw.(map[string]any)
			require.True(t, ok)

			if tt.wantReloader != nil {
				classifierAnnotationsRaw, ok := classifierManager["annotations"]
				require.True(t, ok)
				classifierAnnotations, ok := classifierAnnotationsRaw.(map[string]any)
				require.True(t, ok)
				autoAnnotationRaw, ok := classifierAnnotations["reloader.stakater.com/auto"]
				require.True(t, ok)
				autoAnnotationValue, ok := autoAnnotationRaw.(string)
				require.True(t, ok)
				require.Equal(t, *tt.wantReloader, autoAnnotationValue)
			} else {
				_, hasClassifierAnnotations := classifierManager["annotations"]
				require.False(t, hasClassifierAnnotations)
			}

			agentPatchConfigMapRaw, ok := classifierManager["agentPatchConfigMap"]
			require.True(t, ok)
			agentPatchConfigMap, ok := agentPatchConfigMapRaw.(map[string]any)
			require.True(t, ok)

			agentPatchDataRaw, ok := agentPatchConfigMap["data"]
			require.True(t, ok)
			agentPatchData, ok := agentPatchDataRaw.(map[string]any)
			require.True(t, ok)
			assertPatchData(agentPatchData)

			driftDetectionManagerPatchConfigMapRaw, ok := addonController["driftDetectionManagerPatchConfigMap"]
			require.True(t, ok)
			driftDetectionManagerPatchConfigMap, ok := driftDetectionManagerPatchConfigMapRaw.(map[string]any)
			require.True(t, ok)

			driftDetectionManagerPatchDataRaw, ok := driftDetectionManagerPatchConfigMap["data"]
			require.True(t, ok)
			driftDetectionManagerPatchData, ok := driftDetectionManagerPatchDataRaw.(map[string]any)
			require.True(t, ok)
			assertPatchData(driftDetectionManagerPatchData)
		})
	}
}
