{{/*
Expand the name of the chart.
*/}}
{{- define "hmc.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "hmc.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "hmc.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "hmc.labels" -}}
helm.sh/chart: {{ include "hmc.chart" . }}
{{ include "hmc.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
k0rdent.mirantis.com/component: hmc
{{- end }}

{{/*
Selector labels
*/}}
{{- define "hmc.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hmc.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
The name of the webhook service
*/}}
{{- define "hmc.webhook.serviceName" -}}
{{ include "hmc.fullname" . }}-webhook-service
{{- end }}

{{/*
The namespace of the webhook service
*/}}
{{- define "hmc.webhook.serviceNamespace" -}}
{{ .Release.Namespace }}
{{- end }}

{{/*
The name of the webhook certificate
*/}}
{{- define "hmc.webhook.certName" -}}
{{ include "hmc.fullname" . }}-webhook-serving-cert
{{- end }}

{{/*
The namespace of the webhook certificate
*/}}
{{- define "hmc.webhook.certNamespace" -}}
{{ .Release.Namespace }}
{{- end }}

{{/*
The name of the secret with webhook certificate
*/}}
{{- define "hmc.webhook.certSecretName" -}}
{{ include "hmc.fullname" . }}-webhook-serving-cert
{{- end }}


{{/*
The name of the webhook port. Must be no more than 15 characters
*/}}
{{- define "hmc.webhook.portName" -}}
hmc-webhook
{{- end }}

{{- define "rbac.editorVerbs" -}}
- create
- delete
- get
- list
- patch
- update
- watch
{{- end -}}

{{- define "rbac.viewerVerbs" -}}
- get
- list
- watch
{{- end -}}

{{- define "backup.imageName" -}}
{{- if (.Values.controller.backup.image.fullName) -}}
{{- .Values.controller.backup.image.fullName -}}
{{- else -}}
{{- printf "%s/%s:%s" .Values.controller.backup.image.repository .Values.controller.backup.image.name .Values.controller.backup.image.tag -}}
{{- end -}}
{{- end -}}
