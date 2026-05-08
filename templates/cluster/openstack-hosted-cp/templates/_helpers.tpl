{{- define "cluster.name" -}}
    {{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "openstackmachinetemplate.name" -}}
    {{- include "cluster.name" . }}-mt-{{ .Values.image | toString | sha256sum | trunc 8 }}
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

{{- define "encryption-config.fullpath" -}}
    {{- include "encryption-config.dir" . }}/{{- include "encryption-config.file" . }}
{{- end }}

{{- define "encryption-config.dir" -}}
    /var/lib/k0s/encryption
{{- end }}

{{- define "encryption-config.hash" -}}
    {{- if .Values.encryption.configSecret.hash -}}
    {{- .Values.encryption.configSecret.hash -}}
    {{- else -}}
    {{- $secretName := .Values.encryption.configSecret.name -}}
    {{- $secretKey := default "config" .Values.encryption.configSecret.key -}}
    {{- if $secretName -}}
    {{- $secret := lookup "v1" "Secret" .Release.Namespace $secretName -}}
    {{- if and $secret $secret.data (hasKey $secret.data $secretKey) -}}
    {{- sha256sum (b64dec (index $secret.data $secretKey)) | trunc 8 -}}
    {{- end -}}
    {{- end -}}
    {{- end -}}
{{- end }}

{{- define "encryption-config.file" -}}
    {{- $hash := include "encryption-config.hash" . | trim -}}
    {{- if $hash -}}
    config-{{ $hash }}.yaml
    {{- else -}}
    config.yaml
    {{- end -}}
{{- end }}
