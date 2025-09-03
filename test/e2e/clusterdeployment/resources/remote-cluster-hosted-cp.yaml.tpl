apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: remote-cred
  propagateCredentials: false
  config:
    k0smotron:
      service:
        type: NodePort
    machines:
      - address: ${MACHINE_0_ADDRESS}
        user: root
        port: ${MACHINE_0_PORT}
      - address: ${MACHINE_1_ADDRESS}
        user: root
        port: ${MACHINE_1_PORT}
