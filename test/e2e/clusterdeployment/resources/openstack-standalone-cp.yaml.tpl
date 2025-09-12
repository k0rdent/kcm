apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: openstack-cluster-identity-cred
  config:
    controlPlaneNumber: ${CONTROL_PLANE_NUMBER:=1}
    workersNumber: ${WORKERS_NUMBER:=1}
    controlPlane:
      flavor: ${OPENSTACK_CONTROL_PLANE_MACHINE_FLAVOR}
      image:
        filter:
          name: ${OPENSTACK_IMAGE_NAME}
    worker:
      flavor: ${OPENSTACK_NODE_MACHINE_FLAVOR}
      image:
        filter:
          name: ${OPENSTACK_IMAGE_NAME}
    externalNetwork:
      filter:
        name: ${OPENSTACK_EXTERNAL_NETWORK_NAME:=public}
    identityRef:
      cloudName: ${OPENSTACK_CLOUD_NAME:=openstack}
      region: ${OS_REGION_NAME}

