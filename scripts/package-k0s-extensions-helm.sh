#!/bin/bash
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
# This script packages Helm charts affiliated with k0s extensions for airgap
# installations.
# This script should not be run directly.  Use 'make airgap-package' instead.
for template in $(find ${TEMPLATES_DIR} -name 'k0s*.yaml'); do
    if [[ $template == *"k0smotron"* ]]; then
        extensions_path=".spec.k0sConfig.spec.extensions.helm"
    else
        extensions_path=".spec.k0sConfigSpec.k0s.spec.extensions.helm"
    fi

    repos=$(grep -vw "{{" ${template} | ${YQ} e "${extensions_path}.repositories[] | [.url, .name] | join(\";\")")
    for repo in $repos; do
            url=${repo%;*}
            chartname=${repo#*;}
            version=$(grep -vw "{{" ${template} | $YQ e "${extensions_path}.charts[] | select(.chartname == \"*${chartname}*\") | .version")
            name=$(grep -vw "{{" ${template} | $YQ e "${extensions_path}.charts[] | select(.chartname == \"*${chartname}*\") | .name")
            if [[ $url == "" ]] || [[ $name == "" ]] || [[ $version == "" ]]; then
                echo "Error: Cannot construct Helm pull command from url: $url, name: $name, version: $version: one or more vars is not populated"
                exit 1
            fi
            if [[ ! $(find ${EXTENSION_CHARTS_PACKAGE_DIR} -name ${name}-${version}*.tgz) ]]; then
                echo "Pulling Helm chart ${name} from ${url} with version ${version}"
                ${HELM} pull --repo ${url} --version ${version} ${name} -d ${EXTENSION_CHARTS_PACKAGE_DIR}
            fi
    done
done
