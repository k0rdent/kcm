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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/templates"
	"github.com/K0rdent/kcm/test/utils"
)

var (
	clusterTemplates []string

	kc *kubeclient.KubeClient
)

// Run e2e tests using the Ginkgo runner.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting kcm suite\n")

	err := config.Parse()
	Expect(err).NotTo(HaveOccurred())

	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	GinkgoT().Setenv(clusterdeployment.EnvVarNamespace, internalutils.DefaultSystemNamespace)
	GinkgoT().Setenv("CI_TELEMETRY", "true")

	cmd := exec.Command("make", "test-apply")
	_, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())

	if config.UpgradeRequired() {
		By("installing stable templates for further upgrade testing")
		_, err = utils.Run(exec.Command("make", "stable-templates"))
		Expect(err).NotTo(HaveOccurred())
	}

	kc = kubeclient.NewFromLocal(internalutils.DefaultSystemNamespace)

	By("validating that all K0rdent management components are ready")
	Eventually(func() error {
		err = verifyManagementReadiness(kc)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "%v\n", err)
			return err
		}
		return nil
	}).WithTimeout(30 * time.Minute).WithPolling(20 * time.Second).Should(Succeed())

	Eventually(func() error {
		err = clusterdeployment.ValidateClusterTemplates(context.Background(), kc)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "cluster template validation failed: %v\n", err)
			return err
		}
		return nil
	}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

	clusterTemplates, err = templates.GetSortedClusterTemplates(context.Background(), kc.CrClient, internalutils.DefaultSystemNamespace)
	Expect(err).NotTo(HaveOccurred())

	_, _ = fmt.Fprintf(GinkgoWriter, "Found ClusterTemplates:\n%v\n", clusterTemplates)
})

var _ = AfterSuite(func() {
	if cleanup() {
		By("collecting the support bundle from the management cluster")
		logs.SupportBundle(kc, "")

		By("removing the controller-manager")
		cmd := exec.Command("make", "dev-destroy")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
	}
})

func verifyManagementReadiness(kc *kubeclient.KubeClient) error {
	mgmt := &kcmv1.Management{}
	if err := kc.CrClient.Get(context.Background(), crclient.ObjectKey{Name: kcmv1.ManagementName}, mgmt); err != nil {
		return err
	}
	idx := slices.IndexFunc(mgmt.Status.Conditions, func(c metav1.Condition) bool { return c.Type == kcmv1.ReadyCondition })
	if idx < 0 {
		return errors.New("ready condition was not reported")
	}
	readyCondition := mgmt.Status.Conditions[idx]
	if readyCondition.Status == metav1.ConditionTrue {
		return nil
	}
	return fmt.Errorf("%s: %s", readyCondition.Reason, readyCondition.Message)
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
