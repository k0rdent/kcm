{{- define "cluster.name" -}}
    {{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "openstackmachinetemplate.controlplane.name" -}}
    {{- include "cluster.name" . }}-cp-mt-{{ .Values.controlPlane.image | toString | sha256sum | trunc 8 }}
{{- end }}

{{- define "openstackmachinetemplate.worker.name" -}}
    {{- include "cluster.name" . }}-worker-mt-{{ .Values.worker.image | toString | sha256sum | trunc 8 }}
{{- end }}

{{- define "k0scontrolplane.name" -}}
    {{- include "cluster.name" . }}-cp
{{- end }}

{{- define "k0sworkerconfigtemplate.name" -}}
    {{- include "cluster.name" . }}-machine-config
{{- end }}

{{- define "machinedeployment.name" -}}
    {{- include "cluster.name" . }}-md
{{- end }}
