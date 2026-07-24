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

# Remove classic ELBs whose dynamic CCM or CAPA ownership tags cannot be
# filtered by cloud-nuke
if [ -z "$CLUSTER_NAME_PREFIX" ]; then
  echo "CLUSTER_NAME_PREFIX must be set"
  exit 1
fi

if [ -z "$YQ" ]; then
  echo "YQ must be set to the path of the yq binary"
  echo "Use 'make dev-aws-nuke' instead of running this script directly"
  exit 1
fi

if [ -z "$AWSCLI" ]; then
  echo "AWSCLI must be set to the path of the AWS CLI"
  echo "Use 'make dev-aws-nuke' instead of running this script directly"
  exit 1
fi

CCM_CLUSTER_TAG="kubernetes.io/cluster/$CLUSTER_NAME_PREFIX"
CCM_CLUSTER_TAG_PREFIX_REGEX=$(printf '%s-' "$CCM_CLUSTER_TAG" | sed 's/[][\.^$*+?(){}|]/\\&/g')
CAPA_CLUSTER_TAG="sigs.k8s.io/cluster-api-provider-aws/cluster/$CLUSTER_NAME_PREFIX"
CAPA_CLUSTER_TAG_PREFIX_REGEX=$(printf '%s-' "$CAPA_CLUSTER_TAG" | sed 's/[][\.^$*+?(){}|]/\\&/g')
echo "Checking for ELB ownership tags matching '$CLUSTER_NAME_PREFIX'"
for LOADBALANCER in $("$AWSCLI" elb describe-load-balancers --output yaml | "$YQ" '.LoadBalancerDescriptions[].LoadBalancerName'); do
  echo "Checking ELB: $LOADBALANCER for ownership tag"
  if "$AWSCLI" elb describe-tags --load-balancer-names "$LOADBALANCER" --output yaml |
    CCM_CLUSTER_TAG="$CCM_CLUSTER_TAG" CCM_CLUSTER_TAG_PREFIX_REGEX="$CCM_CLUSTER_TAG_PREFIX_REGEX" \
      CAPA_CLUSTER_TAG="$CAPA_CLUSTER_TAG" CAPA_CLUSTER_TAG_PREFIX_REGEX="$CAPA_CLUSTER_TAG_PREFIX_REGEX" \
      "$YQ" -e '.TagDescriptions[].Tags[] | select((
        .Key == strenv(CCM_CLUSTER_TAG) or
        (.Key | test("^" + strenv(CCM_CLUSTER_TAG_PREFIX_REGEX))) or
        .Key == strenv(CAPA_CLUSTER_TAG) or
        (.Key | test("^" + strenv(CAPA_CLUSTER_TAG_PREFIX_REGEX)))
      ) and .Value == "owned")' >/dev/null; then
    echo "Deleting ELB: $LOADBALANCER"
    "$AWSCLI" elb delete-load-balancer --load-balancer-name "$LOADBALANCER"
  fi
done
