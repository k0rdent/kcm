{{/*
Expand the name of the chart.
*/}}
{{- define "kcm-regional.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "kcm-regional.fullname" -}}
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
{{- define "kcm-regional.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kcm-regional.labels" -}}
helm.sh/chart: {{ include "kcm-regional.chart" . }}
{{ include "kcm-regional.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kcm-regional.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kcm-regional.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
The name of the telemetry PVC
*/}}
{{- define "kcm-regional.telemetry.claimName" -}}
{{- $vol := .Values.telemetry.local.volume -}}
{{- if and (eq $vol.source "existing") $vol.existingClaim -}}
{{- $vol.existingClaim -}}
{{- else -}}
{{- printf "%s-telemetry" (include "kcm-regional.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
The name of the telemetry PV
*/}}
{{- define "kcm-regional.telemetry.volumeName" -}}
{{- if .Values.telemetry.local.volume.hostPath.name -}}
{{- .Values.telemetry.local.volume.hostPath.name -}}
{{- else -}}
{{- printf "%s-telemetry-data" (include "kcm-regional.fullname" .) -}}
{{- end -}}
{{- end -}}

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
