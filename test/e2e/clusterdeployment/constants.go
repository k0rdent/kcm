// Copyright 2024
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package clusterdeployment

const (
	// Common
	EnvVarClusterDeploymentName     = "CLUSTER_DEPLOYMENT_NAME"
	EnvVarClusterDeploymentPrefix   = "CLUSTER_DEPLOYMENT_PREFIX"
	EnvVarClusterDeploymentTemplate = "CLUSTER_DEPLOYMENT_TEMPLATE"
	EnvVarNamespace                 = "NAMESPACE"
	// EnvVarNoCleanup disables After* cleanup in provider specs to allow for
	// debugging of test failures.
	EnvVarNoCleanup             = "NO_CLEANUP"
	EnvVarManagementClusterName = "MANAGEMENT_CLUSTER_NAME"

	EnvVarControlPlaneNumberNumber = "CONTROL_PLANE_NUMBER"
	EnvVarWorkersNumber            = "WORKERS_NUMBER"

	// AWS
	EnvVarAWSAccessKeyID     = "AWS_ACCESS_KEY_ID"
	EnvVarAWSSecretAccessKey = "AWS_SECRET_ACCESS_KEY"
	EnvVarAWSVPCID           = "AWS_VPC_ID"
	EnvVarAWSSubnets         = "AWS_SUBNETS"
	EnvVarAWSInstanceType    = "AWS_INSTANCE_TYPE"
	EnvVarAWSAMIID           = "AWS_AMI_ID"
	EnvVarAWSSecurityGroupID = "AWS_SG_ID"

	// VSphere
	EnvVarVSphereUser                       = "VSPHERE_USER"
	EnvVarVSpherePassword                   = "VSPHERE_PASSWORD"
	EnvVarVSphereServer                     = "VSPHERE_SERVER"
	EnvVarVSphereThumbprint                 = "VSPHERE_THUMBPRINT"
	EnvVarVSphereDatacenter                 = "VSPHERE_DATACENTER"
	EnvVarVSphereDatastore                  = "VSPHERE_DATASTORE"
	EnvVarVSphereResourcepool               = "VSPHERE_RESOURCEPOOL"
	EnvVarVSphereFolder                     = "VSPHERE_FOLDER"
	EnvVarVSphereControlPlaneEndpoint       = "VSPHERE_CONTROL_PLANE_ENDPOINT"
	EnvVarVSphereVMTemplate                 = "VSPHERE_VM_TEMPLATE"
	EnvVarVSphereNetwork                    = "VSPHERE_NETWORK"
	EnvVarVSphereSSHKey                     = "VSPHERE_SSH_KEY"
	EnvVarVSphereHostedControlPlaneEndpoint = "VSPHERE_HOSTED_CONTROL_PLANE_ENDPOINT"

	// Azure
	EnvVarAzureClientSecret = "AZURE_CLIENT_SECRET"
	EnvVarAzureClientID     = "AZURE_CLIENT_ID"
	EnvVarAzureTenantID     = "AZURE_TENANT_ID"
	EnvVarAzureSubscription = "AZURE_SUBSCRIPTION_ID"
	EnvVarAzureVMSize       = "AZURE_VM_SIZE"
	EnvVarAzureImageGallery = "AZURE_IMAGE_GALLERY"
	EnvVarAzureImageName    = "AZURE_IMAGE_NAME"
	EnvVarAzureImageVersion = "AZURE_IMAGE_VERSION"

	// GCP
	EnvVarGCPEncodedCredentials = "GCP_B64ENCODED_CREDENTIALS"
	EnvVarGCPProject            = "GCP_PROJECT"
	EnvVarGCPRegion             = "GCP_REGION"
	EnvVarGCPInstanceType       = "GCP_INSTANCE_TYPE"
	EnvVarGCPImage              = "GCP_IMAGE"
	EnvVarGCPRootDeviceType     = "GCP_ROOT_DEVICE_TYPE"

	// OpenStack
	EnvVarOpenStackCPFlavor          = "OPENSTACK_CONTROL_PLANE_MACHINE_FLAVOR"
	EnvVarOpenStackNodeFlavor        = "OPENSTACK_NODE_MACHINE_FLAVOR"
	EnvVarOpenStackImageName         = "OPENSTACK_IMAGE_NAME"
	EnvVarOpenStackExternalNetwork   = "OPENSTACK_EXTERNAL_NETWORK_NAME"
	EnvVarOpenStackCloudName         = "OPENSTACK_CLOUD_NAME"
	EnvVarOpenStackRegion            = "OS_REGION_NAME"
	EnvVarOpenStackNetworkFilterJSON = "OPENSTACK_NETWORK_FILTER_JSON"
	EnvVarOpenStackSubnetFilterJSON  = "OPENSTACK_SUBNET_FILTER_JSON"
	EnvVarOpenStackRouterFilterJSON  = "OPENSTACK_ROUTER_FILTER_JSON"

	// Adopted
	EnvVarAdoptedKubeconfigData = "KUBECONFIG_DATA"

	// Remote
	EnvVarPrivateSSHKeyB64 = "PRIVATE_SSH_KEY_B64"
)
