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

# Get tracked + untracked file changes
TRACKED_CHANGED_FILES=$(git diff --name-only HEAD)
UNTRACKED_FILES=$(git ls-files --others --exclude-standard)

ALL_CHANGED_FILES=$(echo -e "$TRACKED_CHANGED_FILES\n$UNTRACKED_FILES" | sort -u | grep -E '^templates/(provider|cluster)/' | grep -v '^templates/provider/kcm-templates/' || true)

declare -A UPDATED_CHARTS

for file in $ALL_CHANGED_FILES; do
  dir=$(dirname "$file")
  while [[ "$dir" != "." && "$dir" != "/" ]]; do
    chart_file="$dir/Chart.yaml"
    if [[ -f "$chart_file" ]]; then
      if [[ -n "${UPDATED_CHARTS[$chart_file]:-}" ]]; then
        break
      fi

      # Skip if top-level version already changed compared to HEAD
      version_current=$(yq e '.version' "$chart_file")
      version_committed=$(git show "HEAD:$chart_file" 2>/dev/null | yq e '.version' - || echo "")

      if [[ "$version_current" != "$version_committed" ]]; then
        echo "Skipping $chart_file: .version already modified ($version_committed → $version_current)"
      else
        if [[ "$version_current" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
          major="${BASH_REMATCH[1]}"
          minor="${BASH_REMATCH[2]}"
          patch="${BASH_REMATCH[3]}"
          new_version="${major}.${minor}.$((patch + 1))"
          yq e -i ".version = \"$new_version\"" "$chart_file"
          echo "Bumped $chart_file: $version_current → $new_version"
        else
          echo "Invalid semver in $chart_file: $version_current" >&2
          exit 1
        fi
      fi

      UPDATED_CHARTS["$chart_file"]=1
      break
    fi
    dir=$(dirname "$dir")
  done
done
