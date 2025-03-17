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
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
	"github.com/K0rdent/kcm/test/utils"
)

// Run e2e tests using the Ginkgo runner.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting kcm suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	err := config.Parse()
	Expect(err).NotTo(HaveOccurred())

	GinkgoT().Setenv(clusterdeployment.EnvVarNamespace, internalutils.DefaultSystemNamespace)

	cmd := exec.Command("make", "test-apply")
	_, err = utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())

	if config.UpgradeRequired() {
		By("installing stable templates for further upgrade testing")
		_, err = utils.Run(exec.Command("make", "stable-templates"))
		Expect(err).NotTo(HaveOccurred())
	}

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

	Eventually(func() error {
		err = clusterdeployment.ValidateClusterTemplates(context.Background(), kc)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "cluster template validation failed: %v\n", err)
			return err
		}
		return nil
	}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

	config.SetDefaults(context.Background(), kc.CrClient)

	_, _ = fmt.Fprintf(GinkgoWriter, "E2e testing configuration:\n%s\n", config.Show())
})

var _ = AfterSuite(func() {
	if cleanup() {
		By("collecting the support bundle from the management cluster")
		logs.SupportBundle("")

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
func templateBy(t templates.Type, description string) {
	GinkgoHelper()
	By(fmt.Sprintf("[%s] %s", t, description))
}

func cleanup() bool {
	return os.Getenv(clusterdeployment.EnvVarNoCleanup) == ""
}
