{{- define "cluster.name" -}}
    {{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "gcpmanagedcontrolplane.name" -}}
    {{- include "cluster.name" . }}-cp
{{- end }}

{{- define "machinepool.name" -}}
    {{- include "cluster.name" . }}-mp
{{- end }}
