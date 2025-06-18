#!/usr/bin/env bash

# Copyright 2025
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

set -eu

TEMPLATE_DIR=${TEMPLATE_DIR:-templates/provider/kcm-templates/files/templates}
CONFIG_DEV_DIR=${CONFIG_DEV_DIR:-config/dev}

# Get tracked + untracked changed files
TRACKED_CHANGED=$(git diff --name-only HEAD -- "$TEMPLATE_DIR")
UNTRACKED_CHANGED=$(git ls-files --others --exclude-standard "$TEMPLATE_DIR")
ALL_CHANGED=$(echo -e "$TRACKED_CHANGED\n$UNTRACKED_CHANGED" | sort -u | grep -E '\.ya?ml$' || true)

for file in $ALL_CHANGED; do
  # Skip deleted/missing files
  [[ -f "$file" ]] || continue

  # Only process ClusterTemplate kind
  kind=$(yq e '.kind' "$file")
  [[ "$kind" != "ClusterTemplate" ]] && continue

  template_name=$(yq e '.metadata.name' "$file")
  template_type=$(yq e '.spec.helm.chartSpec.chart' "$file")

  for deployment in "$CONFIG_DEV_DIR"/*-clusterdeployment.yaml; do
    [[ ! -f "$deployment" ]] && continue

    current_template=$(yq e '.spec.template' "$deployment")

    if [[ "$current_template" == *"$template_type"* && "$current_template" != "$template_name" ]]; then
      echo "Updating $deployment: $current_template → $template_name"
      yq e -i ".spec.template = \"$template_name\"" "$deployment"
    fi
  done
done
