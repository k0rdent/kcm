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
# bundle-images.sh bundles all of the images used across HMC into a single
# tarball.  This is useful for deploying HMC into air-gapped environments.
# It is recommended to use this script in conjunction with the Makefile target
# `make bundle-images` which will perform some additional steps outside of
# this scripts functionality.
# Usage: make bundle-images
# This script should not be run directly.  Use 'make bundle-images' instead.
LABEL_KEY="cluster.x-k8s.io/provider"
IMAGES_BUNDLED="$IMG $K0S_AG_IMAGE"
EXTENSION_IMAGES_BUNDLED=""

echo -e "Bundling images for HMC, this may take awhile...\n"

trap ctrl_c INT

function wait_for_deploy_exist() {
    local deployment_label=$1
    local max_wait_secs=300
    local interval_secs=5
    local start_time

    start_time=$(date +%s)

    echo "Waiting up to ${max_wait_secs}s for provider Deployment with label: \"${deployment_label}\" to exist in namespace: \"${NAMESPACE}\"..."

    while true; do
        current_time=$(date +%s)
            if (( (current_time - start_time) > max_wait_secs )); then
                echo "Error: Waited for Deployment with label: \"${deployment_label}\" in namespace: \"${NAMESPACE}\" to exist for ${max_wait_secs} seconds and it still does not exist."
                return 1
            fi

        output=$(${KUBECTL} -n "${NAMESPACE}" get deploy -l ${deployment_label})

        if [[ $output != "" ]]; then
            echo "Deployment in namespace: \"${NAMESPACE}\" with label: \"${deployment_label}\" exists."
            break
        else
            echo "Deployment with label: \"${deployment_label}\" in namespace: \"${NAMESPACE}\" does not exist yet.  Waiting ${interval_secs} seconds..."
            sleep $interval_secs
        fi
    done
}

function bundle_images() {
    local images=$1
    local tarball=$2

    echo "Bundling images into ${tarball}..."
    docker save -o ${tarball} ${images}
    if [[ $? -ne 0 ]]; then
        echo "Error: Failed to bundle images into ${tarball}"
        exit 1
    fi
}


function ctrl_c() {
        echo "Caught CTRL-C, exiting..."
        exit 1
}

echo -e "\nVerifying provider Deployments are ready...\n"

# Verify each provider we support has deployed so we can get the images used
# across the deployments.
for template in $(find ${TEMPLATES_DIR} -name 'provider.yaml');
do
    result=$(grep 'kind: .*Provider' ${template})
    provider_yaml=$(grep "${result}" -A2 ${template})
    provider_kind=$(echo -e "${provider_yaml}" | $YQ e '.kind' -)
    provider_name=$(echo -e "${provider_yaml}" | $YQ e '.metadata.name' -)
    provider_kind_tolower=$(echo ${provider_kind} | tr '[:upper:]' '[:lower:]')

    if [[ $provider_name == "" ]]; then
        echo "Error: Cannot determine provider Name from ${template}"
        exit 1
    fi

    if [[ $provider_kind_tolower == "" ]]; then
        echo "Error: Cannot determine provider Kind from ${template}"
        exit 1
    fi

    # controlplane is a special case which needs a hyphen.
    if [[ $provider_kind_tolower == "controlplane" ]]; then
        provider_kind_tolower="control-plane"
    fi

    # coreprovider does not have a provider prefix.
    if [[ $provider_kind_tolower == "coreprovider" ]]; then
        label_value=$(echo ${provider_name})
    else
        label_value=$(echo $(echo ${provider_kind_tolower} | sed -e 's/provider//g')-${provider_name})
    fi

    wait_for_deploy_exist "$LABEL_KEY=$label_value"

    echo "Verifying Deployment(s) with ${LABEL_KEY}=${label_value} label is condition=available..."

    ${KUBECTL} wait --for condition=available --timeout=10m deploy -l ${LABEL_KEY}=${label_value} -n ${NAMESPACE}
    if [[ $? -ne 0 ]]; then
        echo "Error: Timed out waiting for available Deployment with ${LABEL_KEY}=${label_value} label"
        exit 1
    fi
done


# Now that we know everything is deployed and ready, we can get all of images by
# execing into the KIND cluster.
control_plane=$(${KUBECTL} get nodes --no-headers -o custom-columns=":metadata.name")
if [[ $? -ne 0 ]] || [[ $control_plane == "" ]]; then
    echo "Error: Cannot get control plane node"
    exit 1
fi

echo -e "\nPulling images for HMC components...\n"

for image in $(docker exec ${control_plane} crictl images | sed 1,1d | awk '{print $1":"$2}' | grep -v 'kindest');
do
    if [[ $image == "" ]]; then
        echo "Error: Failed to get image from KIND cluster, image string should not be empty"
        exit 1
    fi

    if [[ $image == *"hmc"* ]]; then
        # Don't try to pull the controller image.
        continue
    fi

    tag=${image#*:}
    if [[ $tag == "<none>" ]]; then
        echo "Will not pull image: ${image} with tag <none>, continuing..."
        continue
    fi

    docker pull $image
    if [[ $? -ne 0 ]]; then
        echo "Error: Failed to pull ${image}"
        exit 1
    fi

    IMAGES_BUNDLED="$IMAGES_BUNDLED $image"
done

echo -e "\nPulling images for HMC extensions...\n"

# Next, we need to build a list of images used by k0s extensions.  Walk the
# templates directory and extract the images used by the extensions.
for template in $(find ${TEMPLATES_DIR} -mindepth 2 -name Chart.yaml -type f -exec dirname {} \;);
do
    for k0sconfig_path in $(find "${template}" -name 'k0s*.yaml');
    do
        if [[ k0sconfig_path == *"k0smotron"* ]]; then
            extensions_path=".spec.k0sConfig.spec.extensions.helm"
        else
            extensions_path=".spec.k0sConfigSpec.k0s.spec.extensions.helm"
        fi

      repos=$(grep -vw "{{" ${k0sconfig_path} | $YQ e "${extensions_path}.repositories[] | [.url, .name] | join(\";\")")
      for repo in $repos
      do
          url=${repo%;*}
          chartname=${repo#*;}
          version=$(grep -vw "{{" ${k0sconfig_path} |
              $YQ e "${extensions_path}.charts[] | select(.chartname == \"*${chartname}*\") | .version")
          name=$(grep -vw "{{" ${k0sconfig_path} |
              $YQ e "${extensions_path}.charts[] | select(.chartname == \"*${chartname}*\") | .name")

          if [[ $name != "kube-vip" ]]; then
              k0sconfig="${k0sconfig_path#$template/}"
              echo -e "Processing k0sconfig file: ${k0sconfig}\n"
              ${HELM} template "${template}" --show-only "${k0sconfig}" |
                  $YQ e "${extensions_path}.charts[] | select(.chartname == \"*$chartname*\") | .values" > ${name}-values.yaml
          fi

          if [[ $url == "" ]] || [[ $name == "" ]] || [[ $version == "" ]]; then
              echo "Error: Failed to get URL, name, or version from ${k0sconfig_path}"
              exit 1
          fi

          # Use 'helm template' to get the images used by the extension.
          if [[ $name == "kube-vip" ]]; then
              # FIXME: This is a temporary workaround for kube-vip, if we use
              # a custom image tag in the future we'll need to update this.
              # kube-vip is a special case where our yaml values result in invalid
              # Helm template output, for now render the YAML without values.
              template_output=$(${HELM} template --repo ${url} --version ${version} ${name})
          else
              template_output=$(${HELM} template --repo ${url} --version ${version} ${name} --values ${name}-values.yaml)
              if [[ $? -ne 0 ]]; then
                  echo "Error: Failed to get images from Helm template for ${name}, trying to output values with debug..."

                  template_output=$(${HELM} template --repo ${url} --version ${version} ${name} --values ${name}-values.yaml --debug)
                  if [[ $? -ne 0 ]]; then
                      echo "Error: Failed to get images from Helm template for ${name} with debug output"
                      exit 1
                  fi
              fi
          fi

          for image in $(echo "${template_output}" | $YQ -N e .spec.template.spec.containers[].image);
          do
              docker pull ${image}
              if [[ $? -ne 0 ]]; then
                  echo "Error: Failed to pull ${image}"
                  exit 1
              fi

              EXTENSION_IMAGES_BUNDLED="$EXTENSION_IMAGES_BUNDLED $image"
          done

          rm -f $name-values.yaml
      done
  done
done

echo -e "\nSaving images...\n"
images_bundled_uniq=$(echo "${IMAGES_BUNDLED}" | tr ' ' '\n' | sort -u)
bundle_images "$images_bundled_uniq" $BUNDLE_TARBALL

if [[ $EXTENSION_IMAGES_BUNDLED != "" ]]; then
    extension_images_bundled_uniq=$(echo "${EXTENSION_IMAGES_BUNDLED}" | tr ' ' '\n' | sort -u)
    bundle_images "$extension_images_bundled_uniq" $EXTENSIONS_BUNDLE_TARBALL
fi

echo -e "\nCleaning up all pulled images...\n"
# Cleanup the images bundled by removing them from the local image cache.
all_images="$images_bundled_uniq $extension_images_bundled_uniq"
for image in $all_images;
do
    echo "Removing ${image} from local image cache..."
    docker rmi ${image}
    if [ $? -ne 0 ]; then
        # Note that we failed here but continue trying to remove the other
        # images.
        echo "Error: Failed to remove ${image} from local image cache"
    fi
done

echo "Done! Images bundled into ${BUNDLE_TARBALL} and ${EXTENSIONS_BUNDLE_TARBALL}"
