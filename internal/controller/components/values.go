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
	"os"
	"strconv"

	"helm.sh/helm/v3/pkg/chartutil"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/certmanager"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

func getComponentValues(
	ctx context.Context,
	name string,
	config *apiextv1.JSON,
	opts ReconcileComponentsOpts,
) (*apiextv1.JSON, error) {
	l := ctrl.LoggerFrom(ctx).WithValues("component", name)
	ctx = ctrl.LoggerInto(ctx, l)

	currentValues := chartutil.Values{}
	if config != nil && config.Raw != nil {
		if err := json.Unmarshal(config.Raw, &currentValues); err != nil {
			return nil, err
		}
	}

	env := loadEnvConfig()

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

		if !opts.CertManagerInstalled {
			l.Info("Waiting for Cert manager API before enabling additional components")
		} else {
			l.Info("Cert manager is installed, enabling admission webhook")
			componentValues["admissionWebhook"] = map[string]any{"enabled": true}
		}

		regionalValues, err := getCurrentValuesForKey(currentValues, "regional")
		if err != nil {
			return nil, fmt.Errorf("getting 'regional' values: %w", err)
		}

		componentValues["regional"], err = processRegionalComponentValues(ctx, regionalValues, opts)
		if err != nil {
			return nil, fmt.Errorf("processing regional values: %w", err)
		}

	case kcmv1.CoreKCMRegionalName:
		var err error
		componentValues, err = processRegionalComponentValues(ctx, currentValues, opts)
		if err != nil {
			return nil, fmt.Errorf("process regional values: %w", err)
		}

	case kcmv1.ProviderSveltosName:
		componentValues = getSveltosValues(opts, env)
	}

	if globalVals := getGlobalValues(name, opts, env); globalVals != nil {
		componentValues = chartutil.CoalesceTables(componentValues, globalVals)
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

func getSveltosValues(opts ReconcileComponentsOpts, env envConfig) chartutil.Values {
	projectsveltos := make(map[string]any)
	projectsveltos["registerMgmtClusterJob"] = map[string]any{
		"registerMgmtCluster": map[string]any{
			"args": []string{
				"--labels=" + kcmv1.K0rdentManagementClusterLabelKey + "=" + kcmv1.K0rdentManagementClusterLabelValue,
			},
		},
	}

	agentPatchData := make(map[string]any)
	driftDetectionManagerPatchData := make(map[string]any)
	addonControllerValues := make(map[string]any)
	classifierManagerValues := make(map[string]any)

	if opts.ImagePullSecretName != nil && *opts.ImagePullSecretName != "" {
		//nolint:perfsprint // to preserve the formatting and readability
		imagePatch := fmt.Sprintf(`patch: |-
  - op: add
    path: /spec/template/spec/imagePullSecrets
    value:
    - name: %s`, *opts.ImagePullSecretName)
		agentPatchData["image-patch"] = imagePatch
		driftDetectionManagerPatchData["image-patch"] = imagePatch
	}

	if env.providersReloadSet {
		reloaderValue := strconv.FormatBool(env.providersReloadEnabled)
		reloaderAnnotations := map[string]any{
			"reloader.stakater.com/auto": reloaderValue,
		}

		addonControllerValues["annotations"] = reloaderAnnotations
		projectsveltos["accessManager"] = map[string]any{
			"manager": map[string]any{
				"annotations": reloaderAnnotations,
			},
			// sc-manager
			"annotations": reloaderAnnotations, //nolint:goconst // no need
		}
		projectsveltos["scManager"] = map[string]any{
			"annotations": reloaderAnnotations,
		}
		projectsveltos["hcManager"] = map[string]any{
			"annotations": reloaderAnnotations,
		}
		projectsveltos["eventManager"] = map[string]any{
			"annotations": reloaderAnnotations,
		}
		projectsveltos["shardController"] = map[string]any{
			"annotations": reloaderAnnotations,
		}
		projectsveltos["techsupportController"] = map[string]any{
			"annotations": reloaderAnnotations,
		}
		projectsveltos["mcpServer"] = map[string]any{
			"annotations": reloaderAnnotations,
		}
		classifierManagerValues["annotations"] = reloaderAnnotations

		reloaderPatch := fmt.Sprintf(`patch: |-
  apiVersion: apps/v1
  kind: Deployment
  metadata:
    name: required-by-kustomize
    annotations:
      reloader.stakater.com/auto: %q`, reloaderValue)
		agentPatchData["reloader-annotation-patch"] = reloaderPatch
		driftDetectionManagerPatchData["reloader-annotation-patch"] = reloaderPatch
	}

	if len(driftDetectionManagerPatchData) != 0 {
		addonControllerValues["driftDetectionManagerPatchConfigMap"] = map[string]any{
			"data": driftDetectionManagerPatchData,
		}
	}

	if len(addonControllerValues) != 0 {
		projectsveltos["addonController"] = addonControllerValues
	}

	if len(agentPatchData) != 0 {
		classifierManagerValues["agentPatchConfigMap"] = map[string]any{
			"data": agentPatchData,
		}
	}

	if len(classifierManagerValues) != 0 {
		projectsveltos["classifierManager"] = classifierManagerValues
	}

	return map[string]any{
		"projectsveltos": projectsveltos,
	}
}

func getGlobalValues(
	name string,
	opts ReconcileComponentsOpts,
	env envConfig,
) chartutil.Values {
	if !env.proxySet && len(opts.GlobalRegistry) == 0 && opts.ImagePullSecretName == nil && !env.providersReloadSet {
		return nil
	}

	vals := make(map[string]any)
	global := make(map[string]any)

	if opts.GlobalRegistry != "" {
		global["registry"] = opts.GlobalRegistry
	}

	if opts.ImagePullSecretName != nil && *opts.ImagePullSecretName != "" {
		global["imagePullSecrets"] = []map[string]any{
			{
				"name": *opts.ImagePullSecretName, //nolint:goconst // no need
			},
		}
	} else if opts.ImagePullSecretName != nil {
		global["imagePullSecrets"] = []map[string]any{}
	}

	if env.proxySet && name != kcmv1.ProviderSveltosName {
		global["proxy"] = env.proxyValues
	}

	if env.providersReloadSet && name != kcmv1.ProviderSveltosName {
		global["enableProvidersReload"] = env.providersReloadEnabled
	}

	if len(global) > 0 {
		vals["global"] = global
	}

	return vals
}

type envConfig struct {
	proxyValues            map[string]string
	providersReloadEnabled bool
	proxySet               bool
	providersReloadSet     bool
}

func loadEnvConfig() envConfig {
	proxyValues, proxySet := getProxyConfig()
	providersReloadEnabled, providersReloadSet := getEnableProvidersReloadConfig()
	return envConfig{
		proxyValues:            proxyValues,
		proxySet:               proxySet,
		providersReloadEnabled: providersReloadEnabled,
		providersReloadSet:     providersReloadSet,
	}
}

func getProxyConfig() (map[string]string, bool) {
	secretName, ok := os.LookupEnv(kubeutil.ProxySecretEnvName)
	if !ok || len(secretName) == 0 {
		return nil, false
	}
	return map[string]string{
		"secretName": secretName, //nolint:goconst // no need
	}, true
}

func getEnableProvidersReloadConfig() (enabled, set bool) {
	raw, set := os.LookupEnv(kubeutil.EnableProvidersReloadEnvName)
	if !set {
		return false, false
	}

	val, _ := strconv.ParseBool(raw)

	return val, true
}

func certManagerInstalled(ctx context.Context, restConfig *rest.Config, namespace string) error {
	if restConfig == nil {
		return errors.New("rest config is nil")
	}
	return certmanager.VerifyAPI(ctx, restConfig, namespace)
}

func processRegionalComponentValues(
	ctx context.Context,
	values chartutil.Values,
	opts ReconcileComponentsOpts,
) (map[string]any, error) {
	l := ctrl.LoggerFrom(ctx)

	const (
		certManagerComp        = "cert-manager"
		clusterAPIOperatorComp = "cluster-api-operator"
		veleroComp             = "velero"
		telemetryComp          = "telemetry"
	)
	components := [4]string{certManagerComp, clusterAPIOperatorComp, veleroComp, telemetryComp}
	componentValues := make(map[string]map[string]any, len(components))
	for _, component := range components {
		componentConfig, err := getCurrentValuesForKey(values, component)
		if err != nil {
			return nil, fmt.Errorf("failed to get current values for the %s component: %w", component, err)
		}

		componentValues[component] = componentConfig
	}

	capiOperatorValues := componentValues[clusterAPIOperatorComp]
	veleroValues := componentValues[veleroComp]

	if !opts.CertManagerInstalled {
		l.Info("Waiting for Cert manager API before enabling additional components")
	} else {
		l.Info("Cert manager is installed, enabling additional components")
		// enabling components unless explicitly disabled in values
		if isComponentEnabled(veleroValues) {
			veleroValues["enabled"] = true
		}
		if isComponentEnabled(capiOperatorValues) {
			capiOperatorValues["enabled"] = true
		}
	}

	if opts.RegistryCertSecretName != "" {
		capiOperatorValues = chartutil.CoalesceTables(capiOperatorValues, processCAPIOperatorCertVolumeMounts(capiOperatorValues, opts.RegistryCertSecretName))
	}

	processedValues := make(map[string]any, len(components))
	processedValues[clusterAPIOperatorComp] = capiOperatorValues
	processedValues[veleroComp] = veleroValues
	processedValues[certManagerComp] = componentValues[certManagerComp]
	processedValues[telemetryComp] = componentValues[telemetryComp]

	return processedValues, nil
}

func isComponentEnabled(values chartutil.Values) bool {
	if values == nil {
		return true
	}

	if enabled, ok := values["enabled"].(bool); ok {
		return enabled
	}

	return true
}

// getCurrentValuesForKey looks up a key in currentValues and returns it as map[string]any.
// If the key does not exist or currentValues is nil, it returns en empty map.
// If the value exists but cannot be cast to map[string]any, an error is returned.
func getCurrentValuesForKey(currentValues chartutil.Values, key string) (map[string]any, error) {
	if currentValues == nil {
		return make(map[string]any), nil
	}
	raw, ok := currentValues[key]
	if !ok {
		return make(map[string]any), nil
	}
	v, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value for key %q has unexpected type %T, expected map[string]any", key, raw)
	}
	return v, nil
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

func getProviderConfigSecretName(componentName string) string {
	return componentName + "-variables"
}
