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

// Package aws contains specific helpers for testing a cluster deployment
// that uses the AWS infrastructure provider.
package aws

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
)

func CheckEnv() {
	clusterdeployment.ValidateDeploymentVars([]string{
		clusterdeployment.EnvVarAWSAccessKeyID,
		clusterdeployment.EnvVarAWSSecretAccessKey,
	})
}

func PopulateStandaloneEnvVars(conf config.ProviderTestingConfig) {
	GinkgoHelper()

	PopulateEnvVars(conf.Architecture)
	if conf.Hosted != nil {
		GinkgoT().Setenv(clusterdeployment.EnvVarAWSInstanceType, instanceTypeXL(conf.Architecture))
	}
}

func PopulateEnvVars(arch config.Architecture) {
	GinkgoHelper()

	GinkgoT().Setenv(clusterdeployment.EnvVarAWSAMIID, amiID(arch))
	GinkgoT().Setenv(clusterdeployment.EnvVarAWSInstanceType, instanceTypeSmall(arch))
}

// PopulateHostedTemplateVars populates the environment variables required for
// the AWS hosted CP template by querying the standalone CP cluster with the
// given kubeclient.
func PopulateHostedTemplateVars(ctx context.Context, kc *kubeclient.KubeClient, architecture config.Architecture, clusterName string) {
	GinkgoHelper()

	c := kc.GetDynamicClient(schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  "v1beta2",
		Resource: "awsclusters",
	}, true)

	awsCluster, err := c.Get(ctx, clusterName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get AWS cluster")

	vpcID, found, err := unstructured.NestedString(awsCluster.Object, "spec", "network", "vpc", "id")
	Expect(err).NotTo(HaveOccurred(), "failed to get AWS cluster VPC ID")
	Expect(found).To(BeTrue(), "AWS cluster has no VPC ID")

	subnets, found, err := unstructured.NestedSlice(awsCluster.Object, "spec", "network", "subnets")
	Expect(err).NotTo(HaveOccurred(), "failed to get AWS cluster subnets")
	Expect(found).To(BeTrue(), "AWS cluster has no subnets")

	type awsSubnetMaps []map[string]any
	subnetMaps := make(awsSubnetMaps, len(subnets))
	for i, s := range subnets {
		subnet, ok := s.(map[string]any)
		Expect(ok).To(BeTrue(), "failed to cast subnet to map")
		subnetMaps[i] = map[string]any{
			"isPublic":         subnet["isPublic"],
			"availabilityZone": subnet["availabilityZone"],
			"id":               subnet["resourceID"],
			"routeTableId":     subnet["routeTableId"],
			"zoneType":         "availability-zone",
		}

		if natGatewayID, exists := subnet["natGatewayId"]; exists && natGatewayID != "" {
			subnetMaps[i]["natGatewayId"] = natGatewayID
		}
	}
	var subnetsFormatted string
	encodedYaml, err := yaml.Marshal(subnetMaps)
	Expect(err).NotTo(HaveOccurred(), "failed to get marshall subnet maps")
	scanner := bufio.NewScanner(strings.NewReader(string(encodedYaml)))
	for scanner.Scan() {
		subnetsFormatted += fmt.Sprintf("    %s\n", scanner.Text())
	}
	GinkgoT().Setenv(clusterdeployment.EnvVarAWSSubnets, subnetsFormatted)
	securityGroupID, found, err := unstructured.NestedString(
		awsCluster.Object, "status", "networkStatus", "securityGroups", "node", "id")
	Expect(err).NotTo(HaveOccurred(), "failed to get AWS cluster security group ID")
	Expect(found).To(BeTrue(), "AWS cluster has no security group ID")

	GinkgoT().Setenv(clusterdeployment.EnvVarAWSVPCID, vpcID)
	GinkgoT().Setenv(clusterdeployment.EnvVarAWSSecurityGroupID, securityGroupID)
	GinkgoT().Setenv(clusterdeployment.EnvVarManagementClusterName, clusterName)

	PopulateEnvVars(architecture)
}

func amiID(arch config.Architecture) string {
	switch arch {
	case config.ArchitectureArm64:
		return "ami-050499786ebf55a6a"
	default:
		return ""
	}
}

func instanceTypeSmall(arch config.Architecture) string {
	switch arch {
	case config.ArchitectureArm64:
		return "t4g.small"
	default:
		return "t3.small"
	}
}

func instanceTypeXL(arch config.Architecture) string {
	switch arch {
	case config.ArchitectureArm64:
		return "t4g.xlarge"
	default:
		return "t3.xlarge"
	}
}
