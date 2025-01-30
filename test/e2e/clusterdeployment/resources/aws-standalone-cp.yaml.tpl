apiVersion: k0rdent.mirantis.com/v1alpha1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
spec:
  template: aws-standalone-cp-0-1-0
  credential: ${AWS_CLUSTER_IDENTITY}-cred
  config:
    clusterIdentity:
      name:  ${AWS_CLUSTER_IDENTITY}
      namespace: ${NAMESPACE}
    region: ${AWS_REGION}
    publicIP: ${AWS_PUBLIC_IP:=true}
    controlPlaneNumber: ${CONTROL_PLANE_NUMBER:=1}
    workersNumber: ${WORKERS_NUMBER:=1}
    controlPlane:
      instanceType: ${AWS_INSTANCE_TYPE:=t3.small}
      rootVolumeSize: 30
    worker:
      instanceType: ${AWS_INSTANCE_TYPE:=t3.small}
      rootVolumeSize: 30
