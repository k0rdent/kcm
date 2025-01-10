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
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/utils"
)

// Run e2e tests using the Ginkgo runner.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting kcm suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	GinkgoT().Setenv(clusterdeployment.EnvVarNamespace, internalutils.DefaultSystemNamespace)

	By("building and deploying the controller-manager")
	cmd := exec.Command("make", "kind-deploy")
	_, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())
	cmd = exec.Command("make", "test-apply")
	_, err = utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())

	By("validating that the kcm-controller and CAPI provider controllers are running and ready")
	kc := kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)
	Eventually(func() error {
		err = verifyControllersUp(kc)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Controller validation failed: %v\n", err)
			return err
		}
		return nil
	}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
})

var _ = AfterSuite(func() {
	if !noCleanup() {
		By("collecting logs from local controllers")
		kc := kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)
		collectLogArtifacts(kc, "")

		By("removing the controller-manager")
		cmd := exec.Command("make", "dev-destroy")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
	}
})

// verifyControllersUp validates that controllers for all providers are running
// and ready.
func verifyControllersUp(kc *kubeclient.KubeClient) error {
	if err := validateController(kc, utils.KCMControllerLabel, "kcm-controller-manager"); err != nil {
		return err
	}

	providers := []clusterdeployment.ProviderType{
		clusterdeployment.ProviderCAPI,
		clusterdeployment.ProviderAWS,
		clusterdeployment.ProviderAzure,
		clusterdeployment.ProviderVSphere,
	}

	for _, provider := range providers {
		// Ensure only one controller pod is running.
		if err := validateController(kc, clusterdeployment.GetProviderLabel(provider), string(provider)); err != nil {
			return err
		}
	}

	return nil
}

func validateController(kc *kubeclient.KubeClient, labelSelector, name string) error {
	controllerItems := 1
	if strings.Contains(labelSelector, clusterdeployment.GetProviderLabel(clusterdeployment.ProviderAzure)) {
		// Azure provider has two controllers.
		controllerItems = 2
	}

	deployList, err := kc.Client.AppsV1().Deployments(kc.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: labelSelector,
		Limit:         int64(controllerItems),
	})
	if err != nil {
		return fmt.Errorf("failed to list %s controller deployments: %w", name, err)
	}

	if len(deployList.Items) < controllerItems {
		return fmt.Errorf("expected at least %d %s controller deployments, got %d", controllerItems, name, len(deployList.Items))
	}

	for _, deployment := range deployList.Items {
		// Ensure the deployment is not being deleted.
		if deployment.DeletionTimestamp != nil {
			return fmt.Errorf("controller pod: %s deletion timestamp should be nil, got: %v",
				deployment.Name, deployment.DeletionTimestamp)
		}
		// Ensure the deployment is running and has the expected name.
		if !strings.Contains(deployment.Name, "controller-manager") {
			return fmt.Errorf("controller deployment name %s does not contain 'controller-manager'", deployment.Name)
		}
		if deployment.Status.ReadyReplicas < 1 {
			return fmt.Errorf("controller deployment: %s does not yet have any ReadyReplicas", deployment.Name)
		}
	}

	return nil
}

// templateBy wraps a Ginkgo By with a block describing the template being
// tested.
func templateBy(t clusterdeployment.Template, description string) {
	GinkgoHelper()
	By(fmt.Sprintf("[%s] %s", t, description))
}

// collectLogArtifacts collects log output from each the KCM controller,
// CAPI controller and the provider controller(s) as well as output from clusterctl
// and stores them in the test/e2e directory as artifacts. clusterName can be
// optionally provided, passing an empty string will prevent clusterctl output
// from being fetched.  If collectLogArtifacts fails it produces a warning
// message to the GinkgoWriter, but does not fail the test.
func collectLogArtifacts(kc *kubeclient.KubeClient, clusterName string, providerTypes ...clusterdeployment.ProviderType) {
	GinkgoHelper()

	filterLabels := []string{utils.KCMControllerLabel}

	var host string
	hostURL, err := url.Parse(kc.Config.Host)
	if err != nil {
		utils.WarnError(fmt.Errorf("failed to parse host from kubeconfig: %w", err))
	} else {
		host = strings.ReplaceAll(hostURL.Host, ":", "_")
	}

	if providerTypes == nil {
		filterLabels = clusterdeployment.FilterAllProviders()
	} else {
		for _, providerType := range providerTypes {
			filterLabels = append(filterLabels, clusterdeployment.GetProviderLabel(providerType))
		}
	}

	for _, label := range filterLabels {
		pods, _ := kc.Client.CoreV1().Pods(kc.Namespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: label,
		})

		for _, pod := range pods.Items {
			req := kc.Client.CoreV1().Pods(kc.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
				TailLines: ptr.To(int64(1000)),
			})
			podLogs, err := req.Stream(context.Background())
			if err != nil {
				utils.WarnError(fmt.Errorf("failed to get log stream for pod %s: %w", pod.Name, err))
				continue
			}

			output, err := os.Create(fmt.Sprintf("./test/e2e/%s.log", host+"-"+pod.Name))
			if err != nil {
				utils.WarnError(fmt.Errorf("failed to create log file for pod %s: %w", pod.Name, err))
				continue
			}

			r := bufio.NewReader(podLogs)
			_, err = r.WriteTo(output)
			if err != nil {
				utils.WarnError(fmt.Errorf("failed to write log file for pod %s: %w", pod.Name, err))
			}

			if err = podLogs.Close(); err != nil {
				utils.WarnError(fmt.Errorf("failed to close log stream for pod %s: %w", pod.Name, err))
			}
			if err = output.Close(); err != nil {
				utils.WarnError(fmt.Errorf("failed to close log file for pod %s: %w", pod.Name, err))
			}
		}
	}

	if clusterName != "" {
		cmd := exec.Command("./bin/clusterctl",
			"describe", "cluster", clusterName, "--namespace", internalutils.DefaultSystemNamespace, "--show-conditions=all")
		output, err := utils.Run(cmd)
		if err != nil {
			utils.WarnError(fmt.Errorf("failed to get clusterctl log: %w", err))
			return
		}
		err = os.WriteFile(filepath.Join("test/e2e", host+"-"+"clusterctl.log"), output, 0o644)
		if err != nil {
			utils.WarnError(fmt.Errorf("failed to write clusterctl log: %w", err))
		}
	}
}

func noCleanup() bool {
	noCleanup := os.Getenv(clusterdeployment.EnvVarNoCleanup)
	if noCleanup != "" {
		By(fmt.Sprintf("skipping After node as %s is set", clusterdeployment.EnvVarNoCleanup))
	}

	return noCleanup != ""
}
