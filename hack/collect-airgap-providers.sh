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
set -e

#### VARS
# LOCALBIN - from Makefile
# PROVIDER_TEMPLATES_DIR - from Makefile
# HELM - from Makefile
# YQ - from Makefile
# CLUSTERCTL - from Makefile
PROVIDER_LIST_FILE="${LOCALBIN}/providers.yaml"
REPOSITORIES_FILE="${LOCALBIN}/capi-repositories.yaml"
DOWNLOAD_LIST_FILE="${LOCALBIN}/download-list"
EXCLUDED_PROVIDERS='kcm*\|projectsveltos\|cluster-api-provider-aws\|cluster-api-provider-azure'

$CLUSTERCTL config repositories -o yaml > $REPOSITORIES_FILE

for tmpl in $(ls --color=never -1 $PROVIDER_TEMPLATES_DIR | grep -v $EXCLUDED_PROVIDERS); do
    $HELM template ${PROVIDER_TEMPLATES_DIR}/${tmpl} |
	path="${PROVIDER_TEMPLATES_DIR}/${tmpl}" $YQ 'select(.apiVersion | test("operator.cluster.x-k8s.io.*")) | [{"name": .metadata.name, "version": .spec.version, "kind": .kind, "path": strenv(path)}]';
done | grep -v '\[\]' > $PROVIDER_LIST_FILE

for pdr in $($YQ '.[] | "\(.name):\(.kind)"' $PROVIDER_LIST_FILE); do
    # exports are needed for yq
    export name=${pdr%:*}
    export kind=${pdr#*:}
    export version=$($YQ '[.[] | select(.name == strenv(name))] | .[0] | .version' $PROVIDER_LIST_FILE)
    export path=$($YQ '[.[] | select(.name == strenv(name))] | .[0] | .path' $PROVIDER_LIST_FILE)
    components_filename="${path}/files/${name}_${kind,,}_components_${version}.yaml"
    metadata_filename="${path}/files/${name}_metadata_${version}.yaml"
    components_file=$($YQ '.[] | select(.Name == strenv(name) and .ProviderType == strenv(kind)) | "\(.File)"' $REPOSITORIES_FILE)
    metadata_file="metadata.yaml"
    url=$($YQ '.[] | select(.Name == strenv(name) and .ProviderType == strenv(kind)) | "\(.URL)"' $REPOSITORIES_FILE | sed "s~latest~download/$version~")
    echo "${components_filename},${url}${components_file}"
    echo "${metadata_filename},${url}${metadata_file}"
done | sort -u > $DOWNLOAD_LIST_FILE

for fl in $(cat $DOWNLOAD_LIST_FILE); do
    path=${fl%,*}
    url=${fl#*,}
    dir=${path%/*.yaml}
    if [ -f $path ]; then
	echo "File $path exists, skipping download"
	continue
    fi
    mkdir -p $dir
    curl -fsL $url -o $path
done
