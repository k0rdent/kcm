{{/*
Build proxy env vars if global.proxy is set
*/}}
{{- define "infrastructureProvider.proxyEnv" -}}
{{- $global := .Values.global | default dict -}}
{{- $proxy := $global.proxy | default dict -}}
{{- $localProxy := .Values.proxy | default dict -}}
{{- if and $localProxy.enabled $proxy.secretName }}
env:
  - name: HTTP_PROXY
    valueFrom:
      secretKeyRef:
        name: {{ $proxy.secretName }}
        key: HTTP_PROXY
        optional: true
  - name: http_proxy
    valueFrom:
      secretKeyRef:
        name: {{ $proxy.secretName }}
        key: HTTP_PROXY
        optional: true
  - name: HTTPS_PROXY
    valueFrom:
      secretKeyRef:
        name: {{ $proxy.secretName }}
        key: HTTPS_PROXY
        optional: true
  - name: https_proxy
    valueFrom:
      secretKeyRef:
        name: {{ $proxy.secretName }}
        key: HTTPS_PROXY
        optional: true
  - name: NO_PROXY
    valueFrom:
      secretKeyRef:
        name: {{ $proxy.secretName }}
        key: NO_PROXY
        optional: true
  - name: no_proxy
    valueFrom:
      secretKeyRef:
        name: {{ $proxy.secretName }}
        key: NO_PROXY
        optional: true
{{- end }}
{{- end }}

{{/*
Build the default deployment settings
*/}}
{{- define "infrastructureProvider.deployment.default" -}}
{{- $global := .Values.global | default dict -}}
{{- $version := .Chart.AppVersion -}}

{{- $deployment := dict -}}
{{- $container := dict "name" "manager" -}}

{{- /* Image */ -}}
{{- if $global.registry }}
{{- $_ := set $container "imageUrl" (printf "%s/capi/cluster-api-azure-controller:%s" $global.registry $version) -}}
{{- end }}

{{- /* Proxy env vars */ -}}
{{- $proxyEnv := include "infrastructureProvider.proxyEnv" . | fromYaml -}}
{{- if $proxyEnv }}
{{- $_ := set $container "env" $proxyEnv.env -}}
{{- end }}

{{- /* Add container only if something was configured */ -}}
{{- if gt (len $container) 1 }}
{{- $_ := set $deployment "containers" (list $container) -}}
{{- end }}

{{- /* Image pull secrets */ -}}
{{- if $global.imagePullSecrets }}
{{- $_ := set $deployment "imagePullSecrets" $global.imagePullSecrets -}}
{{- end }}

{{- toYaml $deployment -}}
{{- end }}

{{/*
Merge default deployment settings with user-provided overrides
*/}}
{{- define "infrastructureProvider.deployment" -}}
{{ toYaml (merge (.Values.deployment | default dict) (include "infrastructureProvider.deployment.default" . | fromYaml | default dict)) }}
{{- end }}

{{/*
Build default infrastructure provider patches
*/}}
{{/*
VERY IMPORTANT WARNING:
These ASO selector patches MUST stay in place.

Why:
- CAPZ ASO upstream manifests use control-plane=controller-manager.
- In mixed-provider environments this collides with k0smotron webhook Service selectors.
- Without these patches, CAPZ ASO webhook traffic can be routed to the k0smotron webhook Service.
- That request path fails TLS/x509 verification because certificates do not match the expected webhook Service.

Do not remove or relax these selector patches unless CAPZ upstream changes selector/label strategy
or the providers are guaranteed to be isolated so selector collisions cannot happen.
*/}}
{{- define "infrastructureProvider.patches.default" -}}
{{- $global := .Values.global | default dict -}}
{{- $asoVersion := "v2.13.0" -}}
{{- $proxyEnv := include "infrastructureProvider.proxyEnv" . | fromYaml -}}
{{- $enableProvidersReloadSet := hasKey $global "enableProvidersReload" -}}
{{- $enableProvidersReload := and $enableProvidersReloadSet $global.enableProvidersReload -}}
- patch: |
    - op: add
      path: /spec/selector/control-plane
      value: aso-controller-manager
  target:
    version: v1
    kind: Service
    name: azureserviceoperator-webhook-service
    namespace: {{ .Release.Namespace }}
- patch: |
    - op: add
      path: /spec/selector/matchLabels/control-plane
      value: aso-controller-manager
    - op: add
      path: /spec/template/metadata/labels/control-plane
      value: aso-controller-manager
  target:
    group: apps
    version: v1
    kind: Deployment
    name: azureserviceoperator-controller-manager
    namespace: {{ .Release.Namespace }}
- patch: |
    - op: test
      path: /spec/template/spec/volumes/0/name
      value: cert
    - op: add
      path: /spec/template/spec/volumes/0/secret/secretName
      value: aso-webhook-server-cert
  target:
    group: apps
    version: v1
    kind: Deployment
    name: azureserviceoperator-controller-manager
    namespace: {{ .Release.Namespace }}
- patch: |
    - op: add
      path: /spec/secretName
      value: aso-webhook-server-cert
  target:
    group: cert-manager.io
    version: v1
    kind: Certificate
    name: azureserviceoperator-serving-cert
    namespace: {{ .Release.Namespace }}
{{- if $global.registry }}
- patch: |
    - op: replace
      path: /spec/template/spec/containers/0/image
      value: {{ $global.registry }}/k8s/azureserviceoperator:{{ $asoVersion }}
  target:
    group: apps
    version: v1
    kind: Deployment
    name: azureserviceoperator-controller-manager
    namespace: {{ .Release.Namespace }}
{{- end }}
{{- if $global.imagePullSecrets }}
- patch: |
    - op: add
      path: /spec/template/spec/imagePullSecrets
      value:
{{ toYaml $global.imagePullSecrets | indent 8 }}
  target:
    group: apps
    version: v1
    kind: Deployment
    name: azureserviceoperator-controller-manager
    namespace: {{ .Release.Namespace }}
{{- else }}
- patch: |
    - op: add
      path: /spec/template/spec/imagePullSecrets
      value:
        []
  target:
    group: apps
    version: v1
    kind: Deployment
    namespace: {{ .Release.Namespace }}
{{- end }}
{{- if $proxyEnv }}
- patch: |
{{- range $env := $proxyEnv.env }}
    - op: add
      path: /spec/template/spec/containers/0/env/-
      value:
{{ toYaml $env | indent 8 }}
{{- end }}
  target:
    group: apps
    version: v1
    kind: Deployment
    name: azureserviceoperator-controller-manager
    namespace: {{ .Release.Namespace }}
{{- end }}
{{- if $enableProvidersReloadSet }}
- patch: |
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      annotations:
        reloader.stakater.com/auto: {{ if $enableProvidersReload }}"true"{{ else }}"false"{{ end }}
  target:
    group: apps
    version: v1
    kind: Deployment
    namespace: {{ .Release.Namespace }}
{{- end }}
{{- end }}

{{/*
Merge default infrastructure provider patches with user-provided overrides
*/}}
{{- define "infrastructureProvider.patches" -}}
{{- $defaultYAML := include "infrastructureProvider.patches.default" . -}}
{{- $default := list -}}
{{- if ne (trim $defaultYAML) "" -}}
{{- $default = ($defaultYAML | fromYamlArray) -}}
{{- end -}}
{{- $user := .Values.patches | default list -}}
{{- if not (kindIs "slice" $user) -}}
{{- $user = list -}}
{{- end -}}
{{- $items := concat $user $default -}}
{{- if gt (len $items) 0 -}}
{{- toYaml $items -}}
{{- end -}}
{{- end }}
