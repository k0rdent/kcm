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

package controller

// TODO(KLUDGE): DELETE THIS FILE together with clusterdeployment_k0s_uptodate.go once
// https://github.com/kubernetes-sigs/cluster-api/issues/14086 is fixed.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	runtimev1 "sigs.k8s.io/cluster-api/api/runtime/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func newStuckK0sMachine(t *testing.T, name string, mutators ...func(*clusterapiv1.Machine)) *clusterapiv1.Machine {
	t.Helper()

	m := &clusterapiv1.Machine{
		Name:      name,
		Namespace: pollerTestNamespace,
		Labels: map[string]string{
			clusterapiv1.ClusterNameLabel:           "a",
			clusterapiv1.MachineDeploymentNameLabel: "a-md",
		},
		Spec: clusterapiv1.MachineSpec{
			ClusterName: "a",
			Version:     "v1.36.3+k0s.1",
			Bootstrap: clusterapiv1.Bootstrap{ConfigRef: clusterapiv1.ContractVersionedObjectReference{
				APIGroup: "bootstrap.cluster.x-k8s.io", Kind: "K0sWorkerConfig", Name: name,
			}},
		},
		Status: clusterapiv1.MachineStatus{
			NodeInfo: &corev1.NodeSystemInfo{KubeletVersion: "v1.36.3+k0s"},
			Conditions: []metav1.Condition{
				{Type: clusterapiv1.MachineUpdatingCondition, Status: metav1.ConditionFalse, Reason: clusterapiv1.MachineNotUpdatingReason},
				{Type: clusterapiv1.MachineUpToDateCondition, Status: metav1.ConditionFalse, Reason: clusterapiv1.MachineUpToDateUpdatingReason},
			},
		},
	}
	for _, mut := range mutators {
		mut(m)
	}
	return m
}

func setMachineCondition(c metav1.Condition) func(*clusterapiv1.Machine) {
	return func(m *clusterapiv1.Machine) {
		for i := range m.Status.Conditions {
			if m.Status.Conditions[i].Type == c.Type {
				m.Status.Conditions[i] = c
				return
			}
		}
		m.Status.Conditions = append(m.Status.Conditions, c)
	}
}

func Test_isStuckK0sWorker(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		mutators []func(*clusterapiv1.Machine)
		want     bool
	}{
		"k0s.N build only": {want: true},
		"bare spec version": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) { m.Spec.Version = "v1.36.3" }},
			want:     true,
		},
		"kubelet form spec version": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) { m.Spec.Version = "v1.36.3+k0s" }},
			want:     true,
		},
		"malformed k0s build": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) { m.Spec.Version = "v1.36.3+k0s.1x" }},
		},
		"empty k0s build": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) { m.Spec.Version = "v1.36.3+k0s." }},
		},
		"pre-release kubelet": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) {
				m.Spec.Version = "v1.37.0-rc.1+k0s.0"
				m.Status.NodeInfo.KubeletVersion = "v1.37.0-rc.1+k0s"
			}},
		},
		"kubernetes version differs": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) { m.Spec.Version = "v1.36.4+k0s.0" }},
		},
		"empty spec version": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) { m.Spec.Version = "" }},
		},
		"not a k0s kubelet": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) { m.Status.NodeInfo.KubeletVersion = "v1.36.3-eks-cb19647" }},
		},
		"no node info": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) { m.Status.NodeInfo = nil }},
		},
		"other bootstrap provider": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) { m.Spec.Bootstrap.ConfigRef.Kind = "KubeadmConfig" }},
		},
		"not a machinedeployment worker": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) {
				delete(m.Labels, clusterapiv1.MachineDeploymentNameLabel)
			}},
		},
		"in-place update in progress": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) {
				m.Annotations = map[string]string{clusterapiv1.UpdateInProgressAnnotation: ""}
			}},
		},
		"UpdateMachine hook pending": {
			mutators: []func(*clusterapiv1.Machine){func(m *clusterapiv1.Machine) {
				m.Annotations = map[string]string{runtimev1.PendingHooksAnnotation: "UpdateMachine"}
			}},
		},
		"updating": {
			mutators: []func(*clusterapiv1.Machine){setMachineCondition(metav1.Condition{
				Type: clusterapiv1.MachineUpdatingCondition, Status: metav1.ConditionTrue, Reason: clusterapiv1.MachineInPlaceUpdatingReason,
			})},
		},
		"already up to date": {
			mutators: []func(*clusterapiv1.Machine){setMachineCondition(metav1.Condition{
				Type: clusterapiv1.MachineUpToDateCondition, Status: metav1.ConditionTrue, Reason: clusterapiv1.MachineUpToDateReason,
			})},
		},
		"template drift": {
			mutators: []func(*clusterapiv1.Machine){setMachineCondition(metav1.Condition{
				Type: clusterapiv1.MachineUpToDateCondition, Status: metav1.ConditionFalse, Reason: clusterapiv1.MachineNotUpToDateReason,
			})},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := isStuckK0sWorker(newStuckK0sMachine(t, "m", tc.mutators...)); got != tc.want {
				t.Fatalf("isStuckK0sWorker() = %t, want %t", got, tc.want)
			}
		})
	}
}

// statusPatchRecorder counts status patches per Machine and drops them, as a real
// apiserver would for an empty patch the admission policy declined
type statusPatchRecorder struct {
	patched map[string]int
	failErr error
	mu      sync.Mutex
}

func (r *statusPatchRecorder) funcs() interceptor.Funcs {
	return interceptor.Funcs{
		SubResourcePatch: func(_ context.Context, _ client.Client, subResource string, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
			if subResource != "status" {
				return nil
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			r.patched[obj.GetName()]++
			return r.failErr
		},
	}
}

func (r *statusPatchRecorder) setFailErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failErr = err
}

func (r *statusPatchRecorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.patched[name]
}

func Test_k0sWorkerUpToDateNudger_nudge(t *testing.T) {
	t.Parallel()

	workersNotUpToDate := []metav1.Condition{{
		Type: clusterapiv1.ClusterWorkerMachinesUpToDateCondition, Status: metav1.ConditionFalse, Reason: clusterapiv1.ClusterWorkerMachinesNotUpToDateReason,
	}}
	workersUpToDate := []metav1.Condition{{
		Type: clusterapiv1.ClusterWorkerMachinesUpToDateCondition, Status: metav1.ConditionTrue, Reason: clusterapiv1.ClusterWorkerMachinesUpToDateReason,
	}}
	policy := &admissionregistrationv1.MutatingAdmissionPolicy{Name: k0sWorkerUpToDatePolicyName}
	pausedCluster := newCAPICluster(t, "a", workersNotUpToDate)
	pausedCluster.Spec.Paused = new(true)

	tests := map[string]struct {
		cd          *kcmv1.ClusterDeployment
		wantPatched map[string]int
		objects     []client.Object
	}{
		"paused capi cluster": {
			cd:      newClusterDeployment(t, "a"),
			objects: []client.Object{newCredential(t, "a", ""), pausedCluster, policy, newStuckK0sMachine(t, "stuck")},
		},
		"paused machine": {
			cd: newClusterDeployment(t, "a"),
			objects: []client.Object{
				newCredential(t, "a", ""), newCAPICluster(t, "a", workersNotUpToDate), policy,
				newStuckK0sMachine(t, "stuck", func(m *clusterapiv1.Machine) { m.Annotations = map[string]string{clusterapiv1.PausedAnnotation: ""} }),
			},
		},
		"stuck worker is nudged, others are not": {
			cd: newClusterDeployment(t, "a"),
			objects: []client.Object{
				newCredential(t, "a", ""), newCAPICluster(t, "a", workersNotUpToDate), policy,
				newStuckK0sMachine(t, "stuck"),
				newStuckK0sMachine(t, "drift", func(m *clusterapiv1.Machine) { m.Spec.Version = "v1.36.4+k0s.0" }),
				newStuckK0sMachine(t, "other-cluster", func(m *clusterapiv1.Machine) { m.Labels[clusterapiv1.ClusterNameLabel] = "b" }),
			},
			wantPatched: map[string]int{"stuck": 1},
		},
		"policy not installed": {
			cd:      newClusterDeployment(t, "a"),
			objects: []client.Object{newCredential(t, "a", ""), newCAPICluster(t, "a", workersNotUpToDate), newStuckK0sMachine(t, "stuck")},
		},
		"workers already up to date": {
			cd:      newClusterDeployment(t, "a"),
			objects: []client.Object{newCredential(t, "a", ""), newCAPICluster(t, "a", workersUpToDate), policy, newStuckK0sMachine(t, "stuck")},
		},
		"no capi cluster": {
			cd:      newClusterDeployment(t, "a"),
			objects: []client.Object{newCredential(t, "a", ""), policy, newStuckK0sMachine(t, "stuck")},
		},
		"missing credential": {
			cd:      newClusterDeployment(t, "a"),
			objects: []client.Object{newCAPICluster(t, "a", workersNotUpToDate), policy, newStuckK0sMachine(t, "stuck")},
		},
		"dry-run cluster deployment": {
			cd:      newClusterDeployment(t, "a", withDryRun()),
			objects: []client.Object{newCredential(t, "a", ""), newCAPICluster(t, "a", workersNotUpToDate), policy, newStuckK0sMachine(t, "stuck")},
		},
		"deleting cluster deployment": {
			cd:      newClusterDeployment(t, "a", withDeletionTimestamp()),
			objects: []client.Object{newCredential(t, "a", ""), newCAPICluster(t, "a", workersNotUpToDate), policy, newStuckK0sMachine(t, "stuck")},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// the fake client writes to the objects it is built with, and the cases share some
			objs := make([]client.Object, 0, len(tc.objects)+1)
			for _, o := range append(tc.objects, tc.cd) {
				cp, ok := o.DeepCopyObject().(client.Object)
				if !ok {
					t.Fatalf("%T is not a client.Object", o)
				}
				objs = append(objs, cp)
			}

			rec := &statusPatchRecorder{patched: make(map[string]int)}
			cl := fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithObjects(objs...).
				WithStatusSubresource(&clusterapiv1.Machine{}).
				WithInterceptorFuncs(rec.funcs()).
				Build()

			n := newK0sWorkerUpToDateNudger(cl, cl, "kcm-system")
			n.nudge(t.Context())

			for _, name := range []string{"stuck", "drift", "other-cluster"} {
				if got := rec.count(name); got != tc.wantPatched[name] {
					t.Errorf("Machine %s patched %d times, want %d", name, got, tc.wantPatched[name])
				}
			}
		})
	}
}

func Test_k0sWorkerUpToDateNudger_retries(t *testing.T) {
	t.Parallel()

	rec := &statusPatchRecorder{patched: make(map[string]int)}
	machine := newStuckK0sMachine(t, "stuck")
	cl := fake.NewClientBuilder().
		WithScheme(testscheme.Scheme).
		WithObjects(
			newClusterDeployment(t, "a"), newCredential(t, "a", ""),
			newCAPICluster(t, "a", []metav1.Condition{{
				Type: clusterapiv1.ClusterWorkerMachinesUpToDateCondition, Status: metav1.ConditionFalse, Reason: clusterapiv1.ClusterWorkerMachinesNotUpToDateReason,
			}}),
			&admissionregistrationv1.MutatingAdmissionPolicy{Name: k0sWorkerUpToDatePolicyName},
			machine,
		).
		WithStatusSubresource(&clusterapiv1.Machine{}).
		WithInterceptorFuncs(rec.funcs()).
		Build()

	n := newK0sWorkerUpToDateNudger(cl, cl, "kcm-system")
	now := time.Now()
	n.now = func() time.Time { return now }

	// a failed patch does not count as a nudge
	rec.setFailErr(errors.New("boom"))
	n.nudge(t.Context())
	rec.setFailErr(nil)
	if got := rec.count("stuck"); got != 1 {
		t.Fatalf("Machine patched %d times after a failed attempt, want 1", got)
	}

	n.nudge(t.Context())
	now = now.Add(k0sWorkerUpToDateRenudgeAfter - time.Second)
	n.nudge(t.Context())
	if got := rec.count("stuck"); got != 2 {
		t.Fatalf("unchanged Machine patched %d times in total, want 2 (retry after the failure, then nothing)", got)
	}

	// the patch was declined; once the memo expires, it is worth another try
	now = now.Add(time.Second)
	n.nudge(t.Context())
	if got := rec.count("stuck"); got != 3 {
		t.Fatalf("unchanged Machine patched %d times in total after the memo expired, want 3", got)
	}

	// and once the Machine changes
	if err := cl.Get(t.Context(), client.ObjectKeyFromObject(machine), machine); err != nil {
		t.Fatalf("get Machine: %v", err)
	}
	machine.Annotations = map[string]string{"touched": "true"}
	if err := cl.Update(t.Context(), machine); err != nil {
		t.Fatalf("update Machine: %v", err)
	}

	n.nudge(t.Context())
	if got := rec.count("stuck"); got != 4 {
		t.Fatalf("changed Machine patched %d times in total, want 4", got)
	}
}

func Test_k0sWorkerUpToDateNudger_regions(t *testing.T) {
	t.Parallel()

	workersNotUpToDate := []metav1.Condition{{
		Type: clusterapiv1.ClusterWorkerMachinesUpToDateCondition, Status: metav1.ConditionFalse, Reason: clusterapiv1.ClusterWorkerMachinesNotUpToDateReason,
	}}
	inClusterB := func(m *clusterapiv1.Machine) { m.Labels[clusterapiv1.ClusterNameLabel] = "b" }

	tests := map[string]struct {
		regionalErr error
		wantPatched map[string]int
		wantCalls   int
		paused      bool
	}{
		"nudged through the regional client, resolved once per run": {
			wantCalls:   1,
			wantPatched: map[string]int{"stuck-a": 1, "stuck-b": 1},
		},
		"failing region is resolved once per run": {
			regionalErr: errors.New("boom"),
			wantCalls:   1,
		},
		"paused region is left alone": {
			paused: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			region := &kcmv1.Region{Name: "rgn"}
			if tc.paused {
				region.Annotations = map[string]string{kcmv1.RegionPauseAnnotation: "true"}
			}

			// the management cluster holds the same CAPI objects, they must stay untouched
			mgmtRec := &statusPatchRecorder{patched: make(map[string]int)}
			mgmt := fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithObjects(
					region,
					newClusterDeployment(t, "a"), newCredential(t, "a", "rgn"),
					newClusterDeployment(t, "b"), newCredential(t, "b", "rgn"),
					newCAPICluster(t, "a", workersNotUpToDate), newStuckK0sMachine(t, "stuck-a"),
					&admissionregistrationv1.MutatingAdmissionPolicy{Name: k0sWorkerUpToDatePolicyName},
				).
				WithStatusSubresource(&clusterapiv1.Machine{}).
				WithInterceptorFuncs(mgmtRec.funcs()).
				Build()

			rgnRec := &statusPatchRecorder{patched: make(map[string]int)}
			rgn := fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithObjects(
					newCAPICluster(t, "a", workersNotUpToDate), newCAPICluster(t, "b", workersNotUpToDate),
					newStuckK0sMachine(t, "stuck-a"), newStuckK0sMachine(t, "stuck-b", inClusterB),
					&admissionregistrationv1.MutatingAdmissionPolicy{Name: k0sWorkerUpToDatePolicyName},
				).
				WithStatusSubresource(&clusterapiv1.Machine{}).
				WithInterceptorFuncs(rgnRec.funcs()).
				Build()

			n := newK0sWorkerUpToDateNudger(mgmt, mgmt, "kcm-system")
			calls := 0
			n.regionalClient = func(context.Context, *kcmv1.Region) (client.Client, error) {
				calls++
				return rgn, tc.regionalErr
			}
			n.nudge(t.Context())

			if calls != tc.wantCalls {
				t.Errorf("regional client built %d times, want %d", calls, tc.wantCalls)
			}
			for _, name := range []string{"stuck-a", "stuck-b"} {
				if got := rgnRec.count(name); got != tc.wantPatched[name] {
					t.Errorf("Machine %s patched %d times in the region, want %d", name, got, tc.wantPatched[name])
				}
				if got := mgmtRec.count(name); got != 0 {
					t.Errorf("Machine %s patched %d times in the management cluster, want 0", name, got)
				}
			}
		})
	}
}

func Test_k0sWorkerUpToDateNudger_unresponsiveRegion(t *testing.T) {
	t.Parallel()

	workersNotUpToDate := []metav1.Condition{{
		Type: clusterapiv1.ClusterWorkerMachinesUpToDateCondition, Status: metav1.ConditionFalse, Reason: clusterapiv1.ClusterWorkerMachinesNotUpToDateReason,
	}}

	for _, hangOn := range []string{"policy lookup", "cluster list"} {
		t.Run(hangOn, func(t *testing.T) {
			t.Parallel()

			mgmt := fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithObjects(
					&kcmv1.Region{Name: "slow"}, &kcmv1.Region{Name: "healthy"},
					newClusterDeployment(t, "a"), newCredential(t, "a", "slow"),
					newClusterDeployment(t, "b"), newCredential(t, "b", "slow"),
					newClusterDeployment(t, "c"), newCredential(t, "c", "healthy"),
				).
				Build()

			// the slow region accepts requests but never answers them
			hung := 0
			hang := func(ctx context.Context) error {
				hung++
				<-ctx.Done()
				return ctx.Err()
			}
			slow := fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithObjects(&admissionregistrationv1.MutatingAdmissionPolicy{Name: k0sWorkerUpToDatePolicyName}).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if hangOn == "policy lookup" {
							return hang(ctx)
						}
						return c.Get(ctx, key, obj, opts...)
					},
					List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if hangOn == "cluster list" {
							return hang(ctx)
						}
						return c.List(ctx, list, opts...)
					},
				}).
				Build()

			rec := &statusPatchRecorder{patched: make(map[string]int)}
			healthy := fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithObjects(
					&admissionregistrationv1.MutatingAdmissionPolicy{Name: k0sWorkerUpToDatePolicyName},
					newCAPICluster(t, "c", workersNotUpToDate),
					newStuckK0sMachine(t, "stuck-c", func(m *clusterapiv1.Machine) { m.Labels[clusterapiv1.ClusterNameLabel] = "c" }),
				).
				WithStatusSubresource(&clusterapiv1.Machine{}).
				WithInterceptorFuncs(rec.funcs()).
				Build()

			n := newK0sWorkerUpToDateNudger(mgmt, mgmt, "kcm-system")
			n.stepTimeout = 50 * time.Millisecond
			n.regionalClient = func(_ context.Context, rgn *kcmv1.Region) (client.Client, error) {
				if rgn.Name == "slow" {
					return slow, nil
				}
				return healthy, nil
			}
			n.nudge(t.Context())

			if hung != 1 {
				t.Errorf("unresponsive region held up the run %d times, want 1", hung)
			}
			if got := rec.count("stuck-c"); got != 1 {
				t.Errorf("Machine in the healthy region patched %d times, want 1", got)
			}
		})
	}
}
