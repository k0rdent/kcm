{{/*
Build proxy env vars if global.proxy is set
*/}}
{{- define "infrastructureProvider.proxyEnv" -}}
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
{{- define "infrastructureProvider.deployment.default" -}}
{{- $global := .Values.global | default dict -}}
{{- $version := .Chart.AppVersion -}}

{{- $deployment := dict -}}
{{- $container := dict "name" "manager" -}}

{{- /* Image */ -}}
{{- if $global.registry }}
{{- $_ := set $container "imageUrl" (printf "%s/capi/capi-openstack-controller:%s" $global.registry $version) -}}
{{- end }}

{{- /* Proxy env vars */ -}}
{{- $proxyEnv := include "infrastructureProvider.proxyEnv" . | fromYaml -}}
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
{{- define "infrastructureProvider.deployment.merge" -}}
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
Merge default deployment settings with user-provided overrides
*/}}
{{- define "infrastructureProvider.deployment" -}}
{{- $default := include "infrastructureProvider.deployment.default" . | fromYaml | default dict -}}
{{- $user := .Values.deployment | default dict -}}
{{- include "infrastructureProvider.deployment.merge" (dict "default" $default "user" $user) -}}
{{- end }}

{{/*
Build default infrastructure provider patches
*/}}
{{- define "infrastructureProvider.patches.default" -}}
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
Merge default infrastructure provider patches with user-provided overrides
*/}}
{{- define "infrastructureProvider.patches" -}}
{{- $defaultYAML := include "infrastructureProvider.patches.default" . -}}
{{- $default := list -}}
{{- if ne (trim $defaultYAML) "" -}}
{{- $default = ($defaultYAML | fromYamlArray) -}}
{{- end -}}
{{- $user := .Values.patches | default list -}}
{{- if not (kindIs "slice" $user) -}}
{{- $user = list -}}
{{- end -}}
{{- $items := concat $user $default -}}
{{- if gt (len $items) 0 -}}
{{- toYaml $items -}}
{{- end -}}
{{- end }}
