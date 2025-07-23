apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: azure-cluster-identity-cred
  config:
    location: "${AZURE_REGION}"
    subscriptionID: "${AZURE_SUBSCRIPTION_ID}"
    vmSize: ${AZURE_VM_SIZE:=Standard_A4_v2}
    rootVolumeSize: 50
    image:
      computeGallery:
        gallery: ${AZURE_IMAGE_GALLERY}
        name: ${AZURE_IMAGE_NAME}
        version: ${AZURE_IMAGE_VERSION}
    resourceGroup: "${AZURE_RESOURCE_GROUP}"
    network:
      vnetName: "${AZURE_VM_NET_NAME}"
      nodeSubnetName: "${AZURE_NODE_SUBNET}"
      routeTableName: "${AZURE_ROUTE_TABLE}"
      securityGroupName: "${AZURE_SECURITY_GROUP}"
    tenantID: "${AZURE_TENANT_ID}"
    clientID: "${AZURE_CLIENT_ID}"
    clientSecret: "${AZURE_CLIENT_SECRET}"
