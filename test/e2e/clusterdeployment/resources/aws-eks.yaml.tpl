apiVersion: k0rdent.mirantis.com/v1alpha1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
spec:
  template: aws-eks-0-1-0
  credential: ${AWS_CLUSTER_IDENTITY}-cred
  config:
    region: ${AWS_REGION}
    workersNumber: ${WORKERS_NUMBER:=1}
    publicIP: ${AWS_PUBLIC_IP:=true}
    worker:
      instanceType: ${AWS_INSTANCE_TYPE:=t3.small}
