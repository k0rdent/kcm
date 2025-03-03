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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/gomega"

	"github.com/K0rdent/kcm/test/utils"
)

// SupportBundle collects the support bundle from the specified cluster.
// If the clusterName is unset, it collects the support bundle from the management cluster.
func SupportBundle(clusterName string) {
	var args []string
	if clusterName != "" {
		dir, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())

		args = append(args, fmt.Sprintf("KUBECONFIG=%s", filepath.Join(dir, clusterName+"-kubeconfig")))
	}
	args = append(args, "support-bundle")
	cmd := exec.Command("make", args...)
	_, err := utils.Run(cmd)
	if err != nil {
		utils.WarnError(fmt.Errorf("failed to collect the support bundle: %w", err))
	}
}
