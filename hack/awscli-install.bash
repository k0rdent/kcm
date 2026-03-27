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

: "${HOSTOS:?HOSTOS must be set}"
: "${LOCALBIN:?LOCALBIN must be set}"
: "${AWSCLI:?AWSCLI must be set}"
: "${AWSCLI_VERSION:?AWSCLI_VERSION must be set}"
: "${CURDIR:?CURDIR must be set}"
: "${ENVSUBST:?ENVSUBST must be set}"

if [[ "${HOSTOS}" == "linux" ]]; then
  for cmd in unzip curl; do
    command -v "${cmd}" >/dev/null 2>&1 || {
      echo "${cmd} is not installed. Please install ${cmd} manually."
      exit 1
    }
  done

  curl -sS --fail "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m)-${AWSCLI_VERSION}.zip" -o /tmp/awscliv2.zip
  unzip -oqq /tmp/awscliv2.zip -d /tmp
  /tmp/aws/install -i "${LOCALBIN}/aws-cli" -b "${LOCALBIN}" --update
  ln -sf "${LOCALBIN}/aws-cli/v2/current/bin/aws" "${AWSCLI}"
  exit 0
fi

if [[ "${HOSTOS}" == "darwin" ]]; then
  curl -sS --fail "https://awscli.amazonaws.com/AWSCLIV2.pkg" -o "${CURDIR}/AWSCLIV2.pkg"
  LOCALBIN="${LOCALBIN}" "${ENVSUBST}" -i "${CURDIR}/scripts/awscli-darwin-install.xml.tpl" >"${CURDIR}/choices.xml"
  installer -pkg "${CURDIR}/AWSCLIV2.pkg" -target CurrentUserHomeDirectory -applyChoiceChangesXML "${CURDIR}/choices.xml"
  ln -sf "${LOCALBIN}/aws-cli/aws" "${AWSCLI}"
  rm -f "${CURDIR}/AWSCLIV2.pkg" "${CURDIR}/choices.xml"
  exit 0
fi

if [[ "${HOSTOS}" == "windows" ]]; then
  echo "Installing to ${LOCALBIN} on Windows is not yet implemented"
  exit 1
fi

echo "Unsupported HOSTOS: ${HOSTOS}"
exit 1
