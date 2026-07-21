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

# Removes vSphere VMs left behind by CAPV during E2E tests.
# Scoped to VMs under ${VSPHERE_FOLDER} in ${VSPHERE_DATACENTER} whose names
# start with ${CLUSTER_NAME_PREFIX}- (the CI run's cluster prefix). Non-VM inventory
# objects (folders, resource pools, networks, datastores) are never enumerated;
# templates share the VM object type but never match the ${CLUSTER_NAME_PREFIX}-*
# name filter, so they are safe as long as the folder/prefix conventions hold.
#
# Meant to be invoked via `make dev-vsphere-nuke`; standalone usage requires
# the VSPHERE_* env vars listed below plus a govc binary on PATH (or GOVC).
set -euo pipefail

: "${GOVC:=govc}"
: "${CLUSTER_NAME_PREFIX:?CLUSTER_NAME_PREFIX must be set}"
: "${VSPHERE_SERVER:?VSPHERE_SERVER must be set}"
: "${VSPHERE_USER:?VSPHERE_USER must be set}"
: "${VSPHERE_PASSWORD:?VSPHERE_PASSWORD must be set}"
: "${VSPHERE_DATACENTER:?VSPHERE_DATACENTER must be set}"
: "${VSPHERE_FOLDER:?VSPHERE_FOLDER must be set}"

# Map the CAPV-style variables onto the ones govc reads directly.
export GOVC_URL="${VSPHERE_SERVER}"
export GOVC_USERNAME="${VSPHERE_USER}"
export GOVC_PASSWORD="${VSPHERE_PASSWORD}"
export GOVC_DATACENTER="${VSPHERE_DATACENTER}"

# Prefer a pinned thumbprint over blanket TLS skip.
if [ -n "${VSPHERE_THUMBPRINT:-}" ]; then
  export GOVC_TLS_KNOWN_HOSTS
  GOVC_TLS_KNOWN_HOSTS=$(mktemp)
  trap 'rm -f "${GOVC_TLS_KNOWN_HOSTS}"' EXIT
  # Keep host[:port] intact -- govc keys known-hosts by URL host which includes
  # the port for non-default ports (and brackets for IPv6). Stripping the port
  # would prevent thumbprint matching for those cases
  host="${VSPHERE_SERVER#https://}"
  host="${host#http://}"
  host="${host%%/*}"
  printf '%s %s\n' "${host}" "${VSPHERE_THUMBPRINT}" >"${GOVC_TLS_KNOWN_HOSTS}"
elif [ "${VSPHERE_INSECURE:-}" = "true" ]; then
  # Explicit opt-out from TLS verification (dev/lab only). Since this script
  # destroys VMs, we refuse to silently trust an untrusted vCenter.
  export GOVC_INSECURE=true
else
  echo "vsphere-nuke: VSPHERE_THUMBPRINT is not set; refusing to connect without TLS verification." >&2
  echo "  Set VSPHERE_THUMBPRINT (preferred) or VSPHERE_INSECURE=true to override." >&2
  exit 1
fi

# Normalize the folder to an absolute inventory path.
folder="${VSPHERE_FOLDER}"
case "${folder}" in
  /*) : ;;
  *) folder="/${VSPHERE_DATACENTER}/vm/${folder}" ;;
esac

pattern="${CLUSTER_NAME_PREFIX}-*"

echo "Searching for VMs matching '${pattern}' under '${folder}'"

# Not using `mapfile < <(cmd)` because process substitution swallows the exit
# status; a broken vCenter lookup would silently report "no matches".
vms_output=$("${GOVC}" find -type m -name "${pattern}" "${folder}")
mapfile -t vms <<<"${vms_output}"
# `mapfile <<<""` yields one empty element; treat that as no matches.
if [ "${#vms[@]}" -eq 1 ] && [ -z "${vms[0]}" ]; then
  vms=()
fi

if [ "${#vms[@]}" -eq 0 ]; then
  echo "No matching VMs found; nothing to clean up."
  exit 0
fi

echo "Found ${#vms[@]} VM(s) to destroy:"
printf '  %s\n' "${vms[@]}"

echo "Waiting 10 secs before proceeding..."
sleep 10

echo "Powering off VMs (ignoring already-off)..."
"${GOVC}" vm.power -off -force "${vms[@]}" || true

echo "Destroying VMs..."
"${GOVC}" vm.destroy "${vms[@]}"

echo "vSphere cleanup complete."
