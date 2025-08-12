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

package logs

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/utils"
)

// SupportBundle collects the support bundle from the specified cluster.
// If the clusterName is unset, it collects the support bundle from the management cluster.
func SupportBundle(kc *kubeclient.KubeClient, clusterName string) {
	var (
		args        []string
		kubeCfgPath string
		cleanupFunc func() error
		err         error
	)
	if clusterName != "" {
		kubeCfgPath, _, cleanupFunc, err = kc.WriteKubeconfig(context.Background(), clusterName)
		if err != nil {
			utils.WarnError(fmt.Errorf("failed to write %s cluster kubeconfig: %w", clusterName, err))
			return
		}

		args = append(args, fmt.Sprintf("KUBECONFIG=%s", kubeCfgPath))
	}
	args = append(args, "support-bundle")
	cmd := exec.Command("make", args...)
	if _, err = utils.Run(cmd); err != nil {
		utils.WarnError(fmt.Errorf("failed to collect the support bundle: %w", err))
	}
	if cleanupFunc != nil {
		err = cleanupFunc()
		Expect(err).NotTo(HaveOccurred())
	}
}

func Println(msg string) {
	timestamp := time.Now().Format(time.DateTime)
	_, _ = fmt.Fprintf(GinkgoWriter, "[%s] %s\n", timestamp, msg)
}
