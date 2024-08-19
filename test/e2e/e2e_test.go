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

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Mirantis/hmc/test/kubeclient"
	"github.com/Mirantis/hmc/test/utils"
)

const namespace = "hmc-system"

var _ = Describe("controller", Ordered, func() {
	// BeforeAll(func() {
	// 	By("building and deploying the controller-manager")
	// 	cmd := exec.Command("make", "dev-apply")
	// 	_, err := utils.Run(cmd)
	// 	Expect(err).NotTo(HaveOccurred())
	// })

	// AfterAll(func() {
	// 	By("removing the controller-manager")
	// 	cmd := exec.Command("make", "dev-destroy")
	// 	_, err := utils.Run(cmd)
	// 	Expect(err).NotTo(HaveOccurred())
	// })

	Context("Operator", func() {
		It("should run successfully", func() {
			kc, err := kubeclient.New(namespace)
			ExpectWithOffset(2, err).NotTo(HaveOccurred())

			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func() error {
				// Ensure only one controller pod is running.
				podList, err := kc.Client.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
					LabelSelector: "control-plane=controller-manager,app.kubernetes.io/name=cluster-api",
				})
				if err != nil {
					return err
				}

				if len(podList.Items) != 1 {
					return fmt.Errorf("expected 1 controller pod, got %d", len(podList.Items))
				}

				controllerPod := podList.Items[0]

				if controllerPod.DeletionTimestamp != nil {
					return fmt.Errorf("deletion timestamp should be nil, got: %v", controllerPod)
				}
				if !strings.Contains(controllerPod.Name, "controller-manager") {
					return fmt.Errorf("controller pod name %s does not contain 'controller-manager'", controllerPod.Name)
				}
				if controllerPod.Status.Phase != "Running" {
					return fmt.Errorf("controller pod in %s status", controllerPod.Status.Phase)
				}

				return nil
			}()
			EventuallyWithOffset(1, verifyControllerUp, time.Minute, time.Second).Should(Succeed())
		})
	})

	Context("AWS Templates", func() {
		BeforeAll(func() {
			By("ensuring AWS credentials are set")
			kc, err := kubeclient.New(namespace)
			ExpectWithOffset(2, err).NotTo(HaveOccurred())
			ExpectWithOffset(2, kc.CreateAWSCredentialsKubeSecret(context.Background())).To(Succeed())
		})

		It("should work with an AWS provider", func() {
			By("using the aws-standalone-cp template")
			ExpectWithOffset(2, utils.ConfigureDeploymentConfig()).To(Succeed())

			cmd := exec.Command("make", "dev-aws-apply")
			_, err := utils.Run(cmd)
			ExpectWithOffset(2, err).NotTo(HaveOccurred())

			EventuallyWithOffset(2, func() error {
				return nil
			})

		})
	})
})
