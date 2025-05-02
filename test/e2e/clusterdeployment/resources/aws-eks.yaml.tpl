apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: ${AWS_CLUSTER_IDENTITY}-cred
  config:
    eksClusterName: ${CLUSTER_DEPLOYMENT_NAME}
    region: ${AWS_REGION}
    workersNumber: ${WORKERS_NUMBER:=1}
    publicIP: ${AWS_PUBLIC_IP:=true}
    worker:
      instanceType: ${AWS_INSTANCE_TYPE:=t3.small}
