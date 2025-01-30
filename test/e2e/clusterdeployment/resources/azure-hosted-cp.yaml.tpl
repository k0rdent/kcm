apiVersion: k0rdent.mirantis.com/v1alpha1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: azure-hosted-cp-0-1-0
  credential: ${AZURE_CLUSTER_IDENTITY}-cred
  config:
    location: "${AZURE_REGION}"
    subscriptionID: "${AZURE_SUBSCRIPTION_ID}"
    vmSize: Standard_A4_v2
    clusterIdentity:
      name: ${AZURE_CLUSTER_IDENTITY}
      namespace: kcm-system
    resourceGroup: "${AZURE_RESOURCE_GROUP}"
    network:
      vnetName: "${AZURE_VM_NET_NAME}"
      nodeSubnetName: "${AZURE_NODE_SUBNET}"
      routeTableName: "${AZURE_ROUTE_TABLE}"
      securityGroupName: "${AZURE_SECURITY_GROUP}"
    tenantID: "${AZURE_TENANT_ID}"
    clientID: "${AZURE_CLIENT_ID}"
    clientSecret: "${AZURE_CLIENT_SECRET}"
