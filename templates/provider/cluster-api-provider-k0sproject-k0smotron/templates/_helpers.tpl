
{{/*
Build the default deployment settings when global.registry is set
*/}}
{{- define "provider.deployment.default" -}}
{{- $global := .Values.global | default dict -}}
{{- $version := .Chart.AppVersion -}}
{{- if $global.registry -}}
containers:
  - name: manager
    imageUrl: {{ printf "%s/capi/k0smotron:%s" $global.registry $version }}
{{- if $global.imagePullSecrets }}
imagePullSecrets: {{ toYaml $global.imagePullSecrets | nindent 2 }}
{{- end }}
{{- end -}}
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
