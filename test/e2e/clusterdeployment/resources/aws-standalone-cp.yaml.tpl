apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: aws-cluster-identity-cred
  config:
    region: ${AWS_REGION}
    publicIP: ${AWS_PUBLIC_IP:=false}
    controlPlaneNumber: ${CONTROL_PLANE_NUMBER:=1}
    workersNumber: ${WORKERS_NUMBER:=1}
    controlPlane:
      amiID: ${AWS_AMI_ID}
      instanceType: ${AWS_INSTANCE_TYPE:=t3.small}
      rootVolumeSize: 40
    worker:
      amiID: ${AWS_AMI_ID}
      instanceType: ${AWS_INSTANCE_TYPE:=t3.small}
      rootVolumeSize: 40
  serviceSpec:
    provider:
      name: ksm-projectsveltos
    services:
      - template: ingress-nginx-4-12-1
        name: ${AWS_SERVICE_NAME:=managed-ingress-nginx}
        namespace: default
