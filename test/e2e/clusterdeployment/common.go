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

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/K0rdent/kcm/test/e2e/kubeclient"
)

// PatchHostedClusterReady patches a hosted clusters' infrastructure resource
// as Ready depending on the given provider.
// See: https://docs.k0smotron.io/stable/capi-aws/#prepare-the-aws-infra-provider
// Use Eventually as the resource might not be available immediately following
// a ClusterDeployment creation.
func PatchHostedClusterReady(kc *kubeclient.KubeClient, provider ProviderType, clusterName string) {
	GinkgoHelper()

	ctx := context.Background()

	var (
		version  string
		resource string
	)

	switch provider {
	case ProviderAWS:
		version = "v1beta2"
		resource = "awsclusters"
	case ProviderAzure:
		version = "v1beta1"
		resource = "azureclusters"
	case ProviderOpenStack:
		version = "v1beta1"
		resource = "openstackclusters"
	case ProviderVSphere:
		return
	default:
		Fail(fmt.Sprintf("unsupported provider: %s", provider))
	}

	c := kc.GetDynamicClient(schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  version,
		Resource: resource,
	}, true)

	trueStatus := map[string]any{
		"status": map[string]any{
			"ready": true,
		},
	}

	patchBytes, err := json.Marshal(trueStatus)
	Expect(err).NotTo(HaveOccurred(), "failed to marshal patch bytes")

	Eventually(func() error {
		_, err = c.Patch(ctx, clusterName, types.MergePatchType,
			patchBytes, metav1.PatchOptions{}, "status")
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "Patch succeeded\n")
		return nil
	}).WithTimeout(time.Minute).WithPolling(5 * time.Second).Should(Succeed())
}
