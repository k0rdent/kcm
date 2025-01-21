apiVersion: k0rdent.mirantis.com/v1alpha1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
spec:
  template: aws-hosted-cp-0-0-4
  credential: ${AWS_CLUSTER_IDENTITY}-cred
  config:
    clusterIdentity:
      name: ${AWS_CLUSTER_IDENTITY}
      namespace: ${NAMESPACE}
    vpcID: ${AWS_VPC_ID}
    region: ${AWS_REGION}
    subnets:
${AWS_SUBNETS}
    instanceType: ${AWS_INSTANCE_TYPE:=t3.medium}
    securityGroupIDs:
      - ${AWS_SG_ID}
    managementClusterName: ${MANAGEMENT_CLUSTER_NAME}
    controlPlane:
       rootVolumeSize: 30
    rootVolumeSize: 30