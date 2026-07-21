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

: "${HELM:?HELM must be set to helm binary path}"
: "${CHARTS_PACKAGE_DIR:?CHARTS_PACKAGE_DIR must be set}"
: "${REGISTRY_REPO:?REGISTRY_REPO must be set}"
: "${REGISTRY_IS_OCI:?REGISTRY_IS_OCI must be set}"
: "${REGISTRY_PLAIN_HTTP:=false}"

if [[ "${REGISTRY_PLAIN_HTTP}" != "true" && "${REGISTRY_PLAIN_HTTP}" != "false" ]]; then
  echo "REGISTRY_PLAIN_HTTP must be true or false" >&2
  exit 1
fi

oci_flags=()
if [[ "${REGISTRY_PLAIN_HTTP}" == "true" ]]; then
  oci_flags+=(--plain-http)
fi

pull_dir="$(mktemp -d)"
trap 'rm -rf "${pull_dir}"' EXIT

repo_flag=""
if [[ "${REGISTRY_IS_OCI}" != "true" ]]; then
  repo_flag="--repo"
fi

shopt -s nullglob
charts=("${CHARTS_PACKAGE_DIR}"/*.tgz)
shopt -u nullglob
if [[ ${#charts[@]} -eq 0 ]]; then
  echo "No chart packages found in ${CHARTS_PACKAGE_DIR}"
  exit 0
fi

for chart in "${charts[@]}"; do
  base="$(basename "${chart}" .tgz)"
  chart_version="$(echo "${base}" | grep -o "v\{0,1\}[0-9]\+\.[0-9]\+\.[0-9].*" || true)"
  if [[ -z "${chart_version}" ]]; then
    echo "Skipping chart with unexpected package name: ${base}" >&2
    continue
  fi
  chart_name="${base%-"${chart_version}"}"

  echo "Verifying chart ${chart_name} version ${chart_version} in ${REGISTRY_REPO}"
  chart_exists=false
  if [[ "${REGISTRY_IS_OCI}" == "true" ]]; then
    if "${HELM}" pull "${REGISTRY_REPO}/${chart_name}" --version "${chart_version}" --destination "${pull_dir}" "${oci_flags[@]}" >/dev/null 2>&1; then
      chart_exists=true
    fi
  else
    if [[ -n "${repo_flag}" ]]; then
      if "${HELM}" pull "${repo_flag}" "${REGISTRY_REPO}" "${chart_name}" --version "${chart_version}" --destination "${pull_dir}" >/dev/null 2>&1; then
        chart_exists=true
      fi
    else
      if "${HELM}" pull "${REGISTRY_REPO}" "${chart_name}" --version "${chart_version}" --destination "${pull_dir}" >/dev/null 2>&1; then
        chart_exists=true
      fi
    fi
  fi

  if [[ "${chart_exists}" == "true" ]]; then
    echo "Chart ${chart_name} version ${chart_version} already exists."
    continue
  fi

  if [[ "${REGISTRY_IS_OCI}" == "true" ]]; then
    echo "Pushing ${chart} to ${REGISTRY_REPO}"
    "${HELM}" push "${chart}" "${REGISTRY_REPO}" "${oci_flags[@]}"
    continue
  fi

  if [[ -z "${REGISTRY_USERNAME:-}" || -z "${REGISTRY_PASSWORD:-}" ]]; then
    echo "REGISTRY_USERNAME and REGISTRY_PASSWORD must be set to push to HTTPS Helm repo"
    exit 1
  fi

  "${HELM}" repo add kcm "${REGISTRY_REPO}"
  echo "Pushing ${chart} to ${REGISTRY_REPO}"
  "${HELM}" cm-push "${chart}" "${REGISTRY_REPO}" --username "${REGISTRY_USERNAME}" --password "${REGISTRY_PASSWORD}"
done
