#!/bin/sh
# Copyright 2024
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

# Directory containing KCM templates
TEMPLATES_DIR=${TEMPLATES_DIR:-templates}
# Output directory for the generated Template manifests
TEMPLATES_OUTPUT_DIR=${TEMPLATES_OUTPUT_DIR:-templates/provider/kcm-templates/files/templates}
# The name of the KCM templates helm chart
KCM_TEMPLATES_CHART_NAME='kcm-templates'

mkdir -p "$TEMPLATES_OUTPUT_DIR"
rm -f "$TEMPLATES_OUTPUT_DIR"/*.yaml

for type in "$TEMPLATES_DIR"/*; do
  [ -d "$type" ] || continue
  type_name=$(basename "$type")
  kind="$(printf '%sTemplate\n' "$type_name" | awk '{$1=toupper(substr($1,1,1))substr($1,2)}1')"
  for chart in "$type"/*; do
    if [ -d "$chart" ]; then
      chart_file="$chart/Chart.yaml"
      [ -f "$chart_file" ] || continue

      name=$(grep '^name:' "$chart_file" | awk '{print $2}')
      if [ -z "$name" ]; then
        echo "Missing .name in $chart_file" >&2
        exit 1
      fi

      if [ "$name" = "$KCM_TEMPLATES_CHART_NAME" ]; then continue; fi

      version=$(grep '^version:' "$chart_file" | awk '{print $2}')
      if [ -z "$version" ]; then
        echo "Missing .version in $chart_file" >&2
        exit 1
      fi
      template_version=$version

      if [ "$kind" = "ProviderTemplate" ]; then
        raw_app_version=$(grep '^appVersion:' "$chart_file" | awk '{print $2}' | tr -d '"')
        # Preserve prerelease/build qualifiers to avoid collisions between versions like
        # v1.1.0-rc.1 and v1.1.0 while still producing DNS-safe dashed names.
        app_version=$(echo "$raw_app_version" |
          sed -E 's/^[vV]//; s/[^0-9A-Za-z.+-]/-/g; s/[.+]/-/g; s/[^0-9A-Za-z-]/-/g; s/^-+//; s/-+$//; s/-+/-/g' |
          tr '[:upper:]' '[:lower:]')
        if [ -z "$app_version" ] || ! echo "$app_version" | grep -Eq '[0-9]'; then
          echo "Invalid appVersion in $chart/Chart.yaml: $raw_app_version" >&2
          exit 1
        fi
        template_version=$app_version
      fi

      template_name=$name-$(echo "$template_version" | sed 's/\./-/g')
      if [ "$kind" = "ProviderTemplate" ]; then file_name=$name; else file_name=$template_name; fi

      cat <<EOF >"$TEMPLATES_OUTPUT_DIR"/"$file_name".yaml
apiVersion: k0rdent.mirantis.com/v1beta1
kind: $kind
metadata:
  name: $template_name
  annotations:
    helm.sh/resource-policy: keep
spec:
  helm:
    chartSpec:
      chart: $name
      version: $version
      interval: 10m0s
      sourceRef:
        kind: HelmRepository
        name: kcm-templates
EOF

      echo "Generated $TEMPLATES_OUTPUT_DIR/$file_name.yaml"
    fi
  done
done
