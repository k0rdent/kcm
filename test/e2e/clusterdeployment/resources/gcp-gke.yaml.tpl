apiVersion: k0rdent.mirantis.com/v1alpha1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: gcp-credential
  config:
    workersNumber: 1
    clusterAnnotations: {}
    project: ${GCP_PROJECT}
    region: ${GCP_REGION}
    network:
      name: ${CLUSTER_DEPLOYMENT_NAME}
    releaseChannel: stable
    machines:
      nodeLocations:
      - ${GCP_REGION}-a
