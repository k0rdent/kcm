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

package collector

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubevirtv1 "kubevirt.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

type (
	LocalCollector struct {
		file         *file
		mgmtClient   client.Client
		childScheme  *runtime.Scheme
		childFactory func([]byte, *runtime.Scheme) (client.Client, error) // for test mocks
		concurrency  int
	}

	// clusterEntry holds per-cluster in-memory bucketed counters and scrapes.
	// The structure is not supposed for concurrent usage.
	clusterEntry struct {
		Counters map[string]uint64 `json:"counters,omitempty"`
		Labels   map[string]string `json:"labels,omitempty"`
	}
)

const labelCluster = "cluster" // clusterdeployment namespaced name, used for cluster search

// NewLocalCollector creates a new instance of the [LocalCollector].
func NewLocalCollector(mgmtClient client.Client, baseDir string, concurrency int) (*LocalCollector, error) {
	childScheme, err := getChildScheme()
	if err != nil {
		return nil, fmt.Errorf("failed to create child client scheme: %w", err)
	}

	f, err := newFile(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create new file for telemetry collection: %w", err)
	}

	return &LocalCollector{
		file:         f,
		mgmtClient:   mgmtClient,
		childScheme:  childScheme,
		concurrency:  concurrency,
		childFactory: defaultClientFactory,
	}, nil
}

// Collect fetches all of the required data from mgmt and child clusters, counting
// and flushing data on a local disk (persistent volume).
// Manages data rotations and failures automatically.
func (l *LocalCollector) Collect(ctx context.Context) error {
	// NOTE: we do not ensure PV/PVC because of arbitrary names
	// either way, absence of these only gives us more verbosity

	if err := l.file.ensureOpen(); err != nil {
		return fmt.Errorf("failed to ensure temp file for local data: %w", err)
	}

	logger := ctrl.LoggerFrom(ctx).WithName("local-collector")
	ctx = ctrl.LoggerInto(ctx, logger)

	mgmtData := newMgmtDataFetcher(false)
	if err := mgmtData.fetch(ctx, l.mgmtClient); err != nil {
		return fmt.Errorf("failed to fetch data from mgmt cluster: %w", err)
	}

	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, l.concurrency)

		entriesLock sync.Mutex
		entries     = make(map[string]clusterEntry, len(mgmtData.clusterDeployments.Items))
	)

	const (
		counterScrapes  = "scrapes"
		counterFailures = "failures"

		labelCldID     = "clusterDeploymentID"
		labelClusterID = "clusterID"
	)
	for _, cld := range mgmtData.clusterDeployments.Items {
		wg.Add(1)
		sem <- struct{}{}

		go func() {
			defer func() {
				wg.Done()
				<-sem
			}()
			ll := logger.WithValues("cld", cld.Namespace+"/"+cld.Name)

			template, ok := mgmtData.tplName2Template[client.ObjectKey{Namespace: cld.Namespace, Name: cld.Spec.Template}] // NOTE: it's okay to read concurrently here since NO writes occur; checked with -race flag
			if !ok {
				template = &kcmv1.ClusterTemplate{}
			}

			cldKey := client.ObjectKeyFromObject(&cld)

			childCl, err := getChildClient(ctx, l.mgmtClient, cldKey, l.childScheme, l.childFactory)
			if err != nil {
				ll.Error(err, "failed to get child kubeconfig")
				return
			}

			entry := newClusterEntry()

			if err := entry.collectChildProperties(ctx, childCl); err != nil {
				ll.Error(err, "failed to collect properties")
				entry.inc(counterFailures)
			}

			entry.inc(counterScrapes)
			entry.inc(bucketTemplate(cld.Spec.Template))
			entry.inc(bucketSyncMode(cld.Spec.ServiceSpec.SyncMode))
			usnct := countUserServices(&cld, mgmtData.mcsList, mgmtData.partialClustersToMatch)
			entry.add(bucketUserServiceCount(usnct), uint64(usnct))
			for _, p := range template.Status.Providers {
				entry.inc(bucketProvider(p))
			}

			entry.label(labelCluster, cldKey.String())
			entry.label(labelCldID, string(cld.UID))
			entry.label(labelCluster, getK0sClusterID(mgmtData.partialCapiClusters, cldKey))

			{
				entriesLock.Lock()
				entries[cldKey.String()] = *entry
				entriesLock.Unlock()
			}
		}()
	}

	if err := l.file.flush(entries); err != nil {
		return fmt.Errorf("failed to flush collected data: %w", err)
	}

	return nil
}

func (ce *clusterEntry) collectChildProperties(ctx context.Context, childCl client.Client) error {
	acc := newLocalAccumulator()
	if err := streamPaginatedNodes(ctx, childCl, 50, func(nodes []*corev1.Node) {
		for _, node := range nodes {
			acc.accumulateNode(node)
		}
	}); err != nil {
		return fmt.Errorf("failed to accumulate nodes info during listing: %w", err)
	}

	if err := streamPaginatedPods(ctx, childCl, 100, func(pods []*corev1.Pod) {
		for _, pod := range pods {
			acc.accumulatePod(pod)
		}
	}); err != nil {
		return fmt.Errorf("failed to accumulate pods info during listing: %w", err)
	}

	dsGVK := schema.GroupVersionKind{
		Group:   appsv1.SchemeGroupVersion.Group,
		Version: appsv1.SchemeGroupVersion.Version,
		Kind:    "DaemonSet",
	}
	partialDaemonSets, err := listAsPartial(ctx, childCl, dsGVK)
	if err != nil {
		return fmt.Errorf("failed to list all daemon sets: %w", err)
	}

	partialVMIs, err := listAsPartial(ctx, childCl, kubevirtv1.VirtualMachineInstanceGroupVersionKind)
	if err != nil {
		if !meta.IsNoMatchError(err) {
			return fmt.Errorf("failed to list kubevirt instances: %w", err)
		}
	}

	// kubevirt
	ce.add(bucketKubevirtVMIs(len(partialVMIs)), uint64(len(partialVMIs)))

	// node-related common
	ce.add(bucketNodeCount(acc.nodesCount), acc.nodesCount)

	ce.add(bucketCPUTotal(acc.nodesTotalCPU), acc.nodesTotalCPU)
	ce.add(bucketMemory(acc.nodesTotalMemory), acc.nodesTotalMemory)

	for ver, n := range acc.kubeVerCount {
		ce.add(bucketKubeVersion(ver), n)
	}
	for arch, n := range acc.archCount {
		ce.add(bucketArch(arch), n)
	}
	for os, n := range acc.osCount {
		ce.add(bucketOS(os), n)
	}

	// gpu-related
	noi, aoi := getGpuOperatorPresence(partialDaemonSets)
	const vendorAMD, vendorNvidia = "amd", "nvidia"
	ce.inc(bucketOperatorInstalled(vendorNvidia, noi))
	ce.inc(bucketOperatorInstalled(vendorAMD, aoi))

	ce.add(bucketGPUTotal(acc.nodesTotalGPUNodes), acc.nodesTotalGPUNodes)
	ce.add(bucketGPUGiUsed(vendorAMD, acc.nodesTotalAMDGiBUsed), acc.nodesTotalAMDGiBUsed)
	ce.add(bucketGPUGiUsed(vendorNvidia, acc.nodesTotalNvidiaGiBUsed), acc.nodesTotalNvidiaGiBUsed)
	ce.add(bucketGPUGiCapacity(vendorAMD, acc.nodesTotalAMDGiBCapacity), acc.nodesTotalAMDGiBCapacity)
	ce.add(bucketGPUGiCapacity(vendorNvidia, acc.nodesTotalNvidiaGiBCapacity), acc.nodesTotalNvidiaGiBCapacity)

	ce.add(bucketPodsWithGPURequests(acc.podsWithGPUReqs), acc.podsWithGPUReqs)

	return nil
}

func newClusterEntry() *clusterEntry {
	return &clusterEntry{Counters: make(map[string]uint64), Labels: make(map[string]string)}
}

func (ce *clusterEntry) add(counter string, delta uint64) {
	ce.Counters[counter] += delta
}

func (ce *clusterEntry) inc(counter string) {
	ce.Counters[counter]++
}

func (ce *clusterEntry) label(key, value string) {
	if v, ok := ce.Labels[key]; ok && v == value {
		return
	}

	ce.Labels[key] = value
}

func (l *LocalCollector) Flush(ctx context.Context) error {
	ctrl.LoggerFrom(ctx).Info("flushing local collector data")

	return l.file.flush(nil)
}

func (l *LocalCollector) Close(ctx context.Context) error {
	ctrl.LoggerFrom(ctx).Info("closing local collector")
	return l.file.closeAndRotate()
}

// buckets
func bucketNodeCount(n uint64) string {
	switch {
	case n <= 10:
		return "node.count:1-10"
	case n <= 20:
		return "node.count:11-20"
	case n <= 30:
		return "node.count:21-30"
	case n <= 40:
		return "node.count:31-40"
	case n <= 50:
		return "node.count:41-50"
	case n <= 100:
		return "node.count:51-100"
	default:
		return "node.count:101+"
	}
}

func bucketUserServiceCount(n int) string {
	switch {
	case n == 0:
		return "userServiceCount:0"
	case n <= 5:
		return "userServiceCount:1-5"
	case n <= 10:
		return "userServiceCount:6-10"
	case n <= 25:
		return "userServiceCount:11-25"
	case n <= 50:
		return "userServiceCount:26-50"
	default:
		return "userServiceCount:51+"
	}
}

func bucketCPUTotal(cores uint64) string {
	switch {
	case cores <= 4:
		return "node.cpu.total:0-4"
	case cores <= 8:
		return "node.cpu.total:4-8"
	case cores <= 16:
		return "node.cpu.total:8-16"
	case cores <= 32:
		return "node.cpu.total:16-32"
	case cores <= 64:
		return "node.cpu.total:32-64"
	case cores <= 128:
		return "node.cpu.total:64-128"
	case cores <= 256:
		return "node.cpu.total:128-256"
	case cores <= 512:
		return "node.cpu.total:256-512"
	default:
		return "node.cpu.total:512+"
	}
}

func bucketMemory(bytes uint64) string {
	switch {
	case bytes <= 16<<30:
		return "node.memory.gibibytes:0-16"
	case bytes <= 32<<30:
		return "node.memory.gibibytes:16-32"
	case bytes <= 64<<30:
		return "node.memory.gibibytes:32-64"
	case bytes <= 128<<30:
		return "node.memory.gibibytes:64-128"
	case bytes <= 256<<30:
		return "node.memory.gibibytes:128-256"
	case bytes <= 512<<30:
		return "node.memory.gibibytes:256-512"
	case bytes <= 1024<<30:
		return "node.memory.gibibytes:512-1024"
	default:
		return "node.memory.gibibytes:1024+"
	}
}

func bucketGPUTotal(gpu uint64) string {
	switch {
	case gpu == 0:
		return "node.gpu.total:0"
	case gpu <= 2:
		return "node.gpu.total:1-2"
	case gpu <= 4:
		return "node.gpu.total:3-4"
	case gpu <= 8:
		return "node.gpu.total:5-8"
	case gpu <= 16:
		return "node.gpu.total:9-16"
	case gpu <= 32:
		return "node.gpu.total:17-32"
	case gpu <= 64:
		return "node.gpu.total:33-64"
	case gpu <= 128:
		return "node.gpu.total:65-128"
	case gpu <= 256:
		return "node.gpu.total:129-256"
	default:
		return "node.gpu.total:257+"
	}
}

var kubeVersionBuckets = map[string]struct{}{
	"1.29": {},
	"1.30": {},
	"1.31": {},
	"1.33": {},
	"1.34": {},
	"1.35": {},
	"1.36": {},
	"1.37": {},
}

func bucketKubeVersion(v string) string {
	if _, ok := kubeVersionBuckets[v]; ok {
		return "node.info.kubeVersion:" + v
	}
	return "node.info.kubeVersion:other"
}

func bucketOS(osname string) string {
	switch strings.ToLower(osname) {
	case "linux":
		return "node.info.os:linux"
	case "windows":
		return "node.info.os:windows"
	default:
		return "node.info.os:other"
	}
}

func bucketArch(arch string) string {
	switch strings.ToLower(arch) {
	case "amd64":
		return "node.info.arch:amd64"
	case "arm64":
		return "node.info.arch:arm64"
	default:
		return "node.info.arch:other"
	}
}

func bucketProvider(p string) string {
	// (bootstrap|control-plane|infrastructure)-<provider>
	// NOTE: very opinionated metric, let us reflect whether we actually need it
	return "providers:" + p
}

func bucketOperatorInstalled(typ string, v bool) string {
	return "gpu.operator_installed." + typ + ":" + strconv.FormatBool(v)
}

func bucketSyncMode(mode string) string {
	return "syncMode:" + strings.ToLower(mode)
}

func bucketPodsWithGPURequests(n uint64) string {
	switch {
	case n == 0:
		return "pods.with_gpu_requests:0"
	case n <= 4:
		return "pods.with_gpu_requests:1-4"
	case n <= 16:
		return "pods.with_gpu_requests:5-16"
	case n <= 64:
		return "pods.with_gpu_requests:17-64"
	case n <= 256:
		return "pods.with_gpu_requests:64-256"
	default:
		return "pods.with_gpu_requests:257+"
	}
}

func bucketKubevirtVMIs(n int) string {
	switch {
	case n == 0:
		return "kubevirt.vmis:0"
	case n <= 5:
		return "kubevirt.vmis:1-5"
	case n <= 10:
		return "kubevirt.vmis:6-10"
	case n <= 20:
		return "kubevirt.vmis:11-20"
	case n <= 50:
		return "kubevirt.vmis:21-50"
	default:
		return "kubevirt.vmis:51+"
	}
}

func bucketTemplate(name string) string {
	return "template:" + name
}

func bucketGPUGiCapacity(typ string, bytes uint64) string {
	prefix := fmt.Sprintf("gpu.%s.gibibytes_capacity:", typ)
	switch {
	case bytes == 0:
		return prefix + "0"
	case bytes <= 4<<30:
		return prefix + "1-4"
	case bytes <= 8<<30:
		return prefix + "4-8"
	case bytes <= 16<<30:
		return prefix + "8-16"
	case bytes <= 32<<30:
		return prefix + "16-32"
	case bytes <= 64<<30:
		return prefix + "32-64"
	case bytes <= 128<<30:
		return prefix + "64-128"
	case bytes <= 256<<30:
		return prefix + "128-256"
	case bytes <= 512<<30:
		return prefix + "256-512"
	case bytes <= 1024<<30:
		return prefix + "512-1024"
	default:
		return prefix + "1024+"
	}
}

func bucketGPUGiUsed(typ string, bytes uint64) string {
	prefix := fmt.Sprintf("gpu.%s.gibibytes:", typ)
	switch {
	case bytes == 0:
		return prefix + "0"
	case bytes <= 4<<30:
		return prefix + "1-4"
	case bytes <= 8<<30:
		return prefix + "4-8"
	case bytes <= 16<<30:
		return prefix + "8-16"
	case bytes <= 32<<30:
		return prefix + "16-32"
	case bytes <= 64<<30:
		return prefix + "32-64"
	case bytes <= 128<<30:
		return prefix + "64-128"
	case bytes <= 256<<30:
		return prefix + "128-256"
	default:
		return prefix + "256+"
	}
}
