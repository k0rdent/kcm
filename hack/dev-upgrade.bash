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
: "${KUBECTL:?KUBECTL must be set to kubectl binary path}"
: "${VERSION:?VERSION must be set}"
: "${FQDN_VERSION:?FQDN_VERSION must be set}"
: "${PROVIDER_TEMPLATES_DIR:?PROVIDER_TEMPLATES_DIR must be set}"
: "${VALUES_FILE:?VALUES_FILE must be set}"
: "${NAMESPACE:?NAMESPACE must be set}"
: "${READINESS_TIMEOUT:?READINESS_TIMEOUT must be set}"

echo "Applying new Release object: kcm-${FQDN_VERSION}"
"${YQ}" e ".spec.version = \"${VERSION}\" | .metadata.name = \"kcm-${FQDN_VERSION}\"" "${PROVIDER_TEMPLATES_DIR}/kcm-templates/files/release.yaml" | "${KUBECTL}" apply -f -

echo "Waiting for Release kcm-${FQDN_VERSION} to become Ready..."
"${KUBECTL}" wait release "kcm-${FQDN_VERSION}" --for='jsonpath={.status.ready}=true' --timeout 5m

echo "Patching .spec.core.kcm.config from ${VALUES_FILE}"
tmp="$(mktemp)"
trap 'rm -f "${tmp}"' EXIT
"${YQ}" -o=json -I=0 '{"spec":{"core":{"kcm":{"config": .}}}}' "${VALUES_FILE}" >"${tmp}"
"${KUBECTL}" patch management kcm --type=merge --patch-file "${tmp}"

echo "Patching Management object to use Release: kcm-${FQDN_VERSION}"
"${KUBECTL}" patch management kcm --type=merge -p '{"spec":{"release":"kcm-'"${FQDN_VERSION}"'"}}'

"${KUBECTL}" rollout restart -n "${NAMESPACE}" deployment/kcm-controller-manager

echo "Waiting for Management object status.release to match kcm-${FQDN_VERSION}..."
"${KUBECTL}" wait management kcm --for="jsonpath={.status.release}=kcm-${FQDN_VERSION}" --timeout="${READINESS_TIMEOUT}"

echo "Waiting for Management object to become Ready..."
"${KUBECTL}" wait management kcm --for=condition=Ready=True --timeout="${READINESS_TIMEOUT}"
