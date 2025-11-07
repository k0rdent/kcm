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

// Package collector holds different implementation of telemetry data collectors.
package collector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/segmentio/analytics-go/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubevirtv1 "kubevirt.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

type SegmentIO struct {
	segmentCl    analytics.Client
	parentClient client.Client
	childScheme  *runtime.Scheme
	childFactory func([]byte, *runtime.Scheme) (client.Client, error) // for test mocks
	concurrency  int
}

// NewSegmentIO creates a new instance of the [SegmentIO].
func NewSegmentIO(segmentClient analytics.Client, parentClient client.Client, concurrency int) (*SegmentIO, error) {
	childScheme, err := getChildScheme()
	if err != nil {
		return nil, fmt.Errorf("failed to create child client scheme: %w", err)
	}

	return &SegmentIO{
		segmentCl:    segmentClient,
		parentClient: parentClient,
		childScheme:  childScheme,
		concurrency:  concurrency,
		childFactory: kubeutil.DefaultClientFactory,
	}, nil
}

func (t *SegmentIO) Collect(ctx context.Context) error {
	l := ctrl.LoggerFrom(ctx).WithName("online-collector")
	ctx = ctrl.LoggerInto(ctx, l)

	isMgmtCluster, err := isMgmtCluster(ctx, t.parentClient)
	if err != nil {
		return fmt.Errorf("failed to determine if management cluster: %w", err)
	}

	anonID, err := t.fetchAnonID(ctx, isMgmtCluster)
	if err != nil {
		return fmt.Errorf("failed to get anonymous ID: %w", err)
	}

	parentData := newParentDataFetcher()
	dataScope, err := parentData.getScopeExplicit(isMgmtCluster, scopeOnline)
	if err != nil {
		return fmt.Errorf("failed to get current scope: %w", err)
	}

	if err := parentData.fetch(ctx, t.parentClient, dataScope); err != nil {
		return fmt.Errorf("failed to fetch data from parent cluster: %w", err)
	}

	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, t.concurrency)
	)

	start := time.Time{}
	if l.V(1).Enabled() {
		start = time.Now()
	}

	for _, cluster := range parentData.clusters {
		if cluster == nil { // sanity check
			continue
		}

		wg.Add(1)
		sem <- struct{}{}

		go func() {
			defer func() {
				wg.Done()
				<-sem
			}()

			ll := l.WithValues("scope", dataScope.String(), "cluster", cluster.GetNamespace()+"/"+cluster.GetName())

			ll.V(1).Info("starting collecting cluster")

			clusterKey := client.ObjectKeyFromObject(cluster)

			secretRef := kubeutil.GetKubeconfigSecretKey(clusterKey)
			childCl, err := kubeutil.GetChildClient(ctx, t.parentClient, secretRef, "value", t.childScheme, t.childFactory)
			if err != nil {
				ll.Error(err, "failed to get child kubeconfig")
				return
			}

			props, err := t.collectChildProperties(ctx, childCl)
			if err != nil {
				ll.Error(err, "failed to collect properties")
				if len(props) == 0 {
					props = make(map[string]any) // avoid runtime error
				}
			}

			// this map is shared within a goroutine, so no sync is required
			props["cluster"] = clusterKey.String()
			props["clusterID"] = getK0sClusterID(parentData.partialCapiClusters, clusterKey)

			props["userServiceCount"] = countChildUserServices(parentData.profilesList, parentData.partialCapiClusters)

			if dataScope.isMgmt() { // sanity
				if cld, ok := cluster.(*kcmv1.ClusterDeployment); ok {
					props["clusterDeploymentID"] = string(cld.UID)
					props["template"] = cld.Spec.Template
					props["syncMode"] = cld.Spec.ServiceSpec.SyncMode

					// NOTE: it's okay to read concurrently here since NO writes occur; checked with -race flag
					if template, ok := parentData.mgmtTemplateName2Template[client.ObjectKey{Namespace: cld.Namespace, Name: cld.Spec.Template}]; ok {
						props["providers"] = template.Status.Providers
						props["templateHelmChartVersion"] = template.Status.ChartVersion
					}
				}
			}

			ll.V(1).Info("collected child properties", "props", props)

			if err := t.segmentCl.Enqueue(analytics.Track{
				AnonymousId: anonID,
				Event:       "ChildDataHearbeat",
				Properties:  props,
			}); err != nil {
				ll.Error(err, "failed to enqueue event to the segmentio")
			}
		}()
	}

	wg.Wait()

	if l.V(1).Enabled() {
		l.V(1).Info("finished collecting telemetry", "finished_in", time.Since(start))
	}

	return nil
}

func (*SegmentIO) collectChildProperties(ctx context.Context, childCl client.Client) (map[string]any, error) {
	acc := newOnlineAccumulator()
	if err := streamPaginatedNodes(ctx, childCl, 50, func(nodes []*corev1.Node) {
		for _, node := range nodes {
			acc.accumulateNode(node)
		}
	}); err != nil {
		return nil, fmt.Errorf("failed to accumulate nodes info during listing: %w", err)
	}

	props := make(map[string]any)
	// populate nodes data
	props["node.count"] = acc.nodesCount
	props["node.cpu.total"] = acc.nodesTotalCPU
	props["node.memory.bytes"] = acc.nodesTotalMemory
	props["node.gpu.total"] = acc.nodesTotalGPU
	props["node.info"] = acc.nodeInfos

	// prepare data to be filled out from pods accumulation
	nodeName2Idx := make(map[string]int, len(acc.nodeInfos))
	for i, info := range acc.nodeInfos {
		nodeName2Idx[info["name"]] = i
	}
	acc.nodeName2InfoIdx = nodeName2Idx
	if err := streamPaginatedPods(ctx, childCl, 100, func(pods []*corev1.Pod) {
		for _, pod := range pods {
			acc.accumulatePodGpu(pod)
		}
	}); err != nil {
		return props, fmt.Errorf("failed to accumulate pods info during listing: %w", err)
	}

	// populate pods data
	props["pods.with_gpu_requests"] = acc.podsWithGPUReqs

	dsGVK := appsv1.SchemeGroupVersion.WithKind("DaemonSet")
	partialDaemonSets, err := listAsPartial(ctx, childCl, dsGVK)
	if err != nil {
		return props, fmt.Errorf("failed to list all daemon sets: %w", err)
	}

	props["gpu.operator_installed.nvidia"], props["gpu.operator_installed.amd"] = getGpuOperatorPresence(partialDaemonSets)

	partialVMIs, err := listAsPartial(ctx, childCl, kubevirtv1.VirtualMachineInstanceGroupVersionKind)
	if err != nil {
		if !meta.IsNoMatchError(err) {
			return props, fmt.Errorf("failed to list kubevirt instances: %w", err)
		}
	}

	props["kubevirt.vmis"] = len(partialVMIs)

	return props, nil
}

func getKubeVersionAndFlavor(info corev1.NodeSystemInfo) (version, flavor string) {
	version, flavor, _ = strings.Cut(info.KubeletVersion, "+")
	return version, flavor
}

// fetchMgmtClusterAnonID retrieves anonymous ID for management clusters.
func (t *SegmentIO) fetchMgmtClusterAnonID(ctx context.Context) (string, error) {
	mgmt := new(kcmv1.Management)
	if err := t.parentClient.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, mgmt); err != nil {
		return "", fmt.Errorf("failed to get Management: %w", err)
	}

	return string(mgmt.UID), nil
}

// fetchRegionalClusterAnonID retrieves anonymous ID for regional clusters
// using kubernetes default Service.
func (t *SegmentIO) fetchRegionalClusterAnonID(ctx context.Context) (string, error) {
	const kubernetesServiceName = "kubernetes"

	kubeSvc := new(metav1.PartialObjectMetadata)
	kubeSvc.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
	if err := t.parentClient.Get(
		ctx,
		client.ObjectKey{Namespace: metav1.NamespaceDefault, Name: kubernetesServiceName},
		kubeSvc,
	); err != nil {
		return "", fmt.Errorf("failed to get Service: %w", err)
	}

	return string(kubeSvc.UID), nil
}

// fetchAnonID fetches the appropriate anon ID
func (t *SegmentIO) fetchAnonID(ctx context.Context, isMgmtCluster bool) (string, error) { //nolint: revive // cannot be avoided
	if isMgmtCluster {
		return t.fetchMgmtClusterAnonID(ctx)
	}

	return t.fetchRegionalClusterAnonID(ctx)
}

func (t *SegmentIO) Close(ctx context.Context) error {
	ctrl.LoggerFrom(ctx).Info("closing online collector")
	return t.segmentCl.Close()
}
