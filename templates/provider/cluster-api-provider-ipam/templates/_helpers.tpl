{{/*
Build proxy env vars if global.proxy is set
*/}}
{{- define "ipamProvider.proxyEnv" -}}
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
{{- define "ipamProvider.deployment.default" -}}
{{- $global := .Values.global | default dict -}}
{{- $version := .Chart.AppVersion -}}

{{- $deployment := dict -}}
{{- $container := dict "name" "manager" -}}

{{- /* Image */ -}}
{{- if $global.registry }}
{{- $_ := set $container "imageUrl" (printf "%s/capi/cluster-api-ipam-in-cluster-controller:%s" $global.registry $version) -}}
{{- end }}

{{- /* Proxy env vars */ -}}
{{- $proxyEnv := include "ipamProvider.proxyEnv" . | fromYaml -}}
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
Merge default deployment settings with user-provided overrides
*/}}
{{- define "ipamProvider.deployment" -}}
{{ toYaml (merge (.Values.deployment | default dict) (include "ipamProvider.deployment.default" . | fromYaml | default dict)) }}
{{- end }}

{{/*
Build default IPAM provider patches
*/}}
{{- define "ipamProvider.patches.default" -}}
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
Merge default IPAM provider patches with user-provided overrides
*/}}
{{- define "ipamProvider.patches" -}}
{{- $defaultYAML := include "ipamProvider.patches.default" . -}}
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
