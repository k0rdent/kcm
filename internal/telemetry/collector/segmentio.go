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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubevirtv1 "kubevirt.io/api/core/v1" // TODO: switch to v1beta2 everywhere
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/utils/kube"
)

type SegmentIO struct {
	segmentCl    analytics.Client
	mgmtClient   client.Client
	childScheme  *runtime.Scheme
	childFactory func([]byte, *runtime.Scheme) (client.Client, error) // for test mocks
	concurrency  int
}

func NewSegmentIO(segmentClient analytics.Client, mgmtClient client.Client, concurrency int) (*SegmentIO, error) {
	childScheme, err := getChildScheme()
	if err != nil {
		return nil, fmt.Errorf("failed to create child client scheme: %w", err)
	}

	return &SegmentIO{
		segmentCl:    segmentClient,
		mgmtClient:   mgmtClient,
		childScheme:  childScheme,
		concurrency:  concurrency,
		childFactory: kube.DefaultClientFactory,
	}, nil
}

func (t *SegmentIO) Collect(ctx context.Context) error {
	l := ctrl.LoggerFrom(ctx).WithName("online-collector")
	ctx = ctrl.LoggerInto(ctx, l)

	mgmtData := newMgmtDataFetcher(true /* mgmt */, true /* cluster template*/)
	if err := mgmtData.fetch(ctx, t.mgmtClient); err != nil {
		return fmt.Errorf("failed to fetch data from mgmt cluster: %w", err)
	}

	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, t.concurrency)

		mgmtID = string(mgmtData.mgmtUID)
	)

	start := time.Time{}
	if l.V(1).Enabled() {
		start = time.Now()
	}

	for _, cld := range mgmtData.clusterDeployments.Items {
		wg.Add(1)
		sem <- struct{}{}

		go func() {
			defer func() {
				wg.Done()
				<-sem
			}()
			ll := l.WithValues("cld", cld.Namespace+"/"+cld.Name)

			ll.V(1).Info("starting collecting cluster")

			template, ok := mgmtData.tplName2Template[client.ObjectKey{Namespace: cld.Namespace, Name: cld.Spec.Template}] // NOTE: it's okay to read concurrently here since NO writes occur; checked with -race flag
			if !ok {
				template = &kcmv1.ClusterTemplate{}
			}

			cldKey := client.ObjectKeyFromObject(&cld)

			secretRef := kube.GetKubeconfigSecretKey(cldKey)
			childCl, err := kube.GetChildClient(ctx, t.mgmtClient, secretRef, "value", t.childScheme, t.childFactory)
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
			props["cluster"] = cldKey.String()
			props["clusterDeploymentID"] = string(cld.UID)
			props["clusterID"] = getK0sClusterID(mgmtData.partialCapiClusters, cldKey)
			props["providers"] = template.Status.Providers
			props["template"] = cld.Spec.Template
			props["templateHelmChartVersion"] = template.Status.ChartVersion
			props["syncMode"] = cld.Spec.ServiceSpec.SyncMode

			props["userServiceCount"] = countUserServices(&cld, mgmtData.mcsList, mgmtData.partialClustersToMatch)

			ll.V(1).Info("collected child properties", "props", props)

			if err := t.segmentCl.Enqueue(analytics.Track{
				AnonymousId: mgmtID,
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

	dsGVK := schema.GroupVersionKind{
		Group:   appsv1.SchemeGroupVersion.Group,
		Version: appsv1.SchemeGroupVersion.Version,
		Kind:    "DaemonSet",
	}
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

func (t *SegmentIO) Close(ctx context.Context) error {
	ctrl.LoggerFrom(ctx).Info("closing online collector")
	return t.segmentCl.Close()
}
