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
{{- define "provider.deployment.default" -}}
{{- $global := .Values.global | default dict -}}
{{- $version := .Chart.AppVersion -}}

{{- $deployment := dict -}}
{{- $container := dict "name" "manager" -}}

{{- /* Image */ -}}
{{- if $global.registry }}
{{- $_ := set $container "imageUrl" (printf "%s/capi/k0smotron:%s" $global.registry $version) -}}
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
Merge default infrastructure provider deployment settings with user-provided overrides
*/}}
{{- define "infrastructureProvider.deployment" -}}
{{ toYaml (merge (.Values.infrastructure.deployment | default dict) (include "provider.deployment.default" . | fromYaml | default dict)) }}
{{- end }}

{{/*
Merge default bootstrap provider deployment settings with user-provided overrides
*/}}
{{- define "bootstrapProvider.deployment" -}}
{{ toYaml (merge (.Values.bootstrap.deployment | default dict) (include "provider.deployment.default" . | fromYaml | default dict)) }}
{{- end }}

{{/*
Merge default control plane provider deployment settings with user-provided overrides
*/}}
{{- define "controlPlaneProvider.deployment" -}}
{{ toYaml (merge (.Values.controlPlane.deployment | default dict) (include "provider.deployment.default" . | fromYaml | default dict)) }}
{{- end }}
