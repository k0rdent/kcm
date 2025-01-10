# This config file is used by azure-nuke to clean up named resources associated
# with a specific managed cluster across an Azure account. CLUSTER_NAME is
# typically the metadata.name of the ClusterDeployment.
# This will nuke the ResourceGroup affiliated with the ClusterDeployment.
#
# Usage:
# 'CLUSTER_NAME=foo AZURE_REGION=westus3 AZURE_TENANT_ID=12345 make dev-azure-nuke' 
# 
# Check cluster names with 'kubectl get clusterdeployment.k0rdent.mirantis.com -n kcm-system'

regions:
  - global
  - ${AZURE_REGION}

resource-types:
  includes:
    - ResourceGroup

accounts:
  ${AZURE_TENANT_ID}:
    filters:
       __global__:
        - ResourceGroup:
          type: "glob"
          value: "${CLUSTER_NAME}*"
          invert: true
