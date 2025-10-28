apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  name: ${CLUSTER_DEPLOYMENT_NAME}
  namespace: ${NAMESPACE}
spec:
  template: ${CLUSTER_DEPLOYMENT_TEMPLATE}
  credential: azure-cluster-identity-cred
  cleanupOnDeletion: true
  config:
    controlPlaneNumber: ${CONTROL_PLANE_NUMBER:=1}
    workersNumber: ${WORKERS_NUMBER:=1}
    location: "${AZURE_REGION}"
    subscriptionID: "${AZURE_SUBSCRIPTION_ID}"
    controlPlane:
      vmSize: ${AZURE_VM_SIZE:=Standard_A4_v2}
      rootVolumeSize: 50
      image:
        computeGallery:
          gallery: ${AZURE_IMAGE_GALLERY}
          name: ${AZURE_IMAGE_NAME}
          version: ${AZURE_IMAGE_VERSION}
    worker:
      vmSize: ${AZURE_VM_SIZE:=Standard_A4_v2}
      rootVolumeSize: 50
      image:
        computeGallery:
          gallery: ${AZURE_IMAGE_GALLERY}
          name: ${AZURE_IMAGE_NAME}
          version: ${AZURE_IMAGE_VERSION}
