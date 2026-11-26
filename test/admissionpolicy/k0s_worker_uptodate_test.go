// Copyright 2026
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

// Package admissionpolicy_test runs the admission policies shipped in the charts
// against a real kube-apiserver.
//
// TODO(KLUDGE): DELETE THIS FILE together with the k0s worker UpToDate kludge once
// https://github.com/kubernetes-sigs/cluster-api/issues/14086 is fixed. It tests the
// kludge itself; the e2e validation "workers-uptodate" is the one to keep.
package admissionpolicy_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	runtimev1 "sigs.k8s.io/cluster-api/api/runtime/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	repoRoot   = "../.."
	namespace  = "default"
	policyFile = "templates/provider/kcm-regional/files/k0s-worker-uptodate-policy.yaml"
)

// machineControllerOwnedConditions mirrors the patch options of the CAPI Machine controller
// (core/reconcilers/machine/machine_controller.go), so writes here take the same path
var machineControllerOwnedConditions = patch.WithOwnedConditions{Conditions: []string{
	clusterapiv1.PausedCondition,
	clusterapiv1.MachineAvailableCondition,
	clusterapiv1.MachineReadyCondition,
	clusterapiv1.MachineBootstrapConfigReadyCondition,
	clusterapiv1.MachineInfrastructureReadyCondition,
	clusterapiv1.MachineNodeReadyCondition,
	clusterapiv1.MachineNodeHealthyCondition,
	clusterapiv1.MachineDeletingCondition,
	clusterapiv1.MachineUpdatingCondition,
}}

func TestK0sWorkerUpToDatePolicy(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set, run via make test")
	}

	c := startEnv(t)

	// a Machine that is stuck before the policy exists
	createMachine(t, c, "pre", "v1.36.3+k0s.1", "K0sWorkerConfig", nil)
	pre := reconcileAsCAPI(t, c, "pre", "v1.36.3+k0s", false)
	expectUpToDate(t, pre, metav1.ConditionFalse, "stuck without the policy")

	installPolicy(t, c)
	waitForPolicy(t, c)

	t.Run("new worker differing only by the k0s build becomes up to date", func(t *testing.T) {
		createMachine(t, c, "build", "v1.36.3+k0s.1", "K0sWorkerConfig", nil)
		m := reconcileAsCAPI(t, c, "build", "v1.36.3+k0s", false)
		expectUpToDate(t, m, metav1.ConditionTrue, "")

		rv, ltt := m.ResourceVersion, conditions.Get(m, clusterapiv1.MachineUpToDateCondition).LastTransitionTime
		for range 10 {
			m = reconcileAsCAPI(t, c, "build", "v1.36.3+k0s", false)
		}
		if m.ResourceVersion != rv {
			t.Fatalf("repeated capi reconciles changed the Machine: resourceVersion %s -> %s", rv, m.ResourceVersion)
		}
		if got := conditions.Get(m, clusterapiv1.MachineUpToDateCondition).LastTransitionTime; !got.Equal(&ltt) {
			t.Fatalf("lastTransitionTime changed: %s -> %s", ltt, got)
		}
	})

	t.Run("bare spec version becomes up to date", func(t *testing.T) {
		createMachine(t, c, "bare", "v1.36.3", "K0sWorkerConfig", nil)
		expectUpToDate(t, reconcileAsCAPI(t, c, "bare", "v1.36.3+k0s", false), metav1.ConditionTrue, "")
	})

	t.Run("kubernetes version mismatch stays not up to date", func(t *testing.T) {
		createMachine(t, c, "k8s", "v1.36.3+k0s.1", "K0sWorkerConfig", nil)
		expectUpToDate(t, reconcileAsCAPI(t, c, "k8s", "v1.36.2+k0s", false), metav1.ConditionFalse, "")
	})

	t.Run("in-place update stays not up to date", func(t *testing.T) {
		createMachine(t, c, "updating", "v1.36.3+k0s.2", "K0sWorkerConfig", nil)
		expectUpToDate(t, reconcileAsCAPI(t, c, "updating", "v1.36.3+k0s", true), metav1.ConditionFalse, "")

		createMachine(t, c, "moved", "v1.36.3+k0s.2", "K0sWorkerConfig", map[string]string{clusterapiv1.UpdateInProgressAnnotation: ""})
		expectUpToDate(t, reconcileAsCAPI(t, c, "moved", "v1.36.3+k0s", false), metav1.ConditionFalse, "update-in-progress annotation")

		createMachine(t, c, "hook", "v1.36.3+k0s.2", "K0sWorkerConfig", map[string]string{runtimev1.PendingHooksAnnotation: "UpdateMachine"})
		expectUpToDate(t, reconcileAsCAPI(t, c, "hook", "v1.36.3+k0s", false), metav1.ConditionFalse, "pending UpdateMachine hook")
	})

	t.Run("template drift stays not up to date", func(t *testing.T) {
		createMachine(t, c, "drift", "v1.36.3+k0s.1", "K0sWorkerConfig", nil)
		m := reconcileAsCAPI(t, c, "drift", "v1.36.3+k0s", false)
		h, err := patch.NewHelper(m, c)
		if err != nil {
			t.Fatal(err)
		}
		conditions.Set(m, metav1.Condition{
			Type: clusterapiv1.MachineUpToDateCondition, Status: metav1.ConditionFalse,
			Reason: clusterapiv1.MachineNotUpToDateReason, Message: "* Version v1.36.3+k0s.1, v1.36.3+k0s.2 required",
		})
		if err := h.Patch(t.Context(), m, machineControllerOwnedConditions); err != nil {
			t.Fatal(err)
		}
		expectUpToDate(t, getMachine(t, c, "drift"), metav1.ConditionFalse, "")
	})

	t.Run("other versions and bootstrap providers are left alone", func(t *testing.T) {
		createMachine(t, c, "eks", "v1.36.3", "K0sWorkerConfig", nil)
		expectUpToDate(t, reconcileAsCAPI(t, c, "eks", "v1.36.3-eks-cb19647", false), metav1.ConditionFalse, "non-k0s kubelet")

		createMachine(t, c, "malformed", "v1.36.3+k0s.1x", "K0sWorkerConfig", nil)
		expectUpToDate(t, reconcileAsCAPI(t, c, "malformed", "v1.36.3+k0s", false), metav1.ConditionFalse, "malformed build")

		createMachine(t, c, "kubeadm", "v1.36.3+k0s.1", "KubeadmConfig", nil)
		expectUpToDate(t, reconcileAsCAPI(t, c, "kubeadm", "v1.36.3+k0s", false), metav1.ConditionFalse, "other bootstrap provider")
	})

	t.Run("in-place lifecycle of a corrected worker", func(t *testing.T) {
		createMachine(t, c, "lifecycle", "v1.36.3+k0s.1", "K0sWorkerConfig", nil)
		expectUpToDate(t, reconcileAsCAPI(t, c, "lifecycle", "v1.36.3+k0s", false), metav1.ConditionTrue, "before")

		updateMachine(t, c, "lifecycle", func(m *clusterapiv1.Machine) {
			m.Annotations = map[string]string{clusterapiv1.UpdateInProgressAnnotation: ""}
			m.Spec.Version = "v1.36.3+k0s.2"
		})
		expectUpToDate(t, reconcileAsCAPI(t, c, "lifecycle", "v1.36.3+k0s", true), metav1.ConditionFalse, "while updating")

		updateMachine(t, c, "lifecycle", func(m *clusterapiv1.Machine) { m.Annotations = nil })
		expectUpToDate(t, reconcileAsCAPI(t, c, "lifecycle", "v1.36.3+k0s", false), metav1.ConditionTrue, "k0s build updated in place")

		updateMachine(t, c, "lifecycle", func(m *clusterapiv1.Machine) { m.Spec.Version = "v1.36.4+k0s.0" })
		expectUpToDate(t, reconcileAsCAPI(t, c, "lifecycle", "v1.36.3+k0s", false), metav1.ConditionFalse, "kubelet still on the old version")
		expectUpToDate(t, reconcileAsCAPI(t, c, "lifecycle", "v1.36.4+k0s", false), metav1.ConditionTrue, "kubelet on the new version")
	})

	t.Run("pre-existing stuck worker needs an empty status patch", func(t *testing.T) {
		m := reconcileAsCAPI(t, c, "pre", "v1.36.3+k0s", false)
		expectUpToDate(t, m, metav1.ConditionFalse, "capi has nothing to write")

		if err := c.Status().Patch(t.Context(), m, client.RawPatch(types.MergePatchType, []byte("{}"))); err != nil {
			t.Fatal(err)
		}
		m = getMachine(t, c, "pre")
		expectUpToDate(t, m, metav1.ConditionTrue, "after the nudge")

		rv := m.ResourceVersion
		if m = reconcileAsCAPI(t, c, "pre", "v1.36.3+k0s", false); m.ResourceVersion != rv {
			t.Fatalf("capi reconcile after the nudge changed the Machine: resourceVersion %s -> %s", rv, m.ResourceVersion)
		}
	})
}

func startEnv(t *testing.T) client.Client {
	t.Helper()

	crd, err := machineCRDPath()
	if err != nil {
		t.Fatal(err)
	}

	env := &envtest.Environment{CRDDirectoryPaths: []string{crd}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	})

	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := dc.ServerResourcesForGroupVersion("admissionregistration.k8s.io/v1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(resources.APIResources, func(r metav1.APIResource) bool { return r.Name == "mutatingadmissionpolicies" }) {
		t.Skip("the envtest kube-apiserver does not serve admissionregistration.k8s.io/v1 MutatingAdmissionPolicy")
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := clusterapiv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// machineCRDPath returns the Machine CRD downloaded by make external-crd for the CAPI version in go.mod.
func machineCRDPath() (string, error) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("no build info")
	}
	idx := slices.IndexFunc(bi.Deps, func(m *debug.Module) bool { return m.Path == "sigs.k8s.io/cluster-api" })
	if idx < 0 {
		return "", errors.New("sigs.k8s.io/cluster-api is not a dependency")
	}
	mod := bi.Deps[idx]
	if mod.Replace != nil {
		mod = mod.Replace
	}

	path := filepath.Join(repoRoot, "bin", "crd", fmt.Sprintf("cluster.x-k8s.io_machines-%s.yaml", mod.Version))
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("machine CRD for cluster-api %s not found, run make external-crd: %w", mod.Version, err)
	}
	return path, nil
}

func installPolicy(t *testing.T, c client.Client) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(repoRoot, policyFile))
	if err != nil {
		t.Fatal(err)
	}

	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		u := new(unstructured.Unstructured)
		if err := dec.Decode(&u.Object); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			t.Fatalf("decode %s: %v", policyFile, err)
		}
		if len(u.Object) == 0 {
			continue
		}
		if err := c.Create(t.Context(), u); err != nil {
			t.Fatalf("create %s %s: %v", u.GetKind(), u.GetName(), err)
		}
	}
}

// waitForPolicy polls until the apiserver applies the freshly created policy.
func waitForPolicy(t *testing.T, c client.Client) {
	t.Helper()

	createMachine(t, c, "probe", "v1.36.3+k0s.1", "K0sWorkerConfig", nil)
	deadline := time.Now().Add(time.Minute)
	for {
		m := getMachine(t, c, "probe")
		if err := writeCAPIStatus(t.Context(), c, m, "v1.36.3+k0s", false); err == nil {
			if up := conditions.Get(getMachine(t, c, "probe"), clusterapiv1.MachineUpToDateCondition); up != nil && up.Status == metav1.ConditionTrue {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the policy did not take effect within a minute")
		}
		// force a write on the next round
		_ = c.Status().Patch(t.Context(), m, client.RawPatch(types.MergePatchType, []byte("{}")))
		time.Sleep(time.Second)
	}
}

func createMachine(t *testing.T, c client.Client, name, version, bootstrapKind string, annotations map[string]string) {
	t.Helper()

	m := &clusterapiv1.Machine{
		Name:        name,
		Namespace:   namespace,
		Annotations: annotations,
		Labels: map[string]string{
			clusterapiv1.ClusterNameLabel:           "c",
			clusterapiv1.MachineDeploymentNameLabel: "c-md",
		},
		Spec: clusterapiv1.MachineSpec{
			ClusterName: "c",
			Version:     version,
			Bootstrap: clusterapiv1.Bootstrap{ConfigRef: clusterapiv1.ContractVersionedObjectReference{
				APIGroup: "bootstrap.cluster.x-k8s.io", Kind: bootstrapKind, Name: name,
			}},
			InfrastructureRef: clusterapiv1.ContractVersionedObjectReference{
				APIGroup: "infrastructure.cluster.x-k8s.io", Kind: "NicoMachine", Name: name,
			},
		},
	}
	if err := c.Create(t.Context(), m); err != nil {
		t.Fatalf("create Machine %s: %v", name, err)
	}
}

func getMachine(t *testing.T, c client.Client, name string) *clusterapiv1.Machine {
	t.Helper()

	m := new(clusterapiv1.Machine)
	if err := c.Get(t.Context(), client.ObjectKey{Namespace: namespace, Name: name}, m); err != nil {
		t.Fatalf("get Machine %s: %v", name, err)
	}
	return m
}

func updateMachine(t *testing.T, c client.Client, name string, mutate func(*clusterapiv1.Machine)) {
	t.Helper()

	m := getMachine(t, c, name)
	orig := m.DeepCopy()
	mutate(m)
	if err := c.Patch(t.Context(), m, client.MergeFrom(orig)); err != nil {
		t.Fatalf("patch Machine %s: %v", name, err)
	}
}

// reconcileAsCAPI writes the Machine status the way the CAPI v1.14 Machine controller
// computes it for a worker whose node reports the given kubelet version.
func reconcileAsCAPI(t *testing.T, c client.Client, name, kubelet string, updating bool) *clusterapiv1.Machine {
	t.Helper()

	if err := writeCAPIStatus(t.Context(), c, getMachine(t, c, name), kubelet, updating); err != nil {
		t.Fatalf("write status of Machine %s: %v", name, err)
	}
	return getMachine(t, c, name)
}

func writeCAPIStatus(ctx context.Context, c client.Client, m *clusterapiv1.Machine, kubelet string, updating bool) error {
	h, err := patch.NewHelper(m, c)
	if err != nil {
		return err
	}

	m.Status.NodeInfo = &corev1.NodeSystemInfo{
		MachineID: "id", SystemUUID: "id", BootID: "id", KernelVersion: "6.8.0", OSImage: "Ubuntu",
		ContainerRuntimeVersion: "containerd://2.3.3", KubeletVersion: kubelet,
		OperatingSystem: "linux", Architecture: "amd64",
	}

	switch {
	case updating:
		conditions.Set(m, metav1.Condition{
			Type: clusterapiv1.MachineUpdatingCondition, Status: metav1.ConditionTrue,
			Reason: clusterapiv1.MachineInPlaceUpdatingReason, Message: "In-place update in progress",
		})
		conditions.Set(m, metav1.Condition{
			Type: clusterapiv1.MachineUpToDateCondition, Status: metav1.ConditionFalse,
			Reason: clusterapiv1.MachineUpToDateUpdatingReason, Message: "* In-place update in progress",
		})
	case kubelet != m.Spec.Version:
		conditions.Set(m, metav1.Condition{Type: clusterapiv1.MachineUpdatingCondition, Status: metav1.ConditionFalse, Reason: clusterapiv1.MachineNotUpdatingReason})
		conditions.Set(m, metav1.Condition{
			Type: clusterapiv1.MachineUpToDateCondition, Status: metav1.ConditionFalse, Reason: clusterapiv1.MachineUpToDateUpdatingReason,
			Message: fmt.Sprintf("* Node.status.nodeInfo.kubeletVersion %s, %s required", kubelet, m.Spec.Version),
		})
	default:
		conditions.Set(m, metav1.Condition{Type: clusterapiv1.MachineUpdatingCondition, Status: metav1.ConditionFalse, Reason: clusterapiv1.MachineNotUpdatingReason})
		conditions.Set(m, metav1.Condition{Type: clusterapiv1.MachineUpToDateCondition, Status: metav1.ConditionTrue, Reason: clusterapiv1.MachineUpToDateReason})
	}

	return h.Patch(ctx, m, machineControllerOwnedConditions)
}

func expectUpToDate(t *testing.T, m *clusterapiv1.Machine, want metav1.ConditionStatus, note string) {
	t.Helper()

	got := conditions.Get(m, clusterapiv1.MachineUpToDateCondition)
	if got == nil || got.Status != want {
		t.Fatalf("Machine %s (%s): UpToDate = %+v, want status %s", m.Name, note, got, want)
	}
}
