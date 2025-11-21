apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
  labels:
     k0rdent.mirantis.com/test-cluster-name: "${CLUSTER_DEPLOYMENT_NAME}"
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: docker-stub-credential
  config:
    clusterLabels: {}
    clusterAnnotations: {}
  serviceSpec:
    services:
      - template: ingress-nginx-4-11-3
        name: ${AWS_SERVICE_NAME:=managed-ingress-nginx}
        namespace: nginx