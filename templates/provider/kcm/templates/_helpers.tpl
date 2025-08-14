{{/*
Expand the name of the chart.
*/}}
{{- define "kcm.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "kcm.fullname" -}}
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
Chart name and namespace annotations to be explixcitly set for projectsveltos CRDs
*/}}
{{- define "kcm.annotations" -}}
meta.helm.sh/release-name: {{ .Release.Name }}
meta.helm.sh/release-namespace: {{ .Release.Namespace }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "kcm.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kcm.labels" -}}
helm.sh/chart: {{ include "kcm.chart" . }}
{{ include "kcm.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
k0rdent.mirantis.com/component: kcm
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kcm.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kcm.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
The name of the webhook service
*/}}
{{- define "kcm.webhook.serviceName" -}}
{{ include "kcm.fullname" . }}-webhook-service
{{- end }}

{{/*
The namespace of the webhook service
*/}}
{{- define "kcm.webhook.serviceNamespace" -}}
{{ .Release.Namespace }}
{{- end }}

{{/*
The name of the webhook certificate
*/}}
{{- define "kcm.webhook.certName" -}}
{{ include "kcm.fullname" . }}-webhook-serving-cert
{{- end }}

{{/*
The namespace of the webhook certificate
*/}}
{{- define "kcm.webhook.certNamespace" -}}
{{ .Release.Namespace }}
{{- end }}

{{/*
The name of the secret with webhook certificate
*/}}
{{- define "kcm.webhook.certSecretName" -}}
{{ include "kcm.fullname" . }}-webhook-serving-cert
{{- end }}


{{/*
The name of the webhook port. Must be no more than 15 characters
*/}}
{{- define "kcm.webhook.portName" -}}
kcm-webhook
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

{{/*
The name of the telemetry PVC
*/}}
{{- define "kcm.telemetry.claimName" -}}
{{- $vol := .Values.telemetry.local.volume -}}
{{- if and (eq $vol.source "existing") $vol.existingClaim -}}
{{- $vol.existingClaim -}}
{{- else -}}
{{- printf "%s-telemetry" (include "kcm.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
The name of the telemetry PV
*/}}
{{- define "kcm.telemetry.volumeName" -}}
{{- if .Values.telemetry.local.volume.hostPath.name -}}
{{- .Values.telemetry.local.volume.hostPath.name -}}
{{- else -}}
{{- printf "%s-telemetry-data" (include "kcm.fullname" .) -}}
{{- end -}}
{{- end -}}
