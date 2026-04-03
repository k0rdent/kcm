#!/usr/bin/env bash

# Copyright 2026
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

: "${CAPO_ORC_VERSION:?CAPO_ORC_VERSION must be set}"
: "${CAPO_ORC_TEMPLATE:?CAPO_ORC_TEMPLATE must be set}"

YQ_BIN="${YQ:-yq}"

mkdir -p "$(dirname "${CAPO_ORC_TEMPLATE}")"

curl -L --fail -sS \
  "https://github.com/k-orc/openstack-resource-controller/releases/download/v${CAPO_ORC_VERSION}/install.yaml" |
  "${YQ_BIN}" 'del(select(.kind == "Namespace"))' |
  "${YQ_BIN}" '(select(.metadata.namespace) | .metadata.namespace) = "{{ .Release.Namespace }}"' |
  "${YQ_BIN}" '(select(.kind == "ClusterRoleBinding" or .kind == "RoleBinding") | .subjects[].namespace) = "{{ .Release.Namespace }}"' |
  awk 'NR==1{print "{{- $global := .Values.global | default dict }}"}
{
  if ($0 ~ /controller-gen.kubebuilder.io\/version/) {
    print $0;
    print "    helm.sh/resource-policy: keep";
    next;
  }

  if ($0 ~ /^[ \t]*image: /) {
    line=$0; sub(/^[ \t]*image: /, "", line);
    n=split(line, arr, "/");
    if (n > 1) {
      registry = arr[1];
      image_path = "";
      for (i = 2; i <= n; i++) {
        image_path = image_path ((i > 2) ? "/" : "") arr[i];
      }
      print "          image: {{ default \"" registry "\" $global.registry }}/" image_path;
      next;
    }
  }

  print;

  if ($0 ~ /^[ \t]*name: manager$/) {
    print "          {{- $proxyEnv := include \"infrastructureProvider.proxyEnv\" . | fromYaml }}";
    print "          {{- if $proxyEnv }}";
    print "          env:";
    print "          {{ toYaml $proxyEnv.env | nindent 12 }}";
    print "          {{- end }}";
  }

  if ($0 ~ /serviceAccountName: orc-controller-manager/) {
    print "      {{- if $global.imagePullSecrets }}";
    print "      imagePullSecrets: {{ toYaml $global.imagePullSecrets | nindent 8 }}";
    print "      {{- end }}";
  }
}' >"${CAPO_ORC_TEMPLATE}"
