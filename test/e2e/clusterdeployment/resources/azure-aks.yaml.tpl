apiVersion: k0rdent.mirantis.com/v1alpha1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: azure-aks-credential
  propagateCredentials: false
  config:
    clusterLabels: {}
    location: "${AZURE_REGION}"
    machinePools:
      system:
        count: 1
        vmSize: Standard_A4_v2
      user:
        count: 1
        vmSize: Standard_A4_v2
