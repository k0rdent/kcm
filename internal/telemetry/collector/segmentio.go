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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/segmentio/analytics-go/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubevirtv1 "kubevirt.io/api/core/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1" // TODO: switch to v1beta2 everywhere
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/utils/pointer"
)

type SegmentIO struct {
	segmentCl       analytics.Client
	crCl            client.Client
	childScheme     *runtime.Scheme
	expBackoff      *backoff.ExponentialBackOff
	systemNamespace string
	concurrency     int
}

type (
	nodeDataAccumulator struct {
		nodeInfos     []map[string]string
		totalCPU      uint64
		totalMemory   uint64
		totalGPUNodes uint
		count         uint
	}

	podDataAccumulator struct {
		nodeInfos       *[]map[string]string
		nodeName2Idx    map[string]int
		podsWithGPUReqs uint
	}
)

func NewSegmentIO(systemNamespace string, segmentClient analytics.Client, crClient client.Client, concurrency int) (*SegmentIO, error) {
	s := runtime.NewScheme()

	for _, f := range []func(*runtime.Scheme) error{corev1.AddToScheme, metav1.AddMetaToScheme} {
		if err := f(s); err != nil {
			return nil, fmt.Errorf("failed to add to scheme: %w", err)
		}
	}

	return &SegmentIO{
		expBackoff:      backoff.NewExponentialBackOff(backoff.WithInitialInterval(500*time.Millisecond), backoff.WithMaxElapsedTime(10*time.Second)),
		segmentCl:       segmentClient,
		crCl:            crClient,
		systemNamespace: systemNamespace,
		childScheme:     s,
		concurrency:     concurrency,
	}, nil
}

func (t *SegmentIO) Collect(ctx context.Context) error {
	l := ctrl.LoggerFrom(ctx)

	mgmt := new(kcmv1.Management)
	if err := t.retry(ctx, func() error {
		return t.crCl.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, mgmt)
	}); err != nil {
		return fmt.Errorf("failed to get Management: %w", err)
	}

	mgmtID := string(mgmt.UID)

	templatesList := new(kcmv1.ClusterTemplateList)
	if err := t.retry(ctx, func() error {
		return t.crCl.List(ctx, templatesList, client.InNamespace(t.systemNamespace))
	}); err != nil {
		return fmt.Errorf("failed to list ClusterTemplates: %w", err)
	}

	tplName2Template := make(map[string]kcmv1.ClusterTemplate, len(templatesList.Items))
	for _, template := range templatesList.Items {
		tplName2Template[template.Name] = template
	}

	clusterDeployments := new(kcmv1.ClusterDeploymentList)
	if err := t.retry(ctx, func() error {
		return t.crCl.List(ctx, clusterDeployments)
	}); err != nil {
		return fmt.Errorf("failed to list ClusterDeployments: %w", err)
	}

	var mcsList kcmv1.MultiClusterServiceList
	if err := t.retry(ctx, func() error {
		return t.crCl.List(ctx, &mcsList)
	}); err != nil {
		return fmt.Errorf("failed to list MultiClusterServices: %w", err)
	}

	clustersGVK := schema.GroupVersionKind{
		Group:   clusterapiv1.GroupVersion.Group,
		Version: clusterapiv1.GroupVersion.Version,
		Kind:    clusterapiv1.ClusterKind,
	}
	partialClusters, err := listPartial(ctx, t.crCl, clustersGVK, 100)
	if err != nil {
		return fmt.Errorf("failed to list all %s: %w", clustersGVK.String(), err)
	}

	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, t.concurrency)
	)

	for _, cld := range clusterDeployments.Items {
		wg.Add(1)
		sem <- struct{}{}

		go func() {
			defer func() {
				wg.Done()
				<-sem
			}()
			ll := l.WithValues("cld", cld.Namespace+"/"+cld.Name)

			template, ok := tplName2Template[cld.Spec.Template] // NOTE: it's okay to read concurrently here since NO writes occure
			if !ok {
				template = kcmv1.ClusterTemplate{}
			}

			childCl, err := getChildClient(ctx, t.crCl, cld.Name, t.systemNamespace, t.childScheme)
			if err != nil {
				ll.Error(err, "failed to get child kubeconfig")
				return
			}

			// this map is shared within a goroutine, so no sync is required
			props := map[string]any{
				"cluster":                  cld.Namespace + "/" + cld.Name,
				"clusterDeploymentID":      string(cld.UID),
				"clusterID":                "", // TODO: get k0s cluster ID once it's exposed in k0smotron API
				"providers":                template.Status.Providers,
				"template":                 cld.Spec.Template,
				"templateHelmChartVersion": template.Status.ChartVersion,
				"syncMode":                 cld.Spec.ServiceSpec.SyncMode,
			}

			if err := t.collectChildProperties(ctx, childCl, props); err != nil {
				ll.Error(err, "failed to collect properties")
			}

			props["userServiceCount"] = countUserServices(&cld, &mcsList, partialClusters)

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

	return nil
}

func (t *SegmentIO) retry(ctx context.Context, op func() error) error {
	return backoff.Retry(func() error {
		select {
		case <-ctx.Done():
			return backoff.Permanent(ctx.Err())
		default:
			return op()
		}
	}, backoff.WithContext(t.expBackoff, ctx))
}

func (*SegmentIO) collectChildProperties(ctx context.Context, childCl client.Client, props map[string]any) error {
	if props == nil {
		props = make(map[string]any)
	}

	nodeAcc := &nodeDataAccumulator{}
	if err := streamPaginatedNodes(ctx, childCl, 100, func(nodes []*corev1.Node) {
		for _, node := range nodes {
			accumulateNode(nodeAcc, node)
		}
	}); err != nil {
		return fmt.Errorf("failed to accumulate nodes info during listing: %w", err)
	}

	// populate nodes data
	props["node.count"] = nodeAcc.count
	props["node.cpu.total"] = nodeAcc.totalCPU
	props["node.memory.bytes"] = nodeAcc.totalMemory
	props["node.gpu.total"] = nodeAcc.totalGPUNodes
	props["node.info"] = nodeAcc.nodeInfos

	// prepare data to be filled out from pods accumulation
	nodeName2Idx := make(map[string]int, len(nodeAcc.nodeInfos))
	for i, info := range nodeAcc.nodeInfos {
		nodeName2Idx[info["name"]] = i
	}
	podAcc := &podDataAccumulator{nodeInfos: &nodeAcc.nodeInfos, nodeName2Idx: nodeName2Idx}
	if err := streamPaginatedPods(ctx, childCl, 200, func(pods []*corev1.Pod) {
		for _, pod := range pods {
			accumulatePodGpu(podAcc, pod)
		}
	}); err != nil {
		return fmt.Errorf("failed to accumulate pods info during listing: %w", err)
	}

	// populate pods data
	props["pods.with_gpu_requests"] = podAcc.podsWithGPUReqs

	dsGVK := schema.GroupVersionKind{
		Group:   appsv1.SchemeGroupVersion.Group,
		Version: appsv1.SchemeGroupVersion.Version,
		Kind:    "DaemonSet",
	}
	partialDaemonSets, err := listPartial(ctx, childCl, dsGVK, 100)
	if err != nil {
		return fmt.Errorf("failed to list all %s: %w", dsGVK.String(), err)
	}

	props["gpu.operator_installed.nvidia"], props["gpu.operator_installed.amd"] = getGpuOperatorPresence(partialDaemonSets)

	partialVMIs, err := listPartial(ctx, childCl, kubevirtv1.VirtualMachineInstanceGroupVersionKind, 100)
	if err != nil {
		return fmt.Errorf("failed to list kubevirt instances: %w", err)
	}

	props["kubevirt.vmis"] = len(partialVMIs)

	return nil
}

func countUserServices(cld *kcmv1.ClusterDeployment, mcsList *kcmv1.MultiClusterServiceList, capiClusters []metav1.PartialObjectMetadata) int {
	svcCnt := len(cld.Spec.ServiceSpec.Services)

	clusterName2Labels := make(map[string]map[string]string, len(capiClusters))
	for _, cl := range capiClusters {
		clusterName2Labels[cl.Namespace+"/"+cl.Name] = cl.Labels
	}

	for _, mcs := range mcsList.Items {
		sel, _ := metav1.LabelSelectorAsSelector(&mcs.Spec.ClusterSelector)

		for _, cl := range capiClusters {
			if sel.Matches(labels.Set(cl.Labels)) {
				svcCnt += len(mcs.Spec.ServiceSpec.Services)
			}
		}
	}

	return svcCnt
}

const (
	amdGPUKey    corev1.ResourceName = "amd.com/gpu"
	nvidiaGPUKey corev1.ResourceName = "nvidia.com/gpu"
)

func accumulateNode(acc *nodeDataAccumulator, node *corev1.Node) {
	acc.count++
	acc.totalCPU += uint64(node.Status.Capacity.Cpu().MilliValue() / 1000)
	acc.totalMemory += uint64(node.Status.Capacity.Memory().Value())

	gpusNC := pointer.To(node.Status.Capacity[nvidiaGPUKey]).Value()
	gpusAC := pointer.To(node.Status.Capacity[amdGPUKey]).Value()
	if gpusNC > 0 || gpusAC > 0 {
		acc.totalGPUNodes++
	}

	info := node.Status.NodeInfo

	nodeInfo := map[string]string{
		"name":                      node.Name,
		"os":                        info.OSImage,
		"arch":                      info.Architecture,
		"kernelVersion":             info.KernelVersion,
		"gpu.amd.bytes_capacity":    strconv.FormatInt(gpusAC, 10),
		"gpu.nvidia.bytes_capacity": strconv.FormatInt(gpusNC, 10),
	}

	kubeFlavor, kubeVersion := "", info.KubeletVersion
	if idx := strings.Index(info.KubeletVersion, "+"); idx >= 0 {
		kubeVersion = info.KubeletVersion[:idx]
		kubeFlavor = info.KernelVersion[idx+1:]
	}

	nodeInfo["kubeVersion"] = kubeVersion
	if kubeFlavor != "" {
		nodeInfo["kubeFlavor"] = kubeFlavor
	}

	acc.nodeInfos = append(acc.nodeInfos, nodeInfo)
}

func accumulatePodGpu(acc *podDataAccumulator, pod *corev1.Pod) {
	gpuRequested := false
	var nvidia, amd int64

	for _, c := range pod.Spec.Containers {
		nReq := pointer.To(c.Resources.Requests[nvidiaGPUKey]).Value()
		aReq := pointer.To(c.Resources.Requests[amdGPUKey]).Value()
		if nReq > 0 || aReq > 0 {
			gpuRequested = true
			nvidia += nReq
			amd += aReq
		}
	}

	if !gpuRequested {
		return
	}

	acc.podsWithGPUReqs++
	if idx, ok := acc.nodeName2Idx[pod.Spec.NodeName]; ok {
		(*acc.nodeInfos)[idx]["gpu.amd.bytes"] = strconv.FormatInt(amd, 10)
		(*acc.nodeInfos)[idx]["gpu.nvidia.bytes"] = strconv.FormatInt(nvidia, 10)
	}
}

func getGpuOperatorPresence(dsList []metav1.PartialObjectMetadata) (nvidiaPresent, amdPresent bool) {
	const (
		nvidiaKey                = "nvidia"
		nvidiaOperatorNamePrefix = "gpu-operator"
		amdKey                   = "amd"
		amdOperatorNamePrefix    = "amd-gpu-operator"
	)
	gpuOperators := make(map[string]bool, 2)

	for _, ds := range dsList {
		if gpuOperators[nvidiaKey] && gpuOperators[amdKey] {
			break
		}

		// TODO: NOTE: how likely both of the operators will be installed?
		name := strings.ToLower(ds.Name)
		if strings.Contains(name, nvidiaOperatorNamePrefix) && !strings.Contains(name, amdOperatorNamePrefix) {
			gpuOperators[nvidiaKey] = true
		}
		if strings.Contains(name, amdOperatorNamePrefix) {
			gpuOperators[amdKey] = true
		}
	}

	return gpuOperators[nvidiaKey], gpuOperators[amdKey]
}

func streamPaginatedPods(ctx context.Context, cl client.Client, limit int64, handle func(pods []*corev1.Pod)) error {
	var cont string
	opts := &client.ListOptions{Limit: limit}

	for {
		opts.Continue = cont
		var list corev1.PodList
		if err := cl.List(ctx, &list, opts); err != nil {
			return fmt.Errorf("failed to list pods: %w", err)
		}

		out := make([]*corev1.Pod, len(list.Items))
		for i := range list.Items {
			out[i] = &list.Items[i]
		}
		handle(out)

		cont = list.Continue
		if cont == "" {
			break
		}
	}
	return nil
}

func streamPaginatedNodes(ctx context.Context, cl client.Client, limit int64, handle func(nodes []*corev1.Node)) error {
	var cont string
	opts := &client.ListOptions{Limit: limit}

	for {
		opts.Continue = cont
		var list corev1.NodeList
		if err := cl.List(ctx, &list, opts); err != nil {
			return fmt.Errorf("failed to list nodes: %w", err)
		}

		items := make([]*corev1.Node, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}

		handle(items)

		cont = list.Continue
		if cont == "" {
			break
		}
	}

	return nil
}

func listPartial(ctx context.Context, c client.Client, gvk schema.GroupVersionKind, limit int64) ([]metav1.PartialObjectMetadata, error) {
	ll := new(metav1.PartialObjectMetadataList)
	ll.SetGroupVersionKind(gvk)

	var (
		result []metav1.PartialObjectMetadata
		cont   string

		opts = &client.ListOptions{Limit: limit}
	)

	for {
		opts.Continue = cont
		if err := c.List(ctx, ll, opts); err != nil {
			return nil, fmt.Errorf("failed to list %s: %w", gvk.String(), err)
		}

		result = append(result, ll.Items...)

		cont = ll.GetContinue()
		if cont == "" {
			break
		}
	}

	return result, nil
}

func (t *SegmentIO) Close(context.Context) error { return t.segmentCl.Close() }
func (*SegmentIO) Flush(context.Context) error   { return nil } // buffer and flushing is done on the segment client's side
