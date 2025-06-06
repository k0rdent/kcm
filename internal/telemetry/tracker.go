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

package telemetry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

type Tracker struct {
	client.Client

	SystemNamespace string
}

const interval = 24 * time.Hour

// Map to serve as blacklist filter for system namespaces
var systemNamespaces = map[string]bool{
	"kube-system":     true,
	"kube-public":     true,
	"kube-node-lease": true,
}

func (t *Tracker) Start(ctx context.Context) error {
	timer := time.NewTimer(0)
	for {
		select {
		case <-timer.C:
			t.Tick(ctx)
			timer.Reset(interval)
		case <-ctx.Done():
			return nil
		}
	}
}

func (t *Tracker) Tick(ctx context.Context) {
	l := log.FromContext(ctx).WithName("telemetry tracker")

	// Cluster deployment heartbeat; One event per cluster deployment
	logger := l.WithValues("event", clusterDeploymentHeartbeatEvent)
	err := t.trackClusterDeploymentHeartbeat(ctx)
	if err != nil {
		logger.Error(err, "failed to track cluster deployment heartbeat")
	} else {
		logger.Info("successfully tracked cluster deployment heartbeat")
	}

	// Management cluster heartbeat
	logger = l.WithValues("event", managementClusterHeartbeatEvent)
	err = t.trackManagementClusterHeartbeat(ctx)
	if err != nil {
		logger.Error(err, "failed to track management cluster heartbeat")
	} else {
		logger.Info("successfully tracked management cluster heartbeat")
	}
}

func (t *Tracker) trackClusterDeploymentHeartbeat(ctx context.Context) error {
	// Get the management cluster
	mgmt := &kcmv1.Management{}
	if err := t.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, mgmt); err != nil {
		return err
	}

	// Get all cluster templates
	templatesList := &kcmv1.ClusterTemplateList{}
	if err := t.List(ctx, templatesList, client.InNamespace(t.SystemNamespace)); err != nil {
		return err
	}

	// Create a map to of templates to decode the ClusterDeployment.Spec.Template value
	templates := make(map[string]kcmv1.ClusterTemplate)
	for _, template := range templatesList.Items {
		templates[template.Name] = template
	}

	var errs error
	clusterDeployments := &kcmv1.ClusterDeploymentList{}
	if err := t.List(ctx, clusterDeployments); err != nil {
		return err
	}

	// Iterate over all cluster deployments and emit a heartbeat for each
	for _, clusterDeployment := range clusterDeployments.Items {
		template := templates[clusterDeployment.Spec.Template]
		// TODO: get k0s cluster ID once it's exposed in k0smotron API
		clusterID := ""

		err := TrackClusterDeploymentHeartbeat(
			string(mgmt.UID),
			string(clusterDeployment.UID),
			clusterID,
			clusterDeployment.Spec.Template,
			template.Status.ChartVersion,
			template.Status.Providers,
		)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to track the heartbeat of the clusterDeployment %s/%s", clusterDeployment.Namespace, clusterDeployment.Name))

			continue
		}
	}
	return errs
}

// Helper function to recognize GPU resources
func isGPUResource(resourceName corev1.ResourceName) bool {
	return strings.Contains(string(resourceName), "gpu") ||
		strings.Contains(string(resourceName), "nvidia.com") ||
		strings.Contains(string(resourceName), "amd.com")
}

// Helper function to count non-system pods per node and return a map indexed by node name
func countNonSystemPodsOnNodes(ctx context.Context, client client.Client) (map[string]map[string]int, error) {

	// Get all pods in the cluster -- these will be filtered to just those assigned to the node
	pods := &corev1.PodList{}
	if err := client.List(ctx, pods); err != nil {
		return nil, err
	}

	nodePodMetrics := make(map[string]map[string]int)

	// Count pods
	for _, pod := range pods.Items {
		// Skip system namespace pods using rubric
		if systemNamespaces[pod.Namespace] {
			continue
		}

		// Skip pods not assigned to a node
		if pod.Spec.NodeName == "" {
			continue
		}

		// Initialize node metrics if not exists
		if nodePodMetrics[pod.Spec.NodeName] == nil {
			nodePodMetrics[pod.Spec.NodeName] = map[string]int{
				"nodePodCount":           0,
				"nodePodRunningCount":    0,
				"nodePodGpuRequestCount": 0,
				"nodePodGpuAllocCount":   0,
				"containerCount":         0,
			}
		}

		// Count total non-system pods and containers
		nodePodMetrics[pod.Spec.NodeName]["nodePodCount"]++
		nodePodMetrics[pod.Spec.NodeName]["containerCount"] += len(pod.Spec.Containers) + len(pod.Spec.InitContainers)

		// Running pods
		if pod.Status.Phase == corev1.PodRunning {
			nodePodMetrics[pod.Spec.NodeName]["nodePodRunningCount"]++
		}

		// Pods requesting GPU -- need to check resources associated with Pod containeres
		podRequestGPU := false
		podAllocGPU := false
		for _, container := range pod.Spec.Containers {
			if container.Resources.Requests != nil {
				for resourceName := range container.Resources.Requests {
					if isGPUResource(resourceName) {
						podRequestGPU = true
						break
					}
				}
			}
			if container.Resources.Limits != nil {
				for resourceName := range container.Resources.Limits {
					if isGPUResource(resourceName) {
						podAllocGPU = true
						break
					}
				}
			}
		}

		if podRequestGPU {
			nodePodMetrics[pod.Spec.NodeName]["nodePodGpuRequestCount"]++
		}
		if podAllocGPU {
			nodePodMetrics[pod.Spec.NodeName]["nodePodGpuAllocCount"]++
		}
	}

	return nodePodMetrics, nil
}

// Helper function to build node-level metrics
// Return a map indexed by node name, plus cluster-level aggregates
func buildNodeLevelMetrics(ctx context.Context, client client.Client) ([]map[string]any, map[string]any, error) {
	// Get all nodes in the cluster
	nodes := &corev1.NodeList{}
	if err := client.List(ctx, nodes); err != nil {
		return nil, nil, err
	}

	clusterMetrics := make(map[string]any)
	nodeMetrics := make([]map[string]any, 0)
	controllerNodes := 0
	workerNodes := 0
	totalPods := 0
	totalContainers := 0
	totalPodsRequestGPU := 0
	totalPodsAllocGPU := 0

	// Count non-system pods per node
	nodePodCounts, err := countNonSystemPodsOnNodes(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	// Iterate through nodes and build a node-level metrics map
	for i, node := range nodes.Items {
		if i == 0 {
			clusterMetrics = map[string]any{
				"kubernetesVersion": node.Status.NodeInfo.KubeletVersion,
				"kubeProxyVersion":  node.Status.NodeInfo.KubeProxyVersion,
				"containerRuntime":  node.Status.NodeInfo.ContainerRuntimeVersion,
			}
		}

		// Node CPU cores & Memory
		cpuCores := node.Status.Capacity.Cpu().Value()
		memoryBytes := node.Status.Capacity.Memory().Value()
		memoryGB := memoryBytes / (1024 * 1024 * 1024) // Convert to GB

		// Node Storage (ephemeral-storage)
		diskBytes := int64(0)
		if storage, exists := node.Status.Capacity["ephemeral-storage"]; exists {
			diskBytes = storage.Value()
		}
		diskGB := diskBytes / (1024 * 1024 * 1024) // Convert to GB

		// NVIDIA: Node Feature Discovery (NFD) and GPU Feature Discovery (GFD)
		// See https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/gpu-sharing.html for more details on GPU sharing
		gpuNvidiaCapacity := node.Status.Capacity[corev1.ResourceName("nvidia.com/gpu")]
		gpuNvidiaCapacityShared := node.Status.Capacity[corev1.ResourceName("nvidia.com/gpu.shared")]
		gpuNvidiaAlloc := node.Status.Allocatable[corev1.ResourceName("nvidia.com/gpu")]
		gpuNvidiaAllocShared := node.Status.Allocatable[corev1.ResourceName("nvidia.com/gpu.shared")]

		// Labels provide actual values for GPU counts, types, memory, and sharing configuration
		gpuNvidiaCount := node.Labels["nvidia.com/gpu.count"]
		gpuNvidiaProduct := node.Labels["nvidia.com/gpu.product"]
		gpuNvidiaMemory := node.Labels["nvidia.com/gpu.memory"]
		gpuNvidiaSharing := node.Labels["nvidia.com/gpu.sharing"]

		// Node GPU cores and vendor
		gpuCount := int64(0)
		gpuType := "none"

		// Scan resources for strings suggesting a GPU
		for resourceName, quantity := range node.Status.Capacity {
			if isGPUResource(resourceName) {
				gpuCount += quantity.Value()

				// Try to determine GPU type from resource name -- presuming just one vendor per node
				if strings.Contains(string(resourceName), "nvidia") {
					gpuType = "nvidia"
				} else if strings.Contains(string(resourceName), "amd") {
					gpuType = "amd"
				} else {
					gpuType = "unspecified"
				}
			}
		}

		// Get Node OS info
		os := node.Status.NodeInfo.OSImage
		osType := node.Status.NodeInfo.OperatingSystem
		arch := node.Status.NodeInfo.Architecture

		// Node Role
		isControlPlane := false
		isWorker := false
		if _, exists := node.Labels["node-role.kubernetes.io/control-plane"]; exists {
			isControlPlane = true
		}
		if _, exists := node.Labels["node-role.kubernetes.io/master"]; exists {
			isControlPlane = true
		}
		if _, exists := node.Labels["node-role.kubernetes.io/worker"]; exists {
			isWorker = true
		}

		if isControlPlane {
			controllerNodes++
		}
		if isWorker {
			workerNodes++
		}

		// Update Cluster-level aggregates with this node's metrics
		totalPods += nodePodCounts[node.Name]["nodePodCount"]
		totalContainers += nodePodCounts[node.Name]["containerCount"]
		totalPodsRequestGPU += nodePodCounts[node.Name]["nodePodGpuRequestCount"]
		totalPodsAllocGPU += nodePodCounts[node.Name]["nodePodGpuAllocCount"]

		// Save Node-level metrics for this node
		nodeMetric := map[string]any{
			"arch":                    arch,
			"cpuCores":                cpuCores,
			"diskGB":                  diskGB,
			"gpuCount":                gpuCount,
			"gpuType":                 gpuType,
			"gpuNvidiaCapacity":       gpuNvidiaCapacity.Value(),
			"gpuNvidiaCapacityShared": gpuNvidiaCapacityShared.Value(),
			"gpuNvidiaAlloc":          gpuNvidiaAlloc.Value(),
			"gpuNvidiaAllocShared":    gpuNvidiaAllocShared.Value(),
			"gpuNvidiaCount":          gpuNvidiaCount,
			"gpuNvidiaProduct":        gpuNvidiaProduct,
			"gpuNvidiaMemory":         gpuNvidiaMemory,
			"gpuNvidiaSharing":        gpuNvidiaSharing,
			"isControlPlane":          isControlPlane,
			"isWorker":                isWorker,
			"memoryGB":                memoryGB,
			"containers":              nodePodCounts[node.Name]["containerCount"],
			"os":                      os,
			"osType":                  osType,
			"pods":                    nodePodCounts[node.Name]["nodePodCount"],
			"podsRunning":             nodePodCounts[node.Name]["nodePodRunningCount"],
			"podsRequestGpu":          nodePodCounts[node.Name]["nodePodGpuRequestCount"],
			"podsAllocGpu":            nodePodCounts[node.Name]["nodePodGpuAllocCount"],
		}

		// Add node-level metrics to the array
		nodeMetrics = append(nodeMetrics, nodeMetric)
	}

	// Save Cluster-level aggregates
	clusterMetrics["controllers"] = controllerNodes
	clusterMetrics["workers"] = workerNodes
	clusterMetrics["totalPods"] = totalPods
	clusterMetrics["containers"] = totalContainers
	clusterMetrics["podsRequestGPU"] = totalPodsRequestGPU
	clusterMetrics["podsAllocGPU"] = totalPodsAllocGPU

	return nodeMetrics, clusterMetrics, nil
}

func (t *Tracker) trackManagementClusterHeartbeat(ctx context.Context) error {
	// Get the management cluster
	mgmt := &kcmv1.Management{}
	if err := t.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, mgmt); err != nil {
		return err
	}

	// Get PersistentVolume count
	pvs := &corev1.PersistentVolumeList{}
	pvCount := 0
	if err := t.List(ctx, pvs); err == nil {
		pvCount = len(pvs.Items)
	}

	// Get ClusterDeployment count
	clusterDeployments := &kcmv1.ClusterDeploymentList{}
	clusterDeploymentCount := 0
	if err := t.List(ctx, clusterDeployments); err == nil {
		clusterDeploymentCount = len(clusterDeployments.Items)
	}

	// Get Credentials count
	credentials := &kcmv1.CredentialList{}
	credentialCount := 0
	if err := t.List(ctx, credentials); err == nil {
		credentialCount = len(credentials.Items)
	}

	// Get ProviderTemplate count
	providerTemplates := &kcmv1.ProviderTemplateList{}
	providerTemplateCount := 0
	if err := t.List(ctx, providerTemplates); err == nil {
		providerTemplateCount = len(providerTemplates.Items)
	}

	// Get ClusterTemplate count
	clusterTemplates := &kcmv1.ClusterTemplateList{}
	clusterTemplateCount := 0
	if err := t.List(ctx, clusterTemplates); err == nil {
		clusterTemplateCount = len(clusterTemplates.Items)
	}

	// Get ServiceTemplate count
	serviceTemplates := &kcmv1.ServiceTemplateList{}
	serviceTemplateCount := 0
	if err := t.List(ctx, serviceTemplates); err == nil {
		serviceTemplateCount = len(serviceTemplates.Items)
	}

	// Get Non-System Service counts
	services := &corev1.ServiceList{}
	serviceCount := 0
	if err := t.List(ctx, services); err == nil {

		for _, service := range services.Items {

			// Skip system services
			if systemNamespaces[service.Namespace] {
				continue
			}

			serviceCount++
		}
	}

	// Get node-level metrics
	nodeMetrics, clusterMetrics, err := buildNodeLevelMetrics(ctx, t.Client)
	if err != nil {
		return err
	}

	// distribution, dist_version := DecodeDistribution(kubernetesVersion)

	k8s_props := map[string]any{
		"version":          clusterMetrics["kubernetesVersion"],
		"kubeProxyVersion": clusterMetrics["kubeProxyVersion"],
		"containerRuntime": clusterMetrics["containerRuntime"],
		"dist":             DecodeDistribution(clusterMetrics["kubernetesVersion"].(string)),
		// "distVersion":         dist_version,
		"controllers":    clusterMetrics["controllers"],
		"workers":        clusterMetrics["workers"],
		"pods":           clusterMetrics["totalPods"],
		"containers":     clusterMetrics["containers"],
		"pvs":            pvCount,
		"podsRequestGPU": clusterMetrics["podsRequestGPU"],
		"podsAllocGPU":   clusterMetrics["podsAllocGPU"],
	}

	kcm_props := map[string]any{
		"credentials":        credentialCount,
		"clusterDeployments": clusterDeploymentCount,
		"providerTemplates":  providerTemplateCount,
		"clusterTemplates":   clusterTemplateCount,
		"serviceTemplates":   serviceTemplateCount,
		"services":           serviceCount,
	}

	// Combined telemetry properties
	telemetryProps := map[string]any{
		"clusterID": "kube-system:" + string(mgmt.UID),
		"k8s":       k8s_props,
		"kcm":       kcm_props,
		"nodes":     nodeMetrics,
	}

	// Add clusterID, defined as the UUID of the kube-system namespace with a prefix
	kubeSystemNamespace := &corev1.Namespace{}
	if err := t.Get(ctx, client.ObjectKey{Name: "kube-system"}, kubeSystemNamespace); err == nil {
		telemetryProps["clusterID"] = "kube-system:" + string(kubeSystemNamespace.UID)
	}

	err = TrackManagementClusterHeartbeat(
		string(mgmt.UID),
		telemetryProps,
	)
	if err != nil {
		return fmt.Errorf("failed to track management cluster heartbeat: %w", err)
	}
	return nil
}
