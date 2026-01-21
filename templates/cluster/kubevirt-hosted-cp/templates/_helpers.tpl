{{- define "cluster.name" -}}
    {{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "kubevirtmachinetemplate.worker.name" -}}
    {{- include "cluster.name" . }}-mt
{{- end }}

{{- define "k0smotroncontrolplane.name" -}}
    {{- include "cluster.name" . }}-cp
{{- end }}

{{- define "k0sworkerconfigtemplate.name" -}}
    {{- include "cluster.name" . }}-machine-config
{{- end }}

{{- define "machinedeployment.name" -}}
    {{- include "cluster.name" . }}-md
{{- end }}

{{- define "authentication-config.fullpath" -}}
    {{- include "authentication-config.dir" . }}/{{- include "authentication-config.file" . }}
{{- end }}

{{- define "authentication-config.dir" -}}
    /var/lib/k0s/auth
{{- end }}

{{- define "authentication-config.file" -}}
    {{- if .Values.auth.configSecret.hash -}}
    config-{{ .Values.auth.configSecret.hash }}.yaml
    {{- else -}}
    config.yaml
    {{- end -}}
{{- end }}

{{/*
Build disks settings
*/}}
{{- define "devices.disks" -}}
{{- if .dataVolumes -}}
{{- range .dataVolumes -}}
- disk:
    bus: virtio
  name: {{ .name }}
{{- end -}}
{{- else -}}
- disk:
    bus: virtio
  name: containervolume
{{ if .cloudInit.userData -}}
- disk:
    bus: virtio
  name: cloudinitdisk
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Build volumes settings
*/}}
{{- define "volumes.default" -}}
- name: containervolume
  containerDisk:
    image: {{ .image }}
    {{- if .imagePullPolicy }}
    imagePullPolicy: {{ .imagePullPolicy }}
    {{- end }}
{{ if .cloudInit.userData -}}
- name: cloudinitdisk
  cloudInitNoCloud:
    userData: {{ .cloudInit.userData | quote }}
{{- end -}}
{{- end -}}
