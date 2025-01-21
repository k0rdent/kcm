{{- define "cluster.name" -}}
    {{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "machinepool.system.name" -}}
    {{- include "cluster.name" . }}-system
{{- end }}

{{- define "machinepool.user.name" -}}
    {{- include "cluster.name" . }}-user
{{- end }}
