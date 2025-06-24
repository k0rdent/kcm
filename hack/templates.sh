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

mkdir -p $TEMPLATES_OUTPUT_DIR
rm -f $TEMPLATES_OUTPUT_DIR/*.yaml

for type in "$TEMPLATES_DIR"/*; do
    kind="$(echo "${type#*/}Template" | awk '{$1=toupper(substr($1,0,1))substr($1,2)}1')"
    for chart in "$type"/*; do
        if [ -d "$chart" ]; then
            name=$(grep '^name:' $chart/Chart.yaml | awk '{print $2}')
            if [ "$name" = "$KCM_TEMPLATES_CHART_NAME" ]; then continue; fi
            version=$(grep '^version:' $chart/Chart.yaml | awk '{print $2}')
            template_name=$name-$(echo "$version" | sed 's/^v//; s/\./-/g')
            if [ "$kind" = "ProviderTemplate" ]; then file_name=$name; else file_name=$template_name; fi

            cat <<EOF > $TEMPLATES_OUTPUT_DIR/$file_name.yaml
apiVersion: k0rdent.mirantis.com/v1beta1
kind: $kind
EOF
            cat <<EOF >> $TEMPLATES_OUTPUT_DIR/$file_name.yaml
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

            echo "Generated $TEMPLATES_OUTPUT_DIR/$name.yaml"
        fi
    done
done
