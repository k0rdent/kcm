{{/*
Build the default deployment settings when global.registry is set
*/}}
{{- define "ipamProvider.deployment.default" -}}
{{- $global := .Values.global | default dict -}}
{{- $version := .Chart.AppVersion -}}
{{- if $global.registry -}}
containers:
  - name: manager
    imageUrl: {{ printf "%s/capi/cluster-api-ipam-provider-infoblox:%s" $global.registry $version }}
{{- if $global.imagePullSecrets }}
imagePullSecrets: {{ toYaml $global.imagePullSecrets | nindent 2 }}
{{- end }}
{{- end -}}
{{- end }}

{{/*
Merge default deployment settings with user-provided overrides
*/}}
{{- define "ipamProvider.deployment" -}}
{{ toYaml (merge (.Values.deployment | default dict) (include "ipamProvider.deployment.default" . | fromYaml | default dict)) }}
{{- end }}
