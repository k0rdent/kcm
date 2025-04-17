{{- define "featureGates.default" -}}
ExternalResourceGC: true
{{- end }}

{{/*
Merge .Values.manager.featureGates with the default
*/}}
{{- define "featureGates" -}}
{{ toYaml (merge (.Values.manager.featureGates | default dict) (include "featureGates.default" . | fromYaml)) }}
{{- end }}

{{/*
Manager settings with the default feature gates
*/}}
{{- define "spec.manager" -}}
{{- $manager := deepCopy .Values.manager }}
{{- $_ := set $manager "featureGates" (include "featureGates" . | fromYaml) }}
{{- toYaml $manager }}
{{- end }}
