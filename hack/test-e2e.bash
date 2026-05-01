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

: "${KIND_VERSION:?KIND_VERSION must be set}"

ginkgo_label_flag=""
if [[ -n "${GINKGO_LABEL_FILTER:-}" ]]; then
  ginkgo_label_flag="-ginkgo.label-filter=${GINKGO_LABEL_FILTER}"
fi

ginkgo_focus_flag=""
if [[ -n "${GINKGO_FOCUS:-}" ]]; then
  ginkgo_focus_flag="-ginkgo.focus=${GINKGO_FOCUS}"
fi

go_test_args=(./test/e2e/ -v -ginkgo.v -ginkgo.timeout=6h -timeout=6h)
if [[ -n "${ginkgo_label_flag}" ]]; then
  go_test_args+=("${ginkgo_label_flag}")
fi
if [[ -n "${ginkgo_focus_flag}" ]]; then
  go_test_args+=("${ginkgo_focus_flag}")
fi

if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
  echo "Cleaning preinstalled toolchains on GitHub runner to reclaim disk space"
  sudo rm -rf /usr/lib/jvm /usr/share/dotnet /usr/local/.ghcup /home/runner/.rustup /usr/local/julia /usr/local/lib/android/sdk /opt/hostedtoolcache/CodeQL
fi

KIND_CLUSTER_NAME="kcm-test" KIND_VERSION="${KIND_VERSION}" VALIDATE_CLUSTER_UPGRADE_PATH=false \
  go test "${go_test_args[@]}"
