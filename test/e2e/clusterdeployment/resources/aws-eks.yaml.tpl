apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: aws-cluster-identity-cred
  config:
    eksClusterName: ${CLUSTER_DEPLOYMENT_NAME}
    region: ${AWS_REGION}
    workersNumber: ${WORKERS_NUMBER:=1}
    publicIP: ${AWS_PUBLIC_IP:=true}
    worker:
      amiID: ${AMI_ID}
      instanceType: ${AWS_INSTANCE_TYPE:=t3.small}
