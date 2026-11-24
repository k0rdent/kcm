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

: "${YQ:?YQ must be set to yq binary path}"
: "${KCM_VERSION_PLACEHOLDER:?KCM_VERSION_PLACEHOLDER must be set}"

PROVIDER_TEMPLATES_DIR=${PROVIDER_TEMPLATES_DIR:-templates/provider}

version="${KCM_VERSION_PLACEHOLDER}"
fqdn="${version//./-}"
failed=0

check() {
  local file="${PROVIDER_TEMPLATES_DIR}/$1" expr="$2" want="$3" got
  got="$("${YQ}" eval "${expr}" "${file}")"
  if [[ "${got}" != "${want}" ]]; then
    echo "${file}: ${expr} is \"${got}\", want \"${want}\"" >&2
    failed=1
  fi
}

regional='.dependencies[] | select(.name == "kcm-regional") | .version'

check kcm/Chart.yaml '.version' "${version}"
check kcm/Chart.yaml "${regional}" "${version}"
check kcm/Chart.lock "${regional}" "${version}"
check kcm-regional/Chart.yaml '.version' "${version}"
check kcm-templates/Chart.yaml '.version' "${version}"
check kcm-templates/files/release.yaml '.metadata.name' "kcm-${fqdn}"
check kcm-templates/files/release.yaml '.spec.version' "${version}"
check kcm-templates/files/release.yaml '.spec.kcm.template' "kcm-${fqdn}"
check kcm-templates/files/release.yaml '.spec.regional.template' "kcm-regional-${fqdn}"
check kcm-templates/files/templates/kcm.yaml '.metadata.name' "kcm-${fqdn}"
check kcm-templates/files/templates/kcm.yaml '.spec.helm.chartSpec.version' "${version}"
check kcm-templates/files/templates/kcm-regional.yaml '.metadata.name' "kcm-regional-${fqdn}"
check kcm-templates/files/templates/kcm-regional.yaml '.spec.helm.chartSpec.version' "${version}"

if ((failed)); then
  cat >&2 <<EOF

main carries the placeholder version ${version}: real versions are committed on
release branches only and stamped from the tag at build time. Do not forward-port
release version bumps or commit files rewritten by 'make set-kcm-version' or
'make dev-upgrade'.
EOF
  exit 1
fi

echo "KCM charts and Release carry the placeholder version ${version}"
