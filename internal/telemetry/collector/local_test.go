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
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"testing"

	"github.com/go-logr/logr"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	kubevirtv1 "kubevirt.io/api/core/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func Test_clusterEntry(t *testing.T) {
	t.Parallel()

	var ce clusterEntry
	ce.ensure("foo/ns")
	ce.inc("a")
	ce.inc("a")
	ce.add("a", 3)
	ce.label("x", "y")
	ce.label("x", "y")
	ce.label("", "y")
	ce.inc("")
	ce.add("", 3)

	require.Equal(t, uint64(5), ce.Counters["a"])
	require.Equal(t, "foo/ns", ce.Labels[labelCluster])
	require.Equal(t, "y", ce.Labels["x"])
	require.Zero(t, ce.Counters[""])
	require.Empty(t, ce.Labels[""])
}

func Test_localCollector_collectChildProperties(t *testing.T) {
	type seed struct {
		nodes []*corev1.Node
		pods  []*corev1.Pod
		ds    []*appsv1.DaemonSet
		vmis  []*kubevirtv1.VirtualMachineInstance
	}

	cases := []struct {
		name         string
		in           seed
		wantCounters map[string]uint64
	}{
		{
			name: "mixed cluster with GPU, operators, VMIs",
			in: seed{
				nodes: []*corev1.Node{
					makeNode(t, "n1", "amd64", "linux", "v1.30.4", 4, 8, 64<<30, 0),
					makeNode(t, "n2", "arm64", "linux", "v1.31.0", 4, 16, 0, 200<<30),
				},
				pods: []*corev1.Pod{
					makePod(t, "", "p1", "n1", 1<<30, 512<<20),
					makePod(t, "", "p2", "n1", 512<<20, 512<<20),
					makePod(t, "", "p3", "n1", 2<<30, 1<<30),
				},
				ds: []*appsv1.DaemonSet{
					makeDS(t, "gpu-operator"),
					makeDS(t, "amd-gpu-operator"),
				},
				vmis: []*kubevirtv1.VirtualMachineInstance{
					makeVMI(t, "vmi-1"),
					makeVMI(t, "vmi-2"),
					makeVMI(t, "vmi-3"),
					makeVMI(t, "vmi-4"),
					makeVMI(t, "vmi-5"),
					makeVMI(t, "vmi-6"),
					makeVMI(t, "vmi-7"),
				},
			},
			wantCounters: map[string]uint64{
				// snapshot buckets
				"kubevirt.vmis:6-10":                  1, // 7 VMIs
				"node.count:1-2":                      1, // 2 nodes
				"node.cpu.total:5-8":                  1, // 8 cores total
				"node.memory.gibibytes:17-32":         1, // 24Gi total
				"node.gpu.total:1-2":                  1, // 1 node with GPU capacity
				"gpu.operator_installed.nvidia:true":  1,
				"gpu.operator_installed.amd:true":     1,
				"gpu.nvidia.mebibytes:2049-4096":      1, // 3.5Gi requested
				"gpu.amd.mebibytes:1025-2048":         1, // 2Gi requested
				"gpu.nvidia.gibibytes_capacity:33-64": 1, // 64Gi capacity
				"gpu.amd.gibibytes_capacity:129-256":  1, // 200Gi capacity
				"pods.with_gpu_requests:3-4":          1, // 3 pods with GPU reqs

				// distributions buckets
				"node.info.kubeVersion:1.30": 1,
				"node.info.kubeVersion:1.31": 1,
				"node.info.arch:amd64":       1,
				"node.info.arch:arm64":       1,
				"node.info.os:linux":         2,
			},
		},
		{
			name: "single node invalid kube version, only nvidia operator, no pods",
			in: seed{
				nodes: []*corev1.Node{
					makeNode(t, "solo", "riscv", "Darwin", "not-a-version", 2, 2, 0, 0),
				},
				pods: nil,
				ds: []*appsv1.DaemonSet{
					makeDS(t, "gpu-operator"),
				},
				vmis: nil,
			},
			wantCounters: map[string]uint64{
				"node.count:1-2":                     1,
				"node.cpu.total:1-2":                 1,
				"node.memory.gibibytes:1-2":          1,
				"gpu.operator_installed.nvidia:true": 1,
				"gpu.operator_installed.amd:false":   1,
				"node.info.arch:other":               1,
				"node.info.os:other":                 1,
			},
		},
		{
			name: "three linux/amd64 nodes, no GPU pods (ignored)",
			in: seed{
				nodes: []*corev1.Node{
					makeNode(t, "a", "amd64", "linux", "v1.30.1", 1, 1, 0, 0),
					makeNode(t, "b", "amd64", "linux", "v1.30.2", 1, 1, 0, 0),
					makeNode(t, "c", "amd64", "linux", "v1.30.3", 1, 1, 0, 0),
				},
				pods: []*corev1.Pod{
					makePod(t, "", "no-gpu", "a", 0, 0), // ignored
				},
				ds:   nil,
				vmis: nil,
			},
			wantCounters: map[string]uint64{
				"gpu.operator_installed.amd:false":    1,
				"gpu.operator_installed.nvidia:false": 1,
				"node.count:3-4":                      1,
				"node.cpu.total:3-4":                  1,
				"node.memory.gibibytes:3-4":           1,
				"node.info.kubeVersion:1.30":          3,
				"node.info.arch:amd64":                3,
				"node.info.os:linux":                  3,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqs := require.New(t)
			scheme, err := getChildScheme()
			reqs.NoError(err)

			var objs []client.Object
			for _, o := range tc.in.nodes {
				objs = append(objs, o)
			}
			for _, o := range tc.in.pods {
				objs = append(objs, o)
			}
			for _, o := range tc.in.ds {
				objs = append(objs, o)
			}
			for _, o := range tc.in.vmis {
				objs = append(objs, o)
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			ctx := ctrl.LoggerInto(t.Context(), logr.Discard())

			ce := newClusterEntry()
			reqs.NoError(ce.collectChildProperties(ctx, cl))

			reqs.Subset(tc.wantCounters, ce.Counters) // we want no extra counters in the actual data
		})
	}
}

func Test_localCollector_Collect(t *testing.T) {
	type childSeed struct {
		tag     string // deliver a cluster's corresponding to the tag set of objects
		objects []client.Object
		listErr bool // if true fail child list calls
	}

	type mgmtSeed struct {
		clusterDeployments []*kcmv1.ClusterDeployment
		capiClusters       []*clusterapiv1.Cluster
		secrets            []*corev1.Secret
	}

	type expect struct {
		counters map[string]uint64
		labels   map[string]string
	}

	type tc struct {
		name      string
		mgmt      mgmtSeed
		children  []childSeed       // child clusters
		wantByCLD map[string]expect // key is cld name
	}

	const (
		nsOne  = "ns1"
		cldOne = "cld1"

		nsSome  = "someNs"
		cldSome = "cldSomeName"
		cldErr  = "cldErrName"
	)

	nsName := func(ns, name string) string { return ns + "/" + name }

	cases := []tc{
		{
			name: "single cluster happy path",
			mgmt: mgmtSeed{
				clusterDeployments: []*kcmv1.ClusterDeployment{
					makeCLD(t, nsOne, cldOne, "tpl-1", "OneTime", 10),
				},
				capiClusters: []*clusterapiv1.Cluster{
					makeCAPICluster(t, nsOne, cldOne, "kube-system:abcd-1234"),
				},
				secrets: []*corev1.Secret{
					makeKubeconfigSecret(t, nsOne, cldOne, "child:"+nsName(nsOne, cldOne)),
				},
			},
			children: []childSeed{
				{
					tag: "child:" + nsName(nsOne, cldOne),
					objects: []client.Object{
						makeNode(t, "n1", "amd64", "linux", "v1.30.4", 4, 8, 64<<30, 0),
						makeNode(t, "n2", "amd64", "linux", "v1.30.1", 4, 16, 0, 0),
						makePod(t, nsOne, "p1", "n1", 1024<<20, 512<<20),
						makeDS(t, "gpu-operator"),
						makeDS(t, "amd-gpu-operator"),
						makeVMI(t, "vmi-a"),
						makeVMI(t, "vmi-b"),
						makeVMI(t, "vmi-c"),
						makeVMI(t, "vmi-d"),
						makeVMI(t, "vmi-e"),
						makeVMI(t, "vmi-f"),
						makeVMI(t, "vmi-g"),
					},
				},
			},
			wantByCLD: map[string]expect{
				nsName(nsOne, cldOne): {
					counters: map[string]uint64{
						"scrapes":                             1,
						"kubevirt.vmis:6-10":                  1,
						"node.count:1-2":                      1,
						"node.cpu.total:5-8":                  1,
						"node.memory.gibibytes:17-32":         1,
						"node.gpu.total:1-2":                  1,
						"pods.with_gpu_requests:1-2":          1,
						"gpu.operator_installed.nvidia:true":  1,
						"gpu.operator_installed.amd:true":     1,
						"gpu.nvidia.mebibytes:513-1024":       1,
						"gpu.amd.mebibytes:257-512":           1,
						"gpu.nvidia.gibibytes_capacity:33-64": 1,
						"node.info.kubeVersion:1.30":          2,
						"node.info.arch:amd64":                2,
						"node.info.os:linux":                  2,
						"template:tpl-1":                      1,
						"syncMode:onetime":                    1,
						"userServiceCount:6-10":               1,
					},
					labels: map[string]string{
						"cluster":             nsName(nsOne, cldOne),
						"clusterDeploymentID": "uuid:" + nsName(nsOne, cldOne),
						"clusterID":           "kube-system:abcd-1234",
					},
				},
			},
		},
		{
			name: "one ok cluster, another failed, expect failures counter",
			mgmt: mgmtSeed{
				clusterDeployments: []*kcmv1.ClusterDeployment{
					makeCLD(t, nsSome, cldSome, "tpl", "OneTime", 2),
					makeCLD(t, nsSome, cldErr, "tpl", "continuous", 0),
				},
				secrets: []*corev1.Secret{
					makeKubeconfigSecret(t, nsSome, cldSome, "child:"+nsName(nsSome, cldSome)),
					makeKubeconfigSecret(t, nsSome, cldErr, "child:"+nsName(nsSome, cldErr)),
				},
			},
			children: []childSeed{
				{
					tag: "child:" + nsName(nsSome, cldSome),
					objects: []client.Object{
						makeNode(t, "n1", "amd64", "linux", "v1.30.4", 2, 2, 0, 0),
					},
				},
				{
					tag:     "child:" + nsName(nsSome, cldErr),
					objects: []client.Object{},
					listErr: true,
				},
			},
			wantByCLD: map[string]expect{
				nsName(nsSome, cldSome): {
					counters: map[string]uint64{
						"scrapes":                             1,
						"node.count:1-2":                      1,
						"node.cpu.total:1-2":                  1,
						"node.memory.gibibytes:1-2":           1,
						"userServiceCount:1-2":                1,
						"syncMode:onetime":                    1,
						"template:tpl":                        1,
						"gpu.operator_installed.nvidia:false": 1,
						"gpu.operator_installed.amd:false":    1,
						"node.info.arch:amd64":                1,
						"node.info.os:linux":                  1,
						"node.info.kubeVersion:1.30":          1,
					},
					labels: map[string]string{
						"cluster":             nsName(nsSome, cldSome),
						"clusterDeploymentID": "uuid:" + nsName(nsSome, cldSome),
						"clusterID":           "", // set but empty
					},
				},
				nsName(nsSome, cldErr): {
					counters: map[string]uint64{
						"scrapes":  1,
						"failures": 1,

						"syncMode:continuous": 1,
						"template:tpl":        1,
					},
					labels: map[string]string{
						"cluster":             nsName(nsSome, cldErr),
						"clusterDeploymentID": "uuid:" + nsName(nsSome, cldErr),
						"clusterID":           "", // set but empty
					},
				},
			},
		},
		{
			name: "no secret expect nothing",
			mgmt: mgmtSeed{
				clusterDeployments: []*kcmv1.ClusterDeployment{
					makeCLD(t, nsSome, "no-secret", "tpl", "continuous", 0),
				},
			},
			children:  nil,
			wantByCLD: map[string]expect{},
		},
		{
			name:      "no clusterdeployments expect nothing",
			mgmt:      mgmtSeed{},
			children:  nil,
			wantByCLD: map[string]expect{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqs := require.New(t)

			mgmtScheme := buildMgmtScheme(t)
			var mgmtObjs []client.Object
			for _, cd := range tc.mgmt.clusterDeployments {
				mgmtObjs = append(mgmtObjs, cd)
			}
			for _, s := range tc.mgmt.secrets {
				mgmtObjs = append(mgmtObjs, s)
			}
			for _, capiCluster := range tc.mgmt.capiClusters {
				mgmtObjs = append(mgmtObjs, capiCluster)
			}
			mgmtClient := fake.NewClientBuilder().
				WithScheme(mgmtScheme).
				WithObjects(mgmtObjs...).
				Build()

			fakeFactory := func(kubeconfig []byte, scheme *runtime.Scheme) (client.Client, error) {
				tag := string(kubeconfig)
				var seed *childSeed
				for i := range tc.children {
					if tc.children[i].tag == tag {
						seed = &tc.children[i]
						break
					}
				}
				if seed == nil {
					return nil, fmt.Errorf("unrecognized kubeconfig tag %q", tag)
				}
				builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(seed.objects...)

				baseClient := builder.Build()
				if seed.listErr {
					return listErrorClient{Client: baseClient}, nil
				}
				return baseClient, nil
			}

			dir := t.TempDir()
			lc, err := NewLocalCollector(mgmtClient, dir, 5 /* hardcoded, no effect */)
			reqs.NoError(err)
			lc.childFactory = fakeFactory

			ctx := ctrl.LoggerInto(t.Context(), logr.Discard())
			reqs.NoError(lc.Collect(ctx))

			data, err := os.ReadFile(lc.file.tempPath())
			reqs.NoError(err)

			var st fileState
			err = json.Unmarshal(data, &st)
			if len(tc.wantByCLD) == 0 {
				reqs.Error(err) // we expect nothing in the file hence error of deserializing
			} else {
				reqs.NoError(err)
			}

			got := map[string]clusterEntry{}
			for _, e := range st.Clusters {
				key := e.Labels[labelCluster]
				got[key] = e
			}

			// check
			reqs.Len(got, len(tc.wantByCLD), "mismatch in number of clusters flushed")
			for cldName, want := range tc.wantByCLD {
				reqs.Contains(got, cldName, "expected to have cluster key", cldName)
				entry := got[cldName]

				reqs.Subsetf(want.counters, entry.Counters, "cluster %q has unexpected counters", cldName)
				reqs.Subsetf(want.labels, entry.Labels, "cluster %q has unexpected labels", cldName)
			}
		})
	}
}

func Test_localCollector_Close(t *testing.T) {
	t.Parallel()

	reqs := require.New(t)

	dir := t.TempDir()
	f, err := newFile(dir)
	reqs.NoError(err)

	lc := &LocalCollector{file: f}
	reqs.NoError(lc.Close(t.Context()))

	// same-day close should not create permanent
	_, err = os.Stat(f.permanentPath())
	reqs.ErrorIs(err, fs.ErrNotExist, "permanent should not exist on same-day close")
}

func buildMgmtScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	reqs := require.New(t)

	s := runtime.NewScheme()
	reqs.NoError(clientgoscheme.AddToScheme(s))
	reqs.NoError(kcmv1.AddToScheme(s))
	reqs.NoError(clusterapiv1.AddToScheme(s))
	reqs.NoError(libsveltosv1beta1.AddToScheme(s))
	return s
}

func makeKubeconfigSecret(t *testing.T, ns, name, kubeconfigTag string) *corev1.Secret {
	t.Helper()

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name + "-kubeconfig",
		},
		Data: map[string][]byte{
			"value": []byte(kubeconfigTag),
		},
	}
}

func makeCLD(t *testing.T, ns, name, tpl, syncMode string, services int) *kcmv1.ClusterDeployment {
	t.Helper()

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
			UID:       types.UID("uuid:" + ns + "/" + name),
		},
	}

	cd.Spec.Template = tpl
	cd.Spec.ServiceSpec.SyncMode = syncMode
	if services > 0 {
		cd.Spec.ServiceSpec.Services = make([]kcmv1.Service, services)
	}
	return cd
}

func makeCAPICluster(t *testing.T, ns, name, clusterID string) *clusterapiv1.Cluster {
	t.Helper()

	return &clusterapiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Annotations: map[string]string{k0sClusterIDAnnotation: clusterID},
		},
	}
}

type listErrorClient struct{ client.Client }

func (listErrorClient) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return errors.New("fake list error")
}

func makeNode(t *testing.T, name, arch, operatingSystem, kubeVer string, cpuCores, memGiB int, nvidiaCap, amdCap int64) *corev1.Node {
	t.Helper()

	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{
				Architecture:    arch,
				OperatingSystem: operatingSystem,
				KubeletVersion:  kubeVer,
			},
			Capacity: corev1.ResourceList{},
		},
	}
	n.Status.Capacity[corev1.ResourceCPU] = resource.MustParse(strconv.Itoa(cpuCores))
	n.Status.Capacity[corev1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%dGi", memGiB))
	if nvidiaCap != 0 {
		n.Status.Capacity[nvidiaGPUKey] = *resource.NewQuantity(nvidiaCap, resource.DecimalSI)
	}
	if amdCap != 0 {
		n.Status.Capacity[amdGPUKey] = *resource.NewQuantity(amdCap, resource.DecimalSI)
	}
	return n
}

func makePod(t *testing.T, ns, name, nodeName string, nvidiaReq, amdReq int64) *corev1.Pod {
	t.Helper()

	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{},
				},
			}},
		},
	}
	if nvidiaReq != 0 {
		p.Spec.Containers[0].Resources.Requests[nvidiaGPUKey] = *resource.NewQuantity(nvidiaReq, resource.DecimalSI)
	}
	if amdReq != 0 {
		p.Spec.Containers[0].Resources.Requests[amdGPUKey] = *resource.NewQuantity(amdReq, resource.DecimalSI)
	}
	return p
}

func makeVMI(t *testing.T, name string) *kubevirtv1.VirtualMachineInstance {
	t.Helper()

	return &kubevirtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func makeDS(t *testing.T, name string) *appsv1.DaemonSet {
	t.Helper()

	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}
