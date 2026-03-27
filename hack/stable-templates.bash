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

: "${KUBECTL:?KUBECTL must be set to kubectl binary path}"
: "${YQ:?YQ must be set to yq binary path}"
: "${NAMESPACE:?NAMESPACE must be set}"
: "${KCM_REPO_NAME:?KCM_REPO_NAME must be set}"
: "${KCM_REPO_URL:?KCM_REPO_URL must be set}"
: "${KCM_STABLE_VERSION:?KCM_STABLE_VERSION must be set}"

cat <<EOF | "${KUBECTL}" -n "${NAMESPACE}" create -f -
apiVersion: source.toolkit.fluxcd.io/v1
kind: HelmRepository
metadata:
  name: ${KCM_REPO_NAME}
  labels:
    k0rdent.mirantis.com/managed: "true"
spec:
  type: oci
  url: ${KCM_REPO_URL}
EOF

curl -sS --fail "https://api.github.com/repos/k0rdent/kcm/contents/templates/provider/kcm-templates/files/templates?ref=${KCM_STABLE_VERSION}" |
  jq -r '.[].download_url' |
  while read -r url; do
    curl -sS --fail "${url}" |
      "${YQ}" '.spec.helm.chartSpec.sourceRef.name = "'"${KCM_REPO_NAME}"'"' |
      "${KUBECTL}" -n "${NAMESPACE}" create -f - || true
  done
