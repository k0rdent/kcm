apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: aws-cluster-identity-cred
  cleanupOnDeletion: true
  config:
    vpcID: ${AWS_VPC_ID}
    region: ${AWS_REGION}
    subnets:
${AWS_SUBNETS}
    amiID: ${AWS_AMI_ID}
    instanceType: ${AWS_INSTANCE_TYPE:=t3.medium}
    securityGroupIDs:
      - ${AWS_SG_ID}
    managementClusterName: ${MANAGEMENT_CLUSTER_NAME}
    controlPlane:
       rootVolumeSize: 30
    rootVolumeSize: 30
