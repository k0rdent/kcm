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
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubevirtv1 "kubevirt.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
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
		childFactory: kubeutil.DefaultClientFactory,
	}, nil
}

// Collect fetches all of the required data from mgmt and child clusters, counting
// and flushing data on a local disk (persistent volume).
// Manages data rotations and failures automatically.
func (l *LocalCollector) Collect(ctx context.Context) error {
	// NOTE: we do not ensure PV/PVC because of arbitrary names
	// either way, absence of either only provides us with extra verbosity

	logger := ctrl.LoggerFrom(ctx).WithName("local-collector")
	ctx = ctrl.LoggerInto(ctx, logger)

	l.file.setLogger(logger.WithName("file").V(1))

	if err := l.file.ensureOpen(); err != nil {
		return fmt.Errorf("failed to ensure temp file for local data: %w", err)
	}

	mgmtData := newMgmtDataFetcher(false /* no mgmt */, false /* no cluster templates */)
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

	start := time.Time{}
	if logger.V(1).Enabled() {
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
			ll := logger.WithValues("cld", cld.Namespace+"/"+cld.Name)

			ll.V(1).Info("starting collecting cluster")

			cldKey := client.ObjectKeyFromObject(&cld)

			secretRef := kubeutil.GetKubeconfigSecretKey(client.ObjectKeyFromObject(&cld))
			childCl, err := kubeutil.GetChildClient(ctx, l.mgmtClient, secretRef, "value", l.childScheme, l.childFactory)
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
			entry.inc(bucketUserServiceCount(usnct))

			entry.label(labelCluster, cldKey.String())
			entry.label(labelCldID, string(cld.UID))
			entry.label(labelClusterID, getK0sClusterID(mgmtData.partialCapiClusters, cldKey))

			ll.V(1).Info("collected child properties", "entry", *entry)

			{
				entriesLock.Lock()
				entries[cldKey.String()] = *entry
				entriesLock.Unlock()
			}
		}()
	}

	wg.Wait()

	if logger.V(1).Enabled() {
		logger.V(1).Info("finished collecting telemetry", "finished_in", time.Since(start))
	}

	logger.V(1).Info("starting flushing collected data")
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
	ce.inc(bucketKubevirtVMIs(len(partialVMIs)))

	// node-related common
	ce.inc(bucketNodeCount(acc.nodesCount))
	ce.inc(bucketCPUTotal(acc.nodesTotalCPU))
	ce.inc(bucketMemoryGi(acc.nodesTotalMemory))

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

	ce.inc(bucketGPUTotal(acc.nodesTotalGPU))
	ce.inc(bucketGPUUsedMi(vendorAMD, acc.nodesTotalAMDUsed))
	ce.inc(bucketGPUUsedMi(vendorNvidia, acc.nodesTotalNvidiaUsed))
	ce.inc(bucketGPUCapacityGi(vendorAMD, acc.nodesTotalAMDCapacity))
	ce.inc(bucketGPUCapacityGi(vendorNvidia, acc.nodesTotalNvidiaCapacity))

	ce.inc(bucketPodsWithGPURequests(acc.podsWithGPUReqs))

	return nil
}

func newClusterEntry() *clusterEntry {
	return &clusterEntry{Counters: make(map[string]uint64), Labels: make(map[string]string)}
}

func (ce *clusterEntry) add(counter string, delta uint64) {
	if counter == "" {
		return
	}
	ce.Counters[counter] += delta
}

func (ce *clusterEntry) inc(counter string) {
	if counter == "" {
		return
	}
	ce.Counters[counter]++
}

func (ce *clusterEntry) label(key, value string) {
	if key == "" {
		return
	}
	if v, ok := ce.Labels[key]; ok && v == value {
		return
	}

	ce.Labels[key] = value
}

func (ce *clusterEntry) ensure(cluster string) {
	if ce.Labels == nil {
		ce.Labels = make(map[string]string)
	}
	if cluster != "" {
		ce.Labels[labelCluster] = cluster
	}
	if ce.Counters == nil {
		ce.Counters = make(map[string]uint64)
	}
}

func (l *LocalCollector) Close(ctx context.Context) error {
	ctrl.LoggerFrom(ctx).Info("closing local collector")
	return l.file.closeAndRotate()
}
