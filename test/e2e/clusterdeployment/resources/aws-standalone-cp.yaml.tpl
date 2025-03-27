apiVersion: k0rdent.mirantis.com/v1alpha1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: ${AWS_CLUSTER_IDENTITY}-cred
  config:
    region: ${AWS_REGION}
    publicIP: ${AWS_PUBLIC_IP:=false}
    controlPlaneNumber: ${CONTROL_PLANE_NUMBER:=1}
    workersNumber: ${WORKERS_NUMBER:=1}
    controlPlane:
      instanceType: ${AWS_INSTANCE_TYPE:=t3.small}
      rootVolumeSize: 30
    worker:
      instanceType: ${AWS_INSTANCE_TYPE:=t3.small}
      rootVolumeSize: 30
  serviceSpec:
    services:
      - template: ingress-nginx-4-12-1
        name: managed-ingress-nginx
        namespace: default
