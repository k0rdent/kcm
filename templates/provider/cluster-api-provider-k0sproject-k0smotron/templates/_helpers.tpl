{{/*
Build proxy env vars if global.proxy is set
*/}}
{{- define "provider.proxyEnv" -}}
{{- $global := .Values.global | default dict -}}
{{- $proxy := $global.proxy | default dict -}}
{{- $localProxy := .Values.proxy | default dict -}}
{{- if and $localProxy.enabled $proxy.secretName }}
env:
  - name: HTTP_PROXY
    valueFrom:
      secretKeyRef:
        name: {{ $proxy.secretName }}
        key: HTTP_PROXY
        optional: true
  - name: http_proxy
    valueFrom:
      secretKeyRef:
        name: {{ $proxy.secretName }}
        key: HTTP_PROXY
        optional: true
  - name: HTTPS_PROXY
    valueFrom:
      secretKeyRef:
        name: {{ $proxy.secretName }}
        key: HTTPS_PROXY
        optional: true
  - name: https_proxy
    valueFrom:
      secretKeyRef:
        name: {{ $proxy.secretName }}
        key: HTTPS_PROXY
        optional: true
  - name: NO_PROXY
    valueFrom:
      secretKeyRef:
        name: {{ $proxy.secretName }}
        key: NO_PROXY
        optional: true
  - name: no_proxy
    valueFrom:
      secretKeyRef:
        name: {{ $proxy.secretName }}
        key: NO_PROXY
        optional: true
{{- end }}
{{- end }}

{{/*
Build the default deployment settings
*/}}
{{- define "provider.deployment.default" -}}
{{- $global := .Values.global | default dict -}}
{{- $version := .Chart.AppVersion -}}

{{- $deployment := dict -}}
{{- $container := dict "name" "manager" -}}

{{- /* Image */ -}}
{{- if $global.registry }}
{{- $_ := set $container "imageUrl" (printf "%s:%s" (include "k0smotron.image.repository" .) $version) -}}
{{- end }}

{{- /* Proxy env vars */ -}}
{{- $proxyEnv := include "provider.proxyEnv" . | fromYaml -}}
{{- if $proxyEnv }}
{{- $_ := set $container "env" $proxyEnv.env -}}
{{- end }}

{{- /* Add container only if something was configured */ -}}
{{- if gt (len $container) 1 }}
{{- $_ := set $deployment "containers" (list $container) -}}
{{- end }}

{{- /* Image pull secrets */ -}}
{{- if $global.imagePullSecrets }}
{{- $_ := set $deployment "imagePullSecrets" $global.imagePullSecrets -}}
{{- end }}

{{- toYaml $deployment -}}
{{- end }}

{{/*
Merge deployment settings while preserving default containers.
Containers are merged by name with user values taking precedence.
*/}}
{{- define "provider.deployment.merge" -}}
{{- $default := .default | default dict -}}
{{- $user := .user | default dict -}}
{{- $merged := merge (deepCopy $user) (deepCopy $default) -}}

{{- $hasDefaultContainers := hasKey $default "containers" -}}
{{- $hasUserContainers := hasKey $user "containers" -}}
{{- if or $hasDefaultContainers $hasUserContainers -}}
{{- $defaultContainers := get $default "containers" | default list -}}
{{- $userContainers := get $user "containers" | default list -}}
{{- if and (kindIs "slice" $defaultContainers) (kindIs "slice" $userContainers) -}}
{{- $userContainersByName := dict -}}
{{- range $userContainer := $userContainers -}}
{{- $name := $userContainer.name | default "" -}}
{{- if ne $name "" -}}
{{- $_ := set $userContainersByName $name $userContainer -}}
{{- end -}}
{{- end -}}

{{- $defaultNames := dict -}}
{{- $mergedContainers := list -}}
{{- range $defaultContainer := $defaultContainers -}}
{{- $name := $defaultContainer.name | default "" -}}
{{- if ne $name "" -}}
{{- $_ := set $defaultNames $name true -}}
{{- end -}}
{{- if and (ne $name "") (hasKey $userContainersByName $name) -}}
{{- $mergedContainer := merge (deepCopy (get $userContainersByName $name)) (deepCopy $defaultContainer) -}}
{{- $hasDefaultEnv := hasKey $defaultContainer "env" -}}
{{- $userContainer := get $userContainersByName $name -}}
{{- $hasUserEnv := hasKey $userContainer "env" -}}
{{- if or $hasDefaultEnv $hasUserEnv -}}
{{- $defaultEnv := get $defaultContainer "env" | default list -}}
{{- $userEnv := get $userContainer "env" | default list -}}
{{- if and (kindIs "slice" $defaultEnv) (kindIs "slice" $userEnv) -}}
{{- $userEnvByName := dict -}}
{{- range $item := $userEnv -}}
{{- $envName := $item.name | default "" -}}
{{- if ne $envName "" -}}
{{- $_ := set $userEnvByName $envName $item -}}
{{- end -}}
{{- end -}}

{{- $defaultEnvNames := dict -}}
{{- $mergedEnv := list -}}
{{- range $item := $defaultEnv -}}
{{- $envName := $item.name | default "" -}}
{{- if ne $envName "" -}}
{{- $_ := set $defaultEnvNames $envName true -}}
{{- end -}}
{{- if and (ne $envName "") (hasKey $userEnvByName $envName) -}}
{{- $mergedEnv = append $mergedEnv (get $userEnvByName $envName) -}}
{{- else -}}
{{- $mergedEnv = append $mergedEnv $item -}}
{{- end -}}
{{- end -}}

{{- range $item := $userEnv -}}
{{- $envName := $item.name | default "" -}}
{{- if or (eq $envName "") (not (hasKey $defaultEnvNames $envName)) -}}
{{- $mergedEnv = append $mergedEnv $item -}}
{{- end -}}
{{- end -}}

{{- $_ := set $mergedContainer "env" $mergedEnv -}}
{{- end -}}
{{- end -}}
{{- $mergedContainers = append $mergedContainers $mergedContainer -}}
{{- else -}}
{{- $mergedContainers = append $mergedContainers $defaultContainer -}}
{{- end -}}
{{- end -}}

{{- range $userContainer := $userContainers -}}
{{- $name := $userContainer.name | default "" -}}
{{- if or (eq $name "") (not (hasKey $defaultNames $name)) -}}
{{- $mergedContainers = append $mergedContainers $userContainer -}}
{{- end -}}
{{- end -}}

{{- $_ := set $merged "containers" $mergedContainers -}}
{{- end -}}
{{- end -}}

{{- $hasDefaultImagePullSecrets := hasKey $default "imagePullSecrets" -}}
{{- $hasUserImagePullSecrets := hasKey $user "imagePullSecrets" -}}
{{- if or $hasDefaultImagePullSecrets $hasUserImagePullSecrets -}}
{{- $defaultImagePullSecrets := get $default "imagePullSecrets" | default list -}}
{{- $userImagePullSecrets := get $user "imagePullSecrets" | default list -}}
{{- if and (kindIs "slice" $defaultImagePullSecrets) (kindIs "slice" $userImagePullSecrets) -}}
{{- $userImagePullSecretsByName := dict -}}
{{- range $secret := $userImagePullSecrets -}}
{{- $name := $secret.name | default "" -}}
{{- if ne $name "" -}}
{{- $_ := set $userImagePullSecretsByName $name $secret -}}
{{- end -}}
{{- end -}}

{{- $defaultImagePullSecretNames := dict -}}
{{- $mergedImagePullSecrets := list -}}
{{- range $secret := $defaultImagePullSecrets -}}
{{- $name := $secret.name | default "" -}}
{{- if ne $name "" -}}
{{- $_ := set $defaultImagePullSecretNames $name true -}}
{{- end -}}
{{- if and (ne $name "") (hasKey $userImagePullSecretsByName $name) -}}
{{- $mergedImagePullSecrets = append $mergedImagePullSecrets (get $userImagePullSecretsByName $name) -}}
{{- else -}}
{{- $mergedImagePullSecrets = append $mergedImagePullSecrets $secret -}}
{{- end -}}
{{- end -}}

{{- range $secret := $userImagePullSecrets -}}
{{- $name := $secret.name | default "" -}}
{{- if or (eq $name "") (not (hasKey $defaultImagePullSecretNames $name)) -}}
{{- $mergedImagePullSecrets = append $mergedImagePullSecrets $secret -}}
{{- end -}}
{{- end -}}

{{- $_ := set $merged "imagePullSecrets" $mergedImagePullSecrets -}}
{{- end -}}
{{- end -}}

{{- $hasDefaultTolerations := hasKey $default "tolerations" -}}
{{- $hasUserTolerations := hasKey $user "tolerations" -}}
{{- if or $hasDefaultTolerations $hasUserTolerations -}}
{{- $defaultTolerations := get $default "tolerations" | default list -}}
{{- $userTolerations := get $user "tolerations" | default list -}}
{{- if and (kindIs "slice" $defaultTolerations) (kindIs "slice" $userTolerations) -}}
{{- $_ := set $merged "tolerations" (concat $defaultTolerations $userTolerations) -}}
{{- end -}}
{{- end -}}

{{- toYaml $merged -}}
{{- end }}

{{/*
Merge default infrastructure provider deployment settings with user-provided overrides
*/}}
{{- define "infrastructureProvider.deployment" -}}
{{- $default := include "provider.deployment.default" . | fromYaml | default dict -}}
{{- $user := .Values.infrastructure.deployment | default dict -}}
{{- include "provider.deployment.merge" (dict "default" $default "user" $user) -}}
{{- end }}

{{/*
Merge default bootstrap provider deployment settings with user-provided overrides
*/}}
{{- define "bootstrapProvider.deployment" -}}
{{- $default := include "provider.deployment.default" . | fromYaml | default dict -}}
{{- $user := .Values.bootstrap.deployment | default dict -}}
{{- include "provider.deployment.merge" (dict "default" $default "user" $user) -}}
{{- end }}

{{/*
Merge default control plane provider deployment settings with user-provided overrides
*/}}
{{- define "controlPlaneProvider.deployment" -}}
{{- $default := include "provider.deployment.default" . | fromYaml | default dict -}}
{{- $user := .Values.controlPlane.deployment | default dict -}}
{{- include "provider.deployment.merge" (dict "default" $default "user" $user) -}}
{{- end }}

{{/*
Build default provider patches
*/}}
{{- define "provider.patches.default" -}}
{{- $global := .Values.global | default dict -}}
{{- if and (hasKey $global "imagePullSecrets") (not $global.imagePullSecrets) }}
- patch: |
    - op: add
      path: /spec/template/spec/imagePullSecrets
      value:
        []
  target:
    group: apps
    version: v1
    kind: Deployment
    namespace: {{ .Release.Namespace }}
{{- end }}
{{- if hasKey $global "enableProvidersReload" }}
- patch: |
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      annotations:
        reloader.stakater.com/auto: {{ if $global.enableProvidersReload }}"true"{{ else }}"false"{{ end }}
  target:
    group: apps
    version: v1
    kind: Deployment
    namespace: {{ .Release.Namespace }}
{{- end }}
{{- end }}

{{/*
Merge default provider patches with user-provided overrides
*/}}
{{- define "provider.patches" -}}
{{- $ctx := .context -}}
{{- $defaultYAML := include "provider.patches.default" $ctx -}}
{{- $default := list -}}
{{- if ne (trim $defaultYAML) "" -}}
{{- $default = ($defaultYAML | fromYamlArray) -}}
{{- end -}}
{{- $user := .userPatches | default list -}}
{{- if not (kindIs "slice" $user) -}}
{{- $user = list -}}
{{- end -}}
{{- $items := concat $user $default -}}
{{- if gt (len $items) 0 -}}
{{- toYaml $items -}}
{{- end -}}
{{- end }}

{{/*
Merge default infrastructure provider patches with user-provided overrides
*/}}
{{- define "infrastructureProvider.patches" -}}
{{- $infrastructure := .Values.infrastructure | default dict -}}
{{- $userPatches := $infrastructure.patches | default list -}}
{{ include "provider.patches" (dict "context" . "userPatches" $userPatches) }}
{{- end }}

{{/*
Merge default bootstrap provider patches with user-provided overrides
*/}}
{{- define "bootstrapProvider.patches" -}}
{{- $bootstrap := .Values.bootstrap | default dict -}}
{{- $userPatches := $bootstrap.patches | default list -}}
{{ include "provider.patches" (dict "context" . "userPatches" $userPatches) }}
{{- end }}

{{/*
Merge default control plane provider patches with user-provided overrides
*/}}
{{- define "controlPlaneProvider.patches" -}}
{{- $controlPlane := .Values.controlPlane | default dict -}}
{{- $userPatches := $controlPlane.patches | default list -}}
{{ include "provider.patches" (dict "context" . "userPatches" $userPatches) }}
{{- end }}

{{/*
Build default control plane provider feature gates
*/}}
{{- define "controlPlaneProvider.featureGates.default" -}}
{{- if eq (include "inPlaceUpdates.enabled" .) "true" }}
InPlaceUpdates: true
{{- end }}
{{- end }}

{{/*
Control plane provider manager settings with the default feature gates, user-provided feature gates take precedence
*/}}
{{- define "controlPlaneProvider.manager" -}}
{{- $controlPlane := .Values.controlPlane | default dict -}}
{{- $manager := deepCopy ($controlPlane.manager | default dict) -}}
{{- $default := include "controlPlaneProvider.featureGates.default" . | fromYaml | default dict -}}
{{- if $default -}}
{{- $featureGates := $manager.featureGates | default dict -}}
{{- range $gate, $value := $default -}}
{{- if not (hasKey $featureGates $gate) -}}
{{- $_ := set $featureGates $gate $value -}}
{{- end -}}
{{- end -}}
{{- $_ := set $manager "featureGates" $featureGates -}}
{{- end -}}
{{- toYaml $manager -}}
{{- end }}

{{/*
Name of the in-place version update extension webhook resources
*/}}
{{- define "extensionWebhook.name" -}}
k0smotron-extension-webhook
{{- end }}

{{/*
Repository of the k0smotron image shared by the providers managers and the extension webhook server
*/}}
{{- define "k0smotron.image.repository" -}}
{{- $global := .Values.global | default dict -}}
{{- if $global.registry -}}
{{- printf "%s/capi/k0smotron" $global.registry -}}
{{- else -}}
quay.io/k0sproject/k0smotron
{{- end -}}
{{- end }}

{{/*
Image of the in-place version update extension webhook server
*/}}
{{- define "extensionWebhook.image" -}}
{{- $image := .Values.inPlaceUpdates.extension.image -}}
{{- printf "%s:%s" ($image.repository | default (include "k0smotron.image.repository" .)) ($image.tag | default .Chart.AppVersion) -}}
{{- end }}

{{/*
Whether the in-place version update extension webhook should be deployed: in-place updates are enabled
and the resulting InPlaceUpdates feature gate of the controlplane provider is not explicitly disabled
*/}}
{{- define "extensionWebhook.enabled" -}}
{{- $featureGates := (include "controlPlaneProvider.manager" . | fromYaml).featureGates | default dict -}}
{{- and (eq (include "inPlaceUpdates.enabled" .) "true") (eq (toString (get $featureGates "InPlaceUpdates")) "true") -}}
{{- end }}

{{/*
Fails if the cluster-api CoreProvider in the cluster has either of the InPlaceUpdates or RuntimeSDK feature gates disabled,
otherwise the ExtensionConfig is rejected by the core provider or the controlplane provider gets stuck on the in-place updates.
The check is skipped if no CoreProvider is found, e.g. while rendering without a cluster
*/}}
{{- define "extensionWebhook.validateCoreProvider" -}}
{{- range (lookup "operator.cluster.x-k8s.io/v1alpha2" "CoreProvider" "" "").items -}}
{{- $featureGates := ((.spec | default dict).manager | default dict).featureGates | default dict -}}
{{- if not (and (eq (toString (get $featureGates "InPlaceUpdates")) "true") (eq (toString (get $featureGates "RuntimeSDK")) "true")) -}}
{{- fail (printf "in-place updates require the InPlaceUpdates and RuntimeSDK feature gates of the %s/%s CoreProvider, enable inPlaceUpdates in the cluster-api provider as well" .metadata.namespace .metadata.name) -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
Whether in-place updates are enabled, the chart value takes precedence over the global one
*/}}
{{- define "inPlaceUpdates.enabled" -}}
{{- $global := .Values.global | default dict -}}
{{- $enabled := (.Values.inPlaceUpdates | default dict).enabled -}}
{{- if kindIs "bool" $enabled -}}
{{- $enabled -}}
{{- else -}}
{{- $global.enableInPlaceUpdates | default false -}}
{{- end -}}
{{- end }}
