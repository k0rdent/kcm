{{- define "cluster.name" -}}
    {{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "awsmachinetemplate.worker.name" -}}
    {{- include "cluster.name" . }}-worker-mt
{{- end }}

{{- define "machinedeployment.name" -}}
    {{- include "cluster.name" . }}-md
{{- end }}

{{- define "awsmanagedcontrolplane.name" -}}
    {{- include "cluster.name" . }}-cp
{{- end }}

{{- define "nodeamdconfigtemplate.name" -}}
    {{- include "cluster.name" . }}-machine-config
{{- end }}
