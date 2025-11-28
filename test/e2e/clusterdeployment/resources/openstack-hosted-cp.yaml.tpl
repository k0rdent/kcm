apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: openstack-cluster-identity-cred
  config:
    workersNumber: ${WORKERS_NUMBER:=1}
    flavor: ${OPENSTACK_NODE_MACHINE_FLAVOR}
    image:
      filter:
        name: ${OPENSTACK_IMAGE_NAME}
    identityRef:
      cloudName: ${OPENSTACK_CLOUD_NAME:=openstack}
      region: ${OS_REGION_NAME}
    network:
      filter: ${OPENSTACK_NETWORK_FILTER_JSON:={}}
    subnets:
      - filter: ${OPENSTACK_SUBNET_FILTER_JSON:={}}
    router:
      filter: ${OPENSTACK_ROUTER_FILTER_JSON:={}}

