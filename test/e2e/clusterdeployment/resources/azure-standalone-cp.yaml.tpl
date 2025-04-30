apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: azure-cluster-identity-cred
  config:
    controlPlaneNumber: 1
    workersNumber: 1
    location: "${AZURE_REGION}"
    subscriptionID: "${AZURE_SUBSCRIPTION_ID}"
    controlPlane:
      vmSize: Standard_A4_v2
    worker:
      vmSize: Standard_A4_v2
    tenantID: "${AZURE_TENANT_ID}"
    clientID: "${AZURE_CLIENT_ID}"
    clientSecret: "${AZURE_CLIENT_SECRET}"
