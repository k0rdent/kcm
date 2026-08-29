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

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	conditionsutil "github.com/K0rdent/kcm/internal/util/conditions"
)

const heldTestNS = "k0rdent-apis"

func heldTestMCS(services ...kcmv1.Service) *kcmv1.MultiClusterService {
	return &kcmv1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{Name: "kacs-mothership-auth-api", Generation: 6},
		Spec: kcmv1.MultiClusterServiceSpec{
			ServiceSpec: kcmv1.ServiceSpec{Services: services},
		},
	}
}

func heldTestServiceSet(cluster string, services ...kcmv1.ServiceWithValues) kcmv1.ServiceSet {
	return kcmv1.ServiceSet{
		ObjectMeta: metav1.ObjectMeta{Name: "management-" + cluster, Namespace: "kcm-system"},
		Spec:       kcmv1.ServiceSetSpec{Cluster: cluster, Services: services},
	}
}

func Test_heldServices(t *testing.T) {
	t.Parallel()

	svc := func(name, template string) kcmv1.Service {
		return kcmv1.Service{Name: name, Namespace: heldTestNS, Template: template}
	}
	deployed := func(name, template string) kcmv1.ServiceWithValues {
		return kcmv1.ServiceWithValues{Name: name, Namespace: heldTestNS, Template: template}
	}

	tests := map[string]struct {
		mcs         *kcmv1.MultiClusterService
		serviceSets []kcmv1.ServiceSet
		wantHeld    []heldService
		wantDesired int
	}{
		"everything propagated": {
			mcs: heldTestMCS(svc("a", "cat-1.1.0"), svc("b", "auth-1.1.0")),
			serviceSets: []kcmv1.ServiceSet{
				heldTestServiceSet("", deployed("a", "cat-1.1.0"), deployed("b", "auth-1.1.0")),
			},
			wantDesired: 2,
		},
		// The KSM-207 shape: the dependency-free service advanced, the dependents did not.
		"dependents held at their old templates": {
			mcs: heldTestMCS(svc("a", "cat-1.1.0"), svc("b", "auth-1.1.0"), svc("c", "cat-1.1.0")),
			serviceSets: []kcmv1.ServiceSet{
				heldTestServiceSet("", deployed("a", "cat-1.1.0"), deployed("b", "auth-1.0.1"), deployed("c", "cat-1.0.0")),
			},
			wantHeld: []heldService{
				{key: client.ObjectKey{Namespace: heldTestNS, Name: "b"}, want: "auth-1.1.0", got: "auth-1.0.1", clusters: 1},
				{key: client.ObjectKey{Namespace: heldTestNS, Name: "c"}, want: "cat-1.1.0", got: "cat-1.0.0", clusters: 1},
			},
			wantDesired: 3,
		},
		"service not present in the ServiceSet spec at all": {
			mcs: heldTestMCS(svc("a", "cat-1.1.0"), svc("b", "auth-1.1.0")),
			serviceSets: []kcmv1.ServiceSet{
				heldTestServiceSet("", deployed("a", "cat-1.1.0")),
			},
			wantHeld: []heldService{
				{key: client.ObjectKey{Namespace: heldTestNS, Name: "b"}, want: "auth-1.1.0", got: "", clusters: 1},
			},
			wantDesired: 2,
		},
		"disabled services are never propagated and must not be reported as held": {
			mcs: heldTestMCS(
				svc("a", "cat-1.1.0"),
				kcmv1.Service{Name: "b", Namespace: heldTestNS, Template: "auth-1.1.0", Disable: true},
			),
			serviceSets: []kcmv1.ServiceSet{heldTestServiceSet("", deployed("a", "cat-1.1.0"))},
			wantDesired: 1,
		},
		"a service held on several clusters is reported once with a cluster count": {
			mcs: heldTestMCS(svc("a", "cat-1.1.0")),
			serviceSets: []kcmv1.ServiceSet{
				heldTestServiceSet("cd1", deployed("a", "cat-1.0.0")),
				heldTestServiceSet("cd2", deployed("a", "cat-1.0.0")),
				heldTestServiceSet("cd3", deployed("a", "cat-1.1.0")),
			},
			wantHeld: []heldService{
				{key: client.ObjectKey{Namespace: heldTestNS, Name: "a"}, want: "cat-1.1.0", got: "cat-1.0.0", clusters: 2},
			},
			wantDesired: 1,
		},
		"ServiceSets being deleted are not propagation targets": {
			mcs: heldTestMCS(svc("a", "cat-1.1.0")),
			serviceSets: []kcmv1.ServiceSet{
				func() kcmv1.ServiceSet {
					ss := heldTestServiceSet("cd1", deployed("a", "cat-1.0.0"))
					ss.DeletionTimestamp = new(metav1.Now())
					ss.Finalizers = []string{kcmv1.ServiceSetFinalizer}
					return ss
				}(),
			},
			wantDesired: 1,
		},
		// Scoped to propagation: a cluster with no ServiceSet is ClusterInReadyState's business.
		"no ServiceSets yet is not a hold": {
			mcs:         heldTestMCS(svc("a", "cat-1.1.0")),
			wantDesired: 1,
		},
		"no services declared": {
			mcs: heldTestMCS(),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gotHeld, gotDesired := heldServices(tc.mcs.Spec.ServiceSpec.Services, tc.serviceSets)
			assert.Equal(t, tc.wantDesired, gotDesired)
			assert.Equal(t, tc.wantHeld, gotHeld)
		})
	}
}

// Order must follow the MultiClusterService spec rather than map iteration, so the
// condition message does not churn between otherwise identical reconciles.
func Test_heldServices_DeterministicOrder(t *testing.T) {
	t.Parallel()

	mcs := heldTestMCS(
		kcmv1.Service{Name: "z", Namespace: heldTestNS, Template: "t-2"},
		kcmv1.Service{Name: "m", Namespace: heldTestNS, Template: "t-2"},
		kcmv1.Service{Name: "a", Namespace: heldTestNS, Template: "t-2"},
	)
	serviceSets := []kcmv1.ServiceSet{heldTestServiceSet("",
		kcmv1.ServiceWithValues{Name: "z", Namespace: heldTestNS, Template: "t-1"},
		kcmv1.ServiceWithValues{Name: "m", Namespace: heldTestNS, Template: "t-1"},
		kcmv1.ServiceWithValues{Name: "a", Namespace: heldTestNS, Template: "t-1"},
	)}

	for range 20 {
		held, _ := heldServices(mcs.Spec.ServiceSpec.Services, serviceSets)
		require.Len(t, held, 3)
		assert.Equal(t, []string{"z", "m", "a"}, []string{held[0].key.Name, held[1].key.Name, held[2].key.Name})
	}
}

func Test_heldServicesMessage(t *testing.T) {
	t.Parallel()

	held := func(name, want, got string, clusters int) heldService {
		return heldService{key: client.ObjectKey{Namespace: heldTestNS, Name: name}, want: want, got: got, clusters: clusters}
	}

	t.Run("names the service, what it runs and what it should run", func(t *testing.T) {
		t.Parallel()

		msg := heldServicesMessage([]heldService{held("b", "auth-1.1.0", "auth-1.0.1", 1)}, 3)
		assert.Equal(t,
			"1 of 3 service(s) not yet propagated to the owned ServiceSet(s), waiting on service dependencies: "+
				"k0rdent-apis/b has auth-1.0.1, wants auth-1.1.0", msg)
	})

	t.Run("a service absent from the spec is called out as such", func(t *testing.T) {
		t.Parallel()

		msg := heldServicesMessage([]heldService{held("b", "auth-1.1.0", "", 1)}, 2)
		assert.Contains(t, msg, "k0rdent-apis/b has <not present>, wants auth-1.1.0")
	})

	t.Run("multi-cluster holds report the cluster count", func(t *testing.T) {
		t.Parallel()

		msg := heldServicesMessage([]heldService{held("b", "auth-1.1.0", "auth-1.0.1", 4)}, 2)
		assert.Contains(t, msg, "(on 4 clusters)")
	})

	t.Run("only the first few are named, the rest are counted", func(t *testing.T) {
		t.Parallel()

		var all []heldService
		for _, n := range []string{"s1", "s2", "s3", "s4", "s5"} {
			all = append(all, held(n, "t-2", "t-1", 1))
		}
		msg := heldServicesMessage(all, 5)
		assert.Contains(t, msg, "5 of 5 service(s)")
		assert.Contains(t, msg, "s3")
		assert.NotContains(t, msg, "s4")
		assert.Contains(t, msg, "; and 2 more")
	})

	t.Run("stays bounded when names are pathologically long", func(t *testing.T) {
		t.Parallel()

		long := strings.Repeat("n", 253)
		var all []heldService
		for range 5 {
			all = append(all, held(long, long, long, 1))
		}
		msg := heldServicesMessage(all, 5)
		assert.LessOrEqual(t, len(msg), maxHeldServicesMessageBytes+200,
			"message is persisted on mcs.Status and must not grow unbounded")
		assert.Contains(t, msg, "bytes omitted")
	})
}

func Test_setServiceDependencyReadyCondition(t *testing.T) {
	t.Parallel()

	mcs := heldTestMCS(
		kcmv1.Service{Name: "a", Namespace: heldTestNS, Template: "cat-1.1.0"},
		kcmv1.Service{Name: "b", Namespace: heldTestNS, Template: "auth-1.1.0"},
	)
	setCond := func(obj *kcmv1.MultiClusterService, serviceSets []kcmv1.ServiceSet) {
		setServiceDependencyReadyCondition(&obj.Status.Conditions, obj.Generation, obj.Spec.ServiceSpec.Services, serviceSets)
	}
	t.Run("all propagated: ready", func(t *testing.T) {
		t.Parallel()

		obj := mcs.DeepCopy()
		setCond(obj, []kcmv1.ServiceSet{heldTestServiceSet("",
			kcmv1.ServiceWithValues{Name: "a", Namespace: heldTestNS, Template: "cat-1.1.0"},
			kcmv1.ServiceWithValues{Name: "b", Namespace: heldTestNS, Template: "auth-1.1.0"},
		)})

		cond := apimeta.FindStatusCondition(obj.Status.Conditions, kcmv1.ServiceDependencyReadyCondition)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionTrue, cond.Status)
		assert.Equal(t, kcmv1.SucceededReason, cond.Reason)
		assert.Equal(t, obj.Generation, cond.ObservedGeneration)
	})

	// KSM-207: this is the state that used to report Ready=True at the current
	// observedGeneration with nothing anywhere to show a service was stuck.
	t.Run("a dependent held at its old template: not ready, and Ready flips false", func(t *testing.T) {
		t.Parallel()

		obj := mcs.DeepCopy()
		setCond(obj, []kcmv1.ServiceSet{heldTestServiceSet("",
			kcmv1.ServiceWithValues{Name: "a", Namespace: heldTestNS, Template: "cat-1.1.0"},
			kcmv1.ServiceWithValues{Name: "b", Namespace: heldTestNS, Template: "auth-1.0.1"},
		)})

		cond := apimeta.FindStatusCondition(obj.Status.Conditions, kcmv1.ServiceDependencyReadyCondition)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, kcmv1.ServiceDependencyNotReadyReason, cond.Reason)
		assert.Contains(t, cond.Message, "k0rdent-apis/b has auth-1.0.1, wants auth-1.1.0")

		obj.Status.Conditions = conditionsutil.UpdateReadyCondition(
			obj.Status.Conditions, obj.Generation, handleMultiClusterServiceFailedCondition)

		ready := apimeta.FindStatusCondition(obj.Status.Conditions, kcmv1.ReadyCondition)
		require.NotNil(t, ready)
		assert.Equal(t, metav1.ConditionFalse, ready.Status,
			"a silently held service must not leave the MultiClusterService reporting Ready")
	})
}

// The ClusterDeployment path shares the helpers but has a narrowing of its own: the
// ServiceSets it lists are indexed by cluster, so the list also contains ServiceSets owned
// by MultiClusterServices matching that cluster. Those carry a different set of desired
// services and must not be measured against the ClusterDeployment's own spec.
func Test_setServiceDependencyReadyCondition_ClusterDeployment(t *testing.T) {
	t.Parallel()

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "cd", Namespace: "ns", Generation: 4},
		Spec: kcmv1.ClusterDeploymentSpec{
			ServiceSpec: kcmv1.ServiceSpec{Services: []kcmv1.Service{
				{Name: "a", Namespace: heldTestNS, Template: "cat-1.1.0"},
				{Name: "b", Namespace: heldTestNS, Template: "auth-1.1.0"},
			}},
		},
	}

	ownServiceSet := func(services ...kcmv1.ServiceWithValues) kcmv1.ServiceSet {
		ss := heldTestServiceSet("cd", services...)
		ss.Namespace = "ns"
		return ss
	}
	mcsServiceSet := func(services ...kcmv1.ServiceWithValues) kcmv1.ServiceSet {
		ss := ownServiceSet(services...)
		ss.Spec.MultiClusterService = "some-mcs"
		return ss
	}

	setCond := func(obj *kcmv1.ClusterDeployment, serviceSets []kcmv1.ServiceSet) {
		// Mirrors the narrowing updateServices applies before calling through.
		own := make([]kcmv1.ServiceSet, 0, len(serviceSets))
		for _, ss := range serviceSets {
			if ss.Spec.MultiClusterService == "" {
				own = append(own, ss)
			}
		}
		setServiceDependencyReadyCondition(&obj.Status.Conditions, obj.Generation, obj.Spec.ServiceSpec.Services, own)
	}

	t.Run("all propagated into the ClusterDeployment's own ServiceSet: ready", func(t *testing.T) {
		t.Parallel()

		obj := cd.DeepCopy()
		setCond(obj, []kcmv1.ServiceSet{ownServiceSet(
			kcmv1.ServiceWithValues{Name: "a", Namespace: heldTestNS, Template: "cat-1.1.0"},
			kcmv1.ServiceWithValues{Name: "b", Namespace: heldTestNS, Template: "auth-1.1.0"},
		)})

		cond := apimeta.FindStatusCondition(obj.Status.Conditions, kcmv1.ServiceDependencyReadyCondition)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionTrue, cond.Status)
		assert.Equal(t, obj.Generation, cond.ObservedGeneration)
	})

	t.Run("a ServiceSet owned by a MultiClusterService is not measured against the ClusterDeployment spec", func(t *testing.T) {
		t.Parallel()

		obj := cd.DeepCopy()
		setCond(obj, []kcmv1.ServiceSet{
			ownServiceSet(
				kcmv1.ServiceWithValues{Name: "a", Namespace: heldTestNS, Template: "cat-1.1.0"},
				kcmv1.ServiceWithValues{Name: "b", Namespace: heldTestNS, Template: "auth-1.1.0"},
			),
			// Entirely unrelated services, and none of the ClusterDeployment's own.
			mcsServiceSet(kcmv1.ServiceWithValues{Name: "mcs-svc", Namespace: heldTestNS, Template: "other-1.0.0"}),
		})

		cond := apimeta.FindStatusCondition(obj.Status.Conditions, kcmv1.ServiceDependencyReadyCondition)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionTrue, cond.Status,
			"the MultiClusterService-owned ServiceSet has none of this ClusterDeployment's services and must be ignored")
	})

	t.Run("a dependent held at its old template: not ready, and Ready flips false as Progressing", func(t *testing.T) {
		t.Parallel()

		obj := cd.DeepCopy()
		setCond(obj, []kcmv1.ServiceSet{ownServiceSet(
			kcmv1.ServiceWithValues{Name: "a", Namespace: heldTestNS, Template: "cat-1.1.0"},
			kcmv1.ServiceWithValues{Name: "b", Namespace: heldTestNS, Template: "auth-1.0.1"},
		)})

		cond := apimeta.FindStatusCondition(obj.Status.Conditions, kcmv1.ServiceDependencyReadyCondition)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, kcmv1.ServiceDependencyNotReadyReason, cond.Reason)
		assert.Contains(t, cond.Message, "k0rdent-apis/b has auth-1.0.1, wants auth-1.1.0")

		obj.Status.Conditions = conditionsutil.UpdateReadyCondition(
			obj.Status.Conditions, obj.Generation, handleClusterDeploymentFailedConditions)

		ready := apimeta.FindStatusCondition(obj.Status.Conditions, kcmv1.ReadyCondition)
		require.NotNil(t, ready)
		assert.Equal(t, metav1.ConditionFalse, ready.Status,
			"a silently held service must not leave the ClusterDeployment reporting Ready")
		assert.Equal(t, kcmv1.ProgressingReason, ready.Reason,
			"a dependency hold is deliberate and self-resolving, so it is Progressing rather than Failed")
	})
}
