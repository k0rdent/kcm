apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: vsphere-cluster-identity-cred
  config:
    controlPlaneNumber: ${CONTROL_PLANE_NUMBER:=1}
    workersNumber: ${WORKERS_NUMBER:=1}
    vsphere:
      server: "${VSPHERE_SERVER}"
      thumbprint: "${VSPHERE_THUMBPRINT} "
      datacenter: "${VSPHERE_DATACENTER}"
      datastore: "${VSPHERE_DATASTORE}"
      resourcePool: "${VSPHERE_RESOURCEPOOL}"
      folder: "${VSPHERE_FOLDER}"
      username: "${VSPHERE_USER}"
      password: "${VSPHERE_PASSWORD}"
    controlPlaneEndpointIP: "${VSPHERE_HOSTED_CONTROL_PLANE_ENDPOINT}"

    ssh:
      user: ubuntu
      publicKey: "${VSPHERE_SSH_KEY}"
    rootVolumeSize: 50
    cpus: 4
    memory: 4096
    vmTemplate: "${VSPHERE_VM_TEMPLATE}"
    network: "${VSPHERE_NETWORK}"

    k0smotron:
      service:
        annotations:
          kube-vip.io/loadbalancerIPs: "${VSPHERE_HOSTED_CONTROL_PLANE_ENDPOINT}"
