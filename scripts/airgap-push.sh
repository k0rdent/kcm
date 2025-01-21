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
#
# This script can be used to help users re-tag and push images and Helm charts
# into a private registry for use when deploying KCM ClusterDeployments into an
# air-gapped environment.  This script is packaged as part of the airgap bundle
# for convenience.

REPO=""
CHART_REPO=""
AIRGAP_BUNDLE=""
HELP=""
EXTENSION_TARBALL_PREFIX="kcm-extension-images"
WORK_DIR="$(pwd)/kcm-airgap"

# Print the help message
function print_help() {
    echo "Usage:"
    echo "  airgap-push.sh [OPTIONS]"
    echo "Ensure repositories are logged into via 'helm' and 'docker' before running this script."
    echo "Options:"
    echo "  -h, --help"
    echo "    Print this help message"
    echo "  -r, --image-repo (required)"
    echo "    The image repo to push the images to"
    echo "  -c, --chart-repo (required)"
    echo "    The repository to push the Helm charts to, for OCI prefix use oci://"
    echo "  -i, --insecure-registry"
    echo "    Use insecure registry for pushing Helm charts"
    echo "  -a, --airgap-bundle (required)"
    echo "    The path to the airgap bundle"
}

function ctrl_c() {
        echo "Caught CTRL-C, exiting..."
        exit 1
}

trap ctrl_c INT

# Parse the options
while [[ $# -gt 0 ]]; do
    key="$1"
    case $key in
        -h|--help)
            HELP="true"
            shift
            ;;
        -r|--image-repo)
            REPO="$2"
            shift
            shift
            ;;
        -c|--chart-repo)
            CHART_REPO="$2"
            shift
            shift
            ;;
        -i|--insecure-registry)
            INSECURE_REGISTRY="true"
            shift
            ;;
        -a|--airgap-bundle)
            AIRGAP_BUNDLE="$2"
            shift
            shift
            ;;
        *)
            echo "Unknown option: $1"
            print_help
            exit 1
            ;;
    esac
done


if [ ! -z "$HELP" ]; then
    print_help
    exit 0
fi

if [ -z "$REPO" ]; then
    echo "The repository must be set"
    print_help
    exit 1
fi

if [ -z "$CHART_REPO" ]; then
    echo "The chart repository must be set"
    print_help
    exit 1
fi

if [ -z "$AIRGAP_BUNDLE" ]; then
    echo "The airgap bundle must be set"
    exit 1
else
    # Validate the airgap bundle
    if [ ! -f "$AIRGAP_BUNDLE" ]; then
        echo "The provided airgap bundle: ${AIRGAP_BUNDLE} does not exist"
        exit 1
    fi
fi

if [ ! $(command -v jq) ]; then
    echo "'jq' could not be found, install 'jq' to continue"
    exit 1
fi

mkdir -p ${WORK_DIR}

# Extract extension images from the airgap bundle.
echo "Extracting extension images from airgap bundle: ${AIRGAP_BUNDLE}..."
extension_tarball_name=$(tar tf ${AIRGAP_BUNDLE} | grep "${EXTENSION_TARBALL_PREFIX}")
tar -C ${WORK_DIR} -xf ${AIRGAP_BUNDLE} ${extension_tarball_name}
if [ $? -ne 0 ]; then
    echo "Failed to extract extension images from the airgap bundle"
    exit 1
fi

# Load the extension images into the Docker daemon for re-tagging and pushing.
echo "Loading extension images into Docker..."
docker load -i ${WORK_DIR}/${extension_tarball_name}
if [ $? -ne 0 ]; then
    echo "Failed to load extension images into Docker"
    exit 1
fi


# Extract the repositories json file from the extensions bundle.
echo "Retagging and pushing extension images to ${REPO}..."
tar -C ${WORK_DIR} -xf ${WORK_DIR}/${extension_tarball_name} "repositories"
for image in $(cat ${WORK_DIR}/repositories | jq -r 'to_entries[] | .key'); do
    image_name=$(echo ${image} | grep -o '[^/]*$')

    # docker images -a may return multiple images with the same name but
    # different tags.  We need to retag and push all of them.
    for old_image in $(docker images -a | grep ${image} | awk '{print $1":"$2}'); do
        tag=${old_image#*:}
        new_image="${REPO}/${image_name}:${tag}"

        echo "Retagging image: ${old_image} with ${new_image}..."

        docker tag ${old_image} ${new_image}
        if [ $? -ne 0 ]; then
            echo "Failed to retag image: ${old_image} with ${new_image}"
            exit 1
        fi

        echo "Pushing image: ${new_image}..."

        docker push ${new_image}
        if [ $? -ne 0 ]; then
            echo "Failed to push image: ${new_image}"
            exit 1
        fi
    done
done

# Extract all of the Helm charts from the airgap bundle.
echo "Extracting Helm charts from airgap bundle: ${AIRGAP_BUNDLE}..."
tar -C ${WORK_DIR} -xf ${AIRGAP_BUNDLE} "charts"
if [ $? -ne 0 ]; then
    echo "Failed to extract Helm charts from the airgap bundle"
    exit 1
fi

# Next, use Helm to push the charts to the given chart repository.
echo "Pushing Helm charts to ${CHART_REPO}..."
if [ ! -z "$INSECURE_REGISTRY" ]; then
    insecure_registry_flag="--insecure-skip-tls-verify"
fi

for chart in $(find ${WORK_DIR}/charts -name "*.tgz"); do
    helm push ${insecure_registry_flag} ${chart} ${CHART_REPO}
    if [ $? -ne 0 ]; then
        echo "Failed to push Helm chart: ${chart}"
        exit 1
    fi
done

# Clean up any extracted files.
echo "Cleaning up..."
rm -rf ${WORK_DIR}
