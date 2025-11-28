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
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
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
// standalone OpenStackCluster and sets env vars used by the hosted template.
// It also attempts to derive worker flavor/image from the MachineDeployment's
// OpenStackMachineTemplate.
func PopulateHostedTemplateVars(ctx context.Context, kc *kubeclient.KubeClient, standaloneClusterName string) {
	GinkgoHelper()

	osc := getOpenStackCluster(ctx, kc, standaloneClusterName)
	populateNetworkVars(osc)
	populateIdentityVars(osc)
	populateMachineVars(ctx, kc, standaloneClusterName)
}

func getOpenStackCluster(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) *unstructured.Unstructured {
	oscGVR := schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  "v1beta1",
		Resource: "openstackclusters",
	}
	oscCli := kc.GetDynamicClient(oscGVR, true)
	osc, err := oscCli.Get(ctx, clusterName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get OpenStackCluster")
	return osc
}

func setJSONFilter(envName, key, val string) {
	if val == "" {
		return
	}
	m := map[string]string{key: val}
	b, err := json.Marshal(m)
	Expect(err).NotTo(HaveOccurred(), "failed to marshal JSON filter")
	GinkgoT().Setenv(envName, string(b))
}

func populateNetworkVars(osc *unstructured.Unstructured) {
	if netMap, found, _ := unstructured.NestedMap(osc.Object, "status", "network"); found {
		if name, ok := netMap["name"].(string); ok && name != "" {
			setJSONFilter(clusterdeployment.EnvVarOpenStackNetworkFilterJSON, "name", name)
		} else if id, ok := netMap["id"].(string); ok && id != "" {
			setJSONFilter(clusterdeployment.EnvVarOpenStackNetworkFilterJSON, "id", id)
		}
	}

	if subs, found, _ := unstructured.NestedSlice(osc.Object, "status", "network", "subnets"); found && len(subs) > 0 {
		if sub, ok := subs[0].(map[string]any); ok {
			if name, ok := sub["name"].(string); ok && name != "" {
				setJSONFilter(clusterdeployment.EnvVarOpenStackSubnetFilterJSON, "name", name)
			} else if id, ok := sub["id"].(string); ok && id != "" {
				setJSONFilter(clusterdeployment.EnvVarOpenStackSubnetFilterJSON, "id", id)
			}
		}
	}

	if rMap, found, _ := unstructured.NestedMap(osc.Object, "status", "router"); found {
		if name, ok := rMap["name"].(string); ok && name != "" {
			setJSONFilter(clusterdeployment.EnvVarOpenStackRouterFilterJSON, "name", name)
		} else if id, ok := rMap["id"].(string); ok && id != "" {
			setJSONFilter(clusterdeployment.EnvVarOpenStackRouterFilterJSON, "id", id)
		}
	}
}

func populateIdentityVars(osc *unstructured.Unstructured) {
	if specIDRef, found, _ := unstructured.NestedMap(osc.Object, "spec", "identityRef"); found {
		if region, ok := specIDRef["region"].(string); ok && region != "" {
			GinkgoT().Setenv(clusterdeployment.EnvVarOpenStackRegion, region)
		}
		if cloud, ok := specIDRef["cloudName"].(string); ok && cloud != "" {
			GinkgoT().Setenv(clusterdeployment.EnvVarOpenStackCloudName, cloud)
		}
	}
}

func populateMachineVars(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) {
	mdGVR := schema.GroupVersionResource{
		Group:    "cluster.x-k8s.io",
		Version:  "v1beta1",
		Resource: "machinedeployments",
	}
	mdCli := kc.GetDynamicClient(mdGVR, true)
	mds, err := mdCli.List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{"cluster.x-k8s.io/cluster-name": clusterName}).String(),
	})
	Expect(err).NotTo(HaveOccurred(), "failed to list MachineDeployments")
	if len(mds.Items) == 0 {
		return
	}

	infraRefName, _, _ := unstructured.NestedString(mds.Items[0].Object, "spec", "template", "spec", "infrastructureRef", "name")
	if infraRefName == "" {
		return
	}

	osmtGVR := schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  "v1beta1",
		Resource: "openstackmachinetemplates",
	}
	osmtCli := kc.GetDynamicClient(osmtGVR, true)
	mt, err := osmtCli.Get(ctx, infraRefName, metav1.GetOptions{})
	if err != nil {
		return
	}

	if flavor, _, _ := unstructured.NestedString(mt.Object, "spec", "template", "spec", "flavor"); flavor != "" {
		GinkgoT().Setenv(clusterdeployment.EnvVarOpenStackNodeFlavor, flavor)
	}
	if imgFilter, found, _ := unstructured.NestedMap(mt.Object, "spec", "template", "spec", "image", "filter"); found {
		if name, ok := imgFilter["name"].(string); ok && name != "" {
			GinkgoT().Setenv(clusterdeployment.EnvVarOpenStackImageName, name)
		}
	}
}
