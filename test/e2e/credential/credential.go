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

package credential

import (
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/K0rdent/kcm/test/utils"
)

func Apply(kubeconfigPath string, providers ...string) {
	for _, provider := range providers {
		By(fmt.Sprintf("Applying %s credentials", provider))
		Eventually(func() error {
			var args []string
			if kubeconfigPath != "" {
				args = append(args, "KUBECONFIG="+kubeconfigPath)
			}
			args = append(args, "DEV_PROVIDER="+provider, "dev-creds-apply")

			cmd := exec.Command("make", args...)
			_, err := utils.Run(cmd)
			return err
		}).WithTimeout(5 * time.Minute).WithPolling(time.Minute).Should(Succeed())
	}
}
