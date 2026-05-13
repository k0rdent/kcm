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

: "${YQ:?YQ must be set to the yq binary path}"

if ! command -v grep >/dev/null 2>&1; then
  echo "grep is required but was not found in PATH" >&2
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

if [[ ! -d templates ]]; then
  echo "templates directory not found."
  exit 0
fi

declare -a violations=()

while IFS= read -r -d '' file; do
  while IFS=: read -r line text; do
    expr="${text#*:}"
    key_prefix="${text%%:*}"

    # Skip non-YAML-key template lines like '{{- end }}:{{ ... }}'.
    if [[ "$key_prefix" == *"{{"* ]] || [[ "$key_prefix" == *"}}"* ]]; then
      continue
    fi

    # Already-quoted values are safe in YAML scalar positions.
    if [[ "$expr" == *"| quote"* ]] || [[ "$expr" == *"| squote"* ]] || [[ "$expr" == *" quote .Values"* ]]; then
      continue
    fi

    # Skip template control lines and structured YAML render helpers.
    if [[ "$expr" =~ \{\{[[:space:]]*[-]?[[:space:]]*(if|else|end|range|with)[[:space:]] ]]; then
      continue
    fi
    if [[ "$expr" == *"toYaml"* ]] || [[ "$expr" == *"fromYaml"* ]] || [[ "$expr" == *"nindent"* ]] || [[ "$expr" == *"indent"* ]]; then
      continue
    fi

    raw_path="$(printf '%s' "$expr" | grep -oE '\$?\.Values(\.[A-Za-z0-9_]+)+' | head -n1 | sed -E 's/^\$?\.Values\.//')"
    if [[ -z "$raw_path" ]]; then
      continue
    fi

    chart_dir="${file%%/templates/*}"
    values_file="${chart_dir}/values.yaml"
    if [[ ! -f "$values_file" ]]; then
      continue
    fi

    is_string="$($YQ eval ".${raw_path} | (type == \"!!str\")" "$values_file" 2>/dev/null || true)"
    has_string_default="false"
    if [[ "$expr" =~ \|[[:space:]]*default[[:space:]]*\" ]]; then
      has_string_default="true"
    fi

    if [[ "$is_string" == "true" || "$has_string_default" == "true" ]]; then
      violations+=("${file}:${line}:${text}")
    fi
  done < <(grep -nE ':[[:space:]]*\{\{[^}]*\.Values\.[^}]*\}\}[[:space:]]*(#.*)?$' "$file" || true)
done < <(find templates -type f -path '*/templates/*.yaml' -print0)

if ((${#violations[@]} > 0)); then
  echo "Found unquoted string .Values interpolations in YAML value positions:"
  printf '%s\n' "${violations[@]}"
  exit 1
fi

echo "Quoted .Values check passed."
