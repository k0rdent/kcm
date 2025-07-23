apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: gcp-credential
  config:
    project: ${GCP_PROJECT}
    region: ${GCP_REGION}
    network:
      name: default
    controlPlaneNumber: 1
    worker:
      instanceType: ${GCP_INSTANCE_TYPE:=n1-standard-2}
      image: ${GCP_IMAGE:=projects/ubuntu-os-cloud/global/images/ubuntu-2004-focal-v20250213}
      rootDeviceType: ${GCP_ROOT_DEVICE_TYPE:=pd-standard}
      publicIP: true
    workersNumber: 1
