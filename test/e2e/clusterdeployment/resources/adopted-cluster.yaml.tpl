apiVersion: k0rdent.mirantis.com/v1alpha1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: adopted-cluster-0-2-0
  credential: ${ADOPTED_CREDENTIAL}
  config: {}
