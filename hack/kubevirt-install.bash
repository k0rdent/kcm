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

KUBECTL_BIN="${KUBECTL:-kubectl}"
KUBEVIRT_VERSION="$(curl -sS --fail https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirt/stable.txt)"
CDI_VERSION="$(basename "$(curl -sS --fail -w '%{redirect_url}' -o /dev/null https://github.com/kubevirt/containerized-data-importer/releases/latest)")"

if [[ -z "${KUBEVIRT_VERSION}" || -z "${CDI_VERSION}" ]]; then
  echo "Failed to resolve KubeVirt or CDI versions" >&2
  exit 1
fi

echo "Installing KubeVirt ${KUBEVIRT_VERSION}"
echo "Installing Containerized Data Importer ${CDI_VERSION}"
"${KUBECTL_BIN}" apply -f "https://github.com/kubevirt/kubevirt/releases/download/${KUBEVIRT_VERSION}/kubevirt-operator.yaml"
"${KUBECTL_BIN}" apply -f "https://github.com/kubevirt/kubevirt/releases/download/${KUBEVIRT_VERSION}/kubevirt-cr.yaml"
"${KUBECTL_BIN}" apply -f "https://github.com/kubevirt/containerized-data-importer/releases/download/${CDI_VERSION}/cdi-operator.yaml"
"${KUBECTL_BIN}" apply -f "https://github.com/kubevirt/containerized-data-importer/releases/download/${CDI_VERSION}/cdi-cr.yaml"

echo "Waiting for CRD kubevirts.kubevirt.io to be created..."
until "${KUBECTL_BIN}" get crd kubevirts.kubevirt.io >/dev/null 2>&1; do sleep 2; done
echo "Waiting for CRD kubevirts.kubevirt.io to be Established..."
"${KUBECTL_BIN}" wait --for=condition=Established --timeout=180s crd/kubevirts.kubevirt.io

echo "Waiting for KubeVirt CR object to exist..."
until "${KUBECTL_BIN}" -n kubevirt get kubevirt kubevirt >/dev/null 2>&1; do sleep 2; done

"${KUBECTL_BIN}" -n kubevirt patch kubevirt kubevirt --type=merge --patch '{"spec":{"configuration":{"developerConfiguration":{"useEmulation":true}}}}'

echo "Waiting for KubeVirt to be deployed..."
timeout=900
interval=10
while [[ ${timeout} -gt 0 ]]; do
  status="$(${KUBECTL_BIN} get kubevirt.kubevirt.io/kubevirt -n kubevirt -o=jsonpath="{.status.phase}" 2>/dev/null || echo "")"
  if [[ "${status}" == "Deployed" ]]; then
    echo "KubeVirt is deployed"
    exit 0
  fi
  echo "KubeVirt is deploying..."
  sleep "${interval}"
  timeout=$((timeout - interval))
done

echo "Timeout reached. KubeVirt is not deployed."
exit 1
