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
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"helm.sh/helm/v3/pkg/chartutil"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/certmanager"
)

func getComponentValues(
	ctx context.Context,
	restConfig *rest.Config,
	name string,
	config *apiextv1.JSON,
	opts ReconcileComponentsOpts,
) (*apiextv1.JSON, error) {
	l := ctrl.LoggerFrom(ctx)

	currentValues := chartutil.Values{}
	if config != nil && config.Raw != nil {
		if err := json.Unmarshal(config.Raw, &currentValues); err != nil {
			return nil, err
		}
	}

	componentValues := chartutil.Values{}

	switch name {
	case kcmv1.CoreKCMName:
		// Those are only needed for the initial installation
		componentValues = map[string]any{
			"controller": map[string]any{
				"createManagement":       false,
				"createAccessManagement": false,
				"createRelease":          false,
			},
		}

		if err := certManagerInstalled(ctx, restConfig, opts.Namespace); err != nil {
			l.Info("Waiting for Cert manager API before enabling additional components")
		} else {
			l.Info("Cert manager is installed, enabling admission webhook")
			componentValues["admissionWebhook"] = map[string]any{"enabled": true}
		}

		if opts.RegistryCertSecretName != "" {
			fluxV := make(map[string]any)
			if currentValues != nil {
				if raw, ok := currentValues["flux2"]; ok {
					var castOk bool
					if fluxV, castOk = raw.(map[string]any); !castOk {
						return nil, fmt.Errorf("failed to cast 'flux2' (type %T) to map[string]any", raw)
					}
				}
			}

			componentValues["flux2"] = processFluxCertVolumeMounts(fluxV, opts.RegistryCertSecretName)
		}

		regionalConfig, err := getRegionalComponentValues(ctx, currentValues, restConfig, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to get regional values: %w", err)
		}
		componentValues["regional"] = regionalConfig

	case kcmv1.CoreKCMRegionalName:
		var err error
		componentValues, err = getRegionalComponentValues(ctx, currentValues, restConfig, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to get regional values: %w", err)
		}

	case kcmv1.ProviderSveltosName:
		componentValues = map[string]any{
			"projectsveltos": map[string]any{
				"registerMgmtClusterJob": map[string]any{
					"registerMgmtCluster": map[string]any{
						"args": []string{
							"--labels=" + kcmv1.K0rdentManagementClusterLabelKey + "=" + kcmv1.K0rdentManagementClusterLabelValue,
						},
					},
				},
			},
		}
	}

	if opts.GlobalRegistry != "" {
		globalValues := map[string]any{
			"global": map[string]any{
				"registry": opts.GlobalRegistry,
			},
		}
		componentValues = chartutil.CoalesceTables(componentValues, globalValues)
	}

	var merged chartutil.Values
	// for projectsveltos, we want new values to override values provided in Management spec
	if name == kcmv1.ProviderSveltosName {
		merged = chartutil.CoalesceTables(componentValues, currentValues)
	} else {
		merged = chartutil.CoalesceTables(currentValues, componentValues)
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal values for %s component: %w", name, err)
	}
	return &apiextv1.JSON{Raw: raw}, nil
}

func certManagerInstalled(ctx context.Context, restConfig *rest.Config, namespace string) error {
	if restConfig == nil {
		return errors.New("rest config is nil")
	}
	return certmanager.VerifyAPI(ctx, restConfig, namespace)
}

func getRegionalComponentValues(
	ctx context.Context,
	currentValues chartutil.Values,
	restConfig *rest.Config,
	opts ReconcileComponentsOpts,
) (map[string]any, error) {
	l := ctrl.LoggerFrom(ctx)

	regionalValues := make(map[string]any)
	capiOperatorValues := make(map[string]any)

	if err := certManagerInstalled(ctx, restConfig, opts.Namespace); err != nil {
		l.Info("Waiting for Cert manager API before enabling additional components")
	} else {
		l.Info("Cert manager is installed, enabling additional components")
		regionalValues["velero"] = map[string]any{"enabled": true}
		capiOperatorValues = map[string]any{"enabled": true}
	}

	if opts.RegistryCertSecretName != "" {
		capiOperatorV := make(map[string]any)
		if currentValues != nil {
			if raw, ok := currentValues["cluster-api-operator"]; ok {
				var castOk bool
				if capiOperatorV, castOk = raw.(map[string]any); !castOk {
					return nil, fmt.Errorf("failed to cast 'cluster-api-operator' (type %T) to map[string]any", raw)
				}
			}
		}
		capiOperatorValues = chartutil.CoalesceTables(capiOperatorValues, processCAPIOperatorCertVolumeMounts(capiOperatorV, opts.RegistryCertSecretName))
	}

	regionalValues["cluster-api-operator"] = capiOperatorValues
	return regionalValues, nil
}

func processCAPIOperatorCertVolumeMounts(capiOperatorValues map[string]any, registryCertSecret string) map[string]any {
	// explicitly add the webhook service cert volume to ensure it's present,
	// since helm does not merge custom array values with the default ones
	webhookCertVolume := map[string]any{
		"name": "cert",
		"secret": map[string]any{
			"defaultMode": 420,
			"secretName":  "capi-operator-webhook-service-cert",
		},
	}
	volumeName := "registry-cert"
	registryCertVolume := getRegistryCertVolumeValues(volumeName, registryCertSecret)

	if capiOperatorValues == nil {
		capiOperatorValues = make(map[string]any)
	}
	certVolumes := []any{webhookCertVolume, registryCertVolume}
	if existing, ok := capiOperatorValues["volumes"].([]any); ok {
		capiOperatorValues["volumes"] = append(existing, certVolumes...)
	} else {
		capiOperatorValues["volumes"] = certVolumes
	}

	// explicitly add the webhook service cert volume mount to ensure it's present,
	// since helm does not merge custom array values with the default ones
	webhookCertMount := map[string]any{
		"mountPath": "/tmp/k8s-webhook-server/serving-certs",
		"name":      "cert",
	}
	registryCertMount := getRegistryCertVolumeMountValues(volumeName)
	managerMounts := []any{webhookCertMount, registryCertMount}

	vmRaw, ok := capiOperatorValues["volumeMounts"].(map[string]any)
	if !ok {
		vmRaw = make(map[string]any)
	}
	if mgr, ok := vmRaw["manager"].([]any); ok {
		vmRaw["manager"] = append(mgr, managerMounts...)
	} else {
		vmRaw["manager"] = managerMounts
	}
	capiOperatorValues["volumeMounts"] = vmRaw

	return capiOperatorValues
}

func processFluxCertVolumeMounts(fluxValues map[string]any, registryCertSecret string) map[string]any {
	certVolumeName := "registry-cert"
	registryCertVolume := getRegistryCertVolumeValues(certVolumeName, registryCertSecret)

	if fluxValues == nil {
		fluxValues = make(map[string]any)
	}

	registryCertMount := getRegistryCertVolumeMountValues(certVolumeName)
	componentName := "sourceController"
	values, ok := fluxValues[componentName].(map[string]any)
	if !ok || values == nil {
		values = make(map[string]any)
	}
	certVolumes := []any{registryCertVolume}
	if existing, ok := values["volumes"].([]any); ok {
		values["volumes"] = append(existing, certVolumes...)
	} else {
		values["volumes"] = certVolumes
	}

	volumeMounts := []any{registryCertMount}
	if vm, ok := values["volumeMounts"].([]any); ok {
		values["volumeMounts"] = append(vm, volumeMounts...)
	} else {
		values["volumeMounts"] = volumeMounts
	}
	fluxValues[componentName] = values
	return fluxValues
}

func getRegistryCertVolumeValues(volumeName, secretName string) map[string]any {
	return map[string]any{
		"name": volumeName,
		"secret": map[string]any{
			"defaultMode": 420,
			"secretName":  secretName,
			"items": []any{
				map[string]any{
					"key":  "ca.crt",
					"path": "registry-ca.pem",
				},
			},
		},
	}
}

func getRegistryCertVolumeMountValues(volumeName string) map[string]any {
	return map[string]any{
		"mountPath": "/etc/ssl/certs/registry-ca.pem",
		"name":      volumeName,
		"subPath":   "registry-ca.pem",
	}
}
