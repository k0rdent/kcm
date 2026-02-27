// Copyright 2025
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

// Package openstack contains specific helpers for testing a cluster deployment
// that uses the OpenStack infrastructure provider.
package openstack

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
)

// CheckEnv validates the presence of required OpenStack credentials env vars.
func CheckEnv() {
	clusterdeployment.ValidateDeploymentVars([]string{
		"OS_AUTH_URL",
		"OS_APPLICATION_CREDENTIAL_ID",
		"OS_APPLICATION_CREDENTIAL_SECRET",
		"OS_REGION_NAME",
		"OS_INTERFACE",
		"OS_IDENTITY_API_VERSION",
		"OS_AUTH_TYPE",
	})
}

// PopulateEnvVars sets architecture-dependent defaults and required template envs
// if they are not already provided in the environment.
// For now we require explicit values for flavors and image name, but we wire
// architecture to allow future defaults if desired.
func PopulateEnvVars(_ config.Architecture) {
	GinkgoHelper()
	// No strict defaults provided here to avoid accidental resource choices.
	// The e2e templates reference these env vars; ensure they're set upstream.
	clusterdeployment.ValidateDeploymentVars([]string{
		clusterdeployment.EnvVarOpenStackImageName,
		clusterdeployment.EnvVarOpenStackCPFlavor,
		clusterdeployment.EnvVarOpenStackNodeFlavor,
		clusterdeployment.EnvVarOpenStackRegion,
	})
}

// PopulateHostedTemplateVars reads network/subnet/router (and region) from the
// management's OpenStackCluster and sets env vars used by the hosted template.
// It also attempts to derive worker flavor/image from the MachineDeployment's
// OpenStackMachineTemplate.
func PopulateHostedTemplateVars(ctx context.Context, managementClient *kubeclient.KubeClient, standaloneClusterName string) error {
	GinkgoHelper()

	osc := getOpenStackCluster(ctx, managementClient, standaloneClusterName)
	if err := populateNetworkVars(osc); err != nil {
		return fmt.Errorf("failed to populate network vars from the OpenstackCluster %s/%s: %w", osc.GetNamespace(), osc.GetName(), err)
	}

	if err := populateIdentityVars(osc); err != nil {
		return fmt.Errorf("failed to populate identity reference vars from the OpenstackCluster %s/%s: %w", osc.GetNamespace(), osc.GetName(), err)
	}

	if err := populateMachineVars(ctx, managementClient, standaloneClusterName); err != nil {
		return fmt.Errorf("failed to populate machine vars from the OpenstackCluster %s/%s: %w", osc.GetNamespace(), osc.GetName(), err)
	}

	return nil
}

func getOpenStackCluster(ctx context.Context, mgmtClient *kubeclient.KubeClient, clusterName string) *unstructured.Unstructured {
	GinkgoHelper()

	oscGVR := schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  "v1beta1",
		Resource: "openstackclusters",
	}
	oscCli := mgmtClient.GetDynamicClient(oscGVR, true)
	var openstackCluster *unstructured.Unstructured
	Eventually(func() error {
		var err error
		openstackCluster, err = oscCli.Get(ctx, clusterName, metav1.GetOptions{})
		if err != nil {
			logs.Printf("waiting for OpenStackCluster %q: %v", clusterName, err)
		}
		return err
	}, 5*time.Minute, 10*time.Second).Should(Succeed(), "failed to get OpenStackCluster")

	return openstackCluster
}

func populateNetworkVars(openstackCluster *unstructured.Unstructured) error {
	GinkgoHelper()

	networkName, found, _ := unstructured.NestedString(openstackCluster.Object, "status", "network", "name")
	if !found || len(networkName) == 0 {
		return fmt.Errorf("no network name found in the status of the OpenstackCluster %s/%s", openstackCluster.GetNamespace(), openstackCluster.GetName())
	}

	logs.Printf("Setting env %q: %s", clusterdeployment.EnvVarOpenStackNetworkFilter, networkName)
	GinkgoT().Setenv(clusterdeployment.EnvVarOpenStackNetworkFilter, networkName)
	logs.Printf("Setting env %q: %s", clusterdeployment.EnvVarOpenStackPortFilter, networkName)
	GinkgoT().Setenv(clusterdeployment.EnvVarOpenStackPortFilter, networkName)

	subs, found, _ := unstructured.NestedSlice(openstackCluster.Object, "status", "network", "subnets")
	if !found || len(subs) == 0 {
		return fmt.Errorf("no network subnets found in the status of the OpenstackCluster %s/%s", openstackCluster.GetNamespace(), openstackCluster.GetName())
	}

	subnet, ok := subs[0].(map[string]any)
	if !ok {
		return fmt.Errorf("wrong type of the subnet %T found in the status of the OpenstackCluster %s/%s", subs[0], openstackCluster.GetNamespace(), openstackCluster.GetName())
	}

	subnetName, ok := subnet["name"].(string)
	if !ok || len(subnetName) == 0 {
		return fmt.Errorf("no network subnet name found in the status of the OpenstackCluster %s/%s", openstackCluster.GetNamespace(), openstackCluster.GetName())
	}

	logs.Printf("Setting env %q: %s", clusterdeployment.EnvVarOpenStackSubnetFilter, subnetName)
	GinkgoT().Setenv(clusterdeployment.EnvVarOpenStackSubnetFilter, subnetName)

	routerName, found, _ := unstructured.NestedString(openstackCluster.Object, "status", "router", "name")
	if !found || len(routerName) == 0 {
		return fmt.Errorf("no network router name found in the status of the OpenstackCluster %s/%s", openstackCluster.GetNamespace(), openstackCluster.GetName())
	}

	logs.Printf("Setting env %q: %s", clusterdeployment.EnvVarOpenStackRouterFilter, routerName)
	GinkgoT().Setenv(clusterdeployment.EnvVarOpenStackRouterFilter, routerName)

	return nil
}

func populateIdentityVars(openstackCluster *unstructured.Unstructured) error {
	GinkgoHelper()

	specIDRef, found, _ := unstructured.NestedMap(openstackCluster.Object, "spec", "identityRef")
	if !found || len(specIDRef) == 0 {
		return fmt.Errorf("no Identity Reference found in the spec of the OpenstackCluster %s/%s", openstackCluster.GetNamespace(), openstackCluster.GetName())
	}

	region, ok := specIDRef["region"].(string)
	if !ok || len(region) == 0 {
		return fmt.Errorf("identity reference of the OpenstackCluster %s/%s has no region set", openstackCluster.GetNamespace(), openstackCluster.GetName())
	}
	logs.Printf("Setting env %q: %s", clusterdeployment.EnvVarOpenStackRegion, region)
	GinkgoT().Setenv(clusterdeployment.EnvVarOpenStackRegion, region)

	cloud, ok := specIDRef["cloudName"].(string)
	if !ok && len(cloud) == 0 {
		return fmt.Errorf("identity reference of the OpenstackCluster %s/%s has no cloud name set", openstackCluster.GetNamespace(), openstackCluster.GetName())
	}
	logs.Printf("Setting env %q: %s", clusterdeployment.EnvVarOpenStackCloudName, cloud)
	GinkgoT().Setenv(clusterdeployment.EnvVarOpenStackCloudName, cloud)

	return nil
}

func populateMachineVars(ctx context.Context, mgmtClient *kubeclient.KubeClient, clusterName string) error {
	GinkgoHelper()

	mdGVR := schema.GroupVersionResource{
		Group:    "cluster.x-k8s.io",
		Version:  "v1beta1",
		Resource: "machinedeployments",
	}
	mdCli := mgmtClient.GetDynamicClient(mdGVR, true)
	var machineDeployments *unstructured.UnstructuredList
	Eventually(func() error {
		var err error
		machineDeployments, err = mdCli.List(ctx, metav1.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"cluster.x-k8s.io/cluster-name": clusterName,
			}).String(),
		})
		if err != nil {
			logs.Printf("waiting for MachineDeployments: %v", err)
		}
		return err
	}, 5*time.Minute, 10*time.Second).Should(Succeed(), "failed to list MachineDeployments")

	if len(machineDeployments.Items) == 0 {
		return fmt.Errorf("machine deployment list is empty")
	}

	infraRefName, _, _ := unstructured.NestedString(machineDeployments.Items[0].Object, "spec", "template", "spec", "infrastructureRef", "name")
	if infraRefName == "" {
		return fmt.Errorf("machinedeployments %s/%s has empty '.spec.template.spec.infrastructureRef.name'", machineDeployments.Items[0].GetNamespace(), machineDeployments.Items[0].GetName())
	}

	osmtGVR := schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  "v1beta1",
		Resource: "openstackmachinetemplates",
	}
	osmtCli := mgmtClient.GetDynamicClient(osmtGVR, true)
	var openstackMachineTemplate *unstructured.Unstructured
	Eventually(func() error {
		var err error
		openstackMachineTemplate, err = osmtCli.Get(ctx, infraRefName, metav1.GetOptions{})
		if err != nil {
			logs.Printf("waiting for OpenStackMachineTemplate %q: %v", infraRefName, err)
		}
		return err
	}, 5*time.Minute, 10*time.Second).Should(Succeed(), "failed to get OpenStackMachineTemplate")

	flavor, found, _ := unstructured.NestedString(openstackMachineTemplate.Object, "spec", "template", "spec", "flavor")
	if !found || len(flavor) == 0 {
		return fmt.Errorf("openstackmachinetemplates %s/%s has no flavor set", openstackMachineTemplate.GetNamespace(), openstackMachineTemplate.GetName())
	}

	logs.Printf("Setting env %q: %s", clusterdeployment.EnvVarOpenStackNodeFlavor, flavor)
	GinkgoT().Setenv(clusterdeployment.EnvVarOpenStackNodeFlavor, flavor)

	imgFilter, found, _ := unstructured.NestedMap(openstackMachineTemplate.Object, "spec", "template", "spec", "image", "filter")
	if !found || len(imgFilter) == 0 {
		return fmt.Errorf("openstackmachinetemplates %s/%s has no image filter set", openstackMachineTemplate.GetNamespace(), openstackMachineTemplate.GetName())
	}

	imgFilterName, ok := imgFilter["name"].(string)
	if !ok || len(imgFilterName) == 0 {
		return fmt.Errorf("openstackmachinetemplates %s/%s has no image filter name set", openstackMachineTemplate.GetNamespace(), openstackMachineTemplate.GetName())
	}

	logs.Printf("Setting env %q: %s", clusterdeployment.EnvVarOpenStackImageName, imgFilterName)
	GinkgoT().Setenv(clusterdeployment.EnvVarOpenStackImageName, imgFilterName)

	return nil
}
