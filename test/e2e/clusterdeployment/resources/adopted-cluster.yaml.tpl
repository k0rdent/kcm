apiVersion: k0rdent.mirantis.com/v1alpha1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: adopted-cluster-0-0-2
  credential: ${ADOPTED_CREDENTIAL}
  config: {}
  serviceSpec:
    - template: kyverno-3-2-6
      name: kyverno
      namespace: kyverno
    - template: ingress-nginx-4-11-0
      name: ingress-nginx
      namespace: ingress-nginx
