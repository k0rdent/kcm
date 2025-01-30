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
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	internalutils "github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/utils"
)

type Collector struct {
	Client        *kubeclient.KubeClient
	ProviderTypes []clusterdeployment.ProviderType
	ClusterNames  []string
}

func (c Collector) CollectAll() {
	if c.Client == nil {
		utils.WarnError(errors.New("failed to collect logs: client is nil"))
		return
	}
	c.CollectProvidersLogs()
	c.CollectClustersInfo()
}

// CollectProvidersLogs collects log output from each the KCM controller,
// CAPI controller and the provider controller(s) and stores them in the
// test/e2e directory as artifacts. If CollectLogs fails it produces a warning
// message to the GinkgoWriter, but does not fail the test.
func (c Collector) CollectProvidersLogs() {
	GinkgoHelper()
	if c.Client == nil {
		utils.WarnError(errors.New("failed to collect providers logs: client is nil"))
		return
	}

	filterLabels := []string{utils.KCMControllerLabel}

	if len(c.ProviderTypes) == 0 {
		filterLabels = clusterdeployment.FilterAllProviders()
	} else {
		for _, providerType := range c.ProviderTypes {
			filterLabels = append(filterLabels, clusterdeployment.GetProviderLabel(providerType))
		}
	}

	logFilePrefix := c.getKubeconfigHost()
	if logFilePrefix != "" {
		logFilePrefix += "-"
	}

	client := c.Client
	for _, label := range filterLabels {
		pods, _ := client.Client.CoreV1().Pods(client.Namespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: label,
		})

		for _, pod := range pods.Items {
			req := client.Client.CoreV1().Pods(client.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
				TailLines: internalutils.PtrTo(int64(1000)),
			})
			podLogs, err := req.Stream(context.Background())
			if err != nil {
				utils.WarnError(fmt.Errorf("failed to get log stream for pod %s: %w", pod.Name, err))
				continue
			}

			output, err := os.Create(fmt.Sprintf("./test/e2e/%s.log", logFilePrefix+pod.Name))
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
}

func (c Collector) CollectClustersInfo() {
	if c.Client == nil {
		utils.WarnError(errors.New("failed to collect clusters info: client is nil"))
		return
	}

	logFilePrefix := c.getKubeconfigHost()
	if logFilePrefix != "" {
		logFilePrefix += "-"
	}

	for _, clusterName := range c.ClusterNames {
		cmd := exec.Command("./bin/clusterctl",
			"describe", "cluster", clusterName, "--namespace", internalutils.DefaultSystemNamespace, "--show-conditions=all")
		output, err := utils.Run(cmd)
		if err != nil {
			utils.WarnError(fmt.Errorf("failed to get clusterctl log: %w", err))
			continue
		}
		err = os.WriteFile(filepath.Join("test/e2e", logFilePrefix+clusterName+"-"+"clusterctl.log"), output, 0o644)
		if err != nil {
			utils.WarnError(fmt.Errorf("failed to write clusterctl log: %w", err))
			continue
		}

		_, _ = fmt.Fprintf(GinkgoWriter, "getting ClusterDeployment %s\n", clusterName)
		cd, err := c.Client.GetClusterDeployment(context.Background(), clusterName)
		if err != nil {
			utils.WarnError(fmt.Errorf("failed to get ClusterDeployment %s: %w", clusterName, err))
			continue
		}
		output, err = yaml.Marshal(cd)
		if err != nil {
			utils.WarnError(fmt.Errorf("error marshalling ClusterDeployment %s to YAML: %w", clusterName, err))
			continue
		}
		err = os.WriteFile(filepath.Join("test/e2e", logFilePrefix+clusterName+".yaml.log"), output, 0o644)
		if err != nil {
			utils.WarnError(fmt.Errorf("failed to write ClusterDeployment %s: %w", clusterName, err))
		}
	}
}

func (c Collector) getKubeconfigHost() string {
	hostURL, err := url.Parse(c.Client.Config.Host)
	if err == nil {
		return strings.ReplaceAll(hostURL.Host, ":", "_")
	}
	utils.WarnError(fmt.Errorf("failed to parse host from kubeconfig: %w", err))
	return ""
}
