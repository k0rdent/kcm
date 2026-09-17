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

package sveltos

import (
	"context"
	"testing"
	"time"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	"github.com/projectsveltos/addon-controller/lib/clusterops"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// Test_ensureTeardownOrder walks the handshake that keeps sveltos from
// uninstalling a dependsOn chain front to back (#3066).
func Test_ensureTeardownOrder(t *testing.T) {
	t.Parallel()

	const (
		mcsName   = "test-mcs"
		namespace = "kcm-system"
	)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kcmv1.AddToScheme(scheme))
	utilruntime.Must(addoncontrollerv1beta1.AddToScheme(scheme))
	utilruntime.Must(libsveltosv1beta1.AddToScheme(scheme))

	// cert-manager <- kserve-crd <- kserve-resources, installed in that order
	chart := func(name string) addoncontrollerv1beta1.HelmChart {
		return addoncontrollerv1beta1.HelmChart{ReleaseName: name, ReleaseNamespace: name}
	}
	installOrder := []addoncontrollerv1beta1.HelmChart{
		chart("cert-manager"), chart("kserve-crd"), chart("kserve-resources"),
	}
	teardownOrder := []addoncontrollerv1beta1.HelmChart{
		chart("kserve-resources"), chart("kserve-crd"), chart("cert-manager"),
	}

	service := func(name string, dependsOn ...string) kcmv1.Service {
		svc := kcmv1.Service{Name: name, Namespace: name}
		for _, d := range dependsOn {
			svc.DependsOn = append(svc.DependsOn, kcmv1.ServiceDependsOn{Name: d, Namespace: d})
		}
		return svc
	}
	mcs := &kcmv1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{Name: mcsName},
		Spec: kcmv1.MultiClusterServiceSpec{ServiceSpec: kcmv1.ServiceSpec{Services: []kcmv1.Service{
			service("cert-manager"),
			service("kserve-crd", "cert-manager"),
			service("kserve-resources", "kserve-crd"),
		}}},
	}

	deletedAt := metav1.NewTime(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	now := func() time.Time { return deletedAt.Add(time.Second) }

	serviceSet := func() *kcmv1.ServiceSet {
		return &kcmv1.ServiceSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-serviceset", Namespace: namespace, DeletionTimestamp: &deletedAt,
			},
			Spec: kcmv1.ServiceSetSpec{
				MultiClusterService: mcsName,
				Services: []kcmv1.ServiceWithValues{
					{Name: "cert-manager", Namespace: "cert-manager"},
					{Name: "kserve-crd", Namespace: "kserve-crd"},
					{Name: "kserve-resources", Namespace: "kserve-resources"},
				},
			},
		}
	}

	clusterRef := corev1.ObjectReference{
		Kind:       libsveltosv1beta1.SveltosClusterKind,
		APIVersion: libsveltosv1beta1.GroupVersion.WithKind(libsveltosv1beta1.SveltosClusterKind).GroupVersion().String(),
		Namespace:  namespace,
		Name:       "mgmt",
	}
	profile := func(charts []addoncontrollerv1beta1.HelmChart) *addoncontrollerv1beta1.Profile {
		return &addoncontrollerv1beta1.Profile{
			ObjectMeta: metav1.ObjectMeta{Name: "test-serviceset", Namespace: namespace},
			Spec:       addoncontrollerv1beta1.Spec{HelmCharts: charts},
			Status:     addoncontrollerv1beta1.Status{MatchingClusterRefs: []corev1.ObjectReference{clusterRef}},
		}
	}
	summary := func(charts []addoncontrollerv1beta1.HelmChart) *addoncontrollerv1beta1.ClusterSummary {
		return &addoncontrollerv1beta1.ClusterSummary{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name: clusterops.GetClusterSummaryName(
					addoncontrollerv1beta1.ProfileKind, "test-serviceset", clusterRef.Name, true),
			},
			Spec: addoncontrollerv1beta1.ClusterSummarySpec{
				ClusterProfileSpec: addoncontrollerv1beta1.Spec{HelmCharts: charts},
			},
		}
	}

	// Self-management is the path #3066 was reported on: reconcileDelete hands
	// ensureTeardownOrder a ClusterProfile instead of a Profile, and the
	// ClusterSummary is named after that kind.
	selfManagedServiceSet := func() *kcmv1.ServiceSet {
		ss := serviceSet()
		ss.Spec.Provider.SelfManagement = true
		return ss
	}
	clusterProfile := func(charts []addoncontrollerv1beta1.HelmChart) *addoncontrollerv1beta1.ClusterProfile {
		return &addoncontrollerv1beta1.ClusterProfile{
			ObjectMeta: metav1.ObjectMeta{Name: "test-serviceset"},
			Spec:       addoncontrollerv1beta1.Spec{HelmCharts: charts},
			Status:     addoncontrollerv1beta1.Status{MatchingClusterRefs: []corev1.ObjectReference{clusterRef}},
		}
	}
	clusterSummary := func(charts []addoncontrollerv1beta1.HelmChart) *addoncontrollerv1beta1.ClusterSummary {
		return &addoncontrollerv1beta1.ClusterSummary{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name: clusterops.GetClusterSummaryName(
					addoncontrollerv1beta1.ClusterProfileKind, "test-serviceset", clusterRef.Name, true),
			},
			Spec: addoncontrollerv1beta1.ClusterSummarySpec{
				ClusterProfileSpec: addoncontrollerv1beta1.Spec{HelmCharts: charts},
			},
		}
	}

	t.Run("a Profile in install order is rewritten back to front", func(t *testing.T) {
		t.Parallel()
		p := profile(installOrder)
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(mcs, p, summary(installOrder)).Build()
		r := &ServiceSetReconciler{Client: cl, timeFunc: now}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, serviceSet(), p)
		require.NoError(t, err)
		require.True(t, requeue, "the deletion must wait for the new order to be applied")

		stored := new(addoncontrollerv1beta1.Profile)
		require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(p), stored))
		require.True(t, sameReleaseOrder(teardownOrder, stored.Spec.HelmCharts),
			"got %v", releaseNames(stored.Spec.HelmCharts))
	})

	t.Run("a Profile already in teardown order waits for the ClusterSummary", func(t *testing.T) {
		t.Parallel()
		p := profile(teardownOrder)
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(mcs, p, summary(installOrder)).Build()
		r := &ServiceSetReconciler{Client: cl, timeFunc: now}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, serviceSet(), p)
		require.NoError(t, err)
		require.True(t, requeue, "sveltos uninstalls from the ClusterSummary, so it has to catch up first")
	})

	t.Run("both in teardown order: the deletion may proceed", func(t *testing.T) {
		t.Parallel()
		p := profile(teardownOrder)
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(mcs, p, summary(teardownOrder)).Build()
		r := &ServiceSetReconciler{Client: cl, timeFunc: now}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, serviceSet(), p)
		require.NoError(t, err)
		require.False(t, requeue)
	})

	t.Run("self-management: a ClusterProfile is rewritten the same way", func(t *testing.T) {
		t.Parallel()
		p := clusterProfile(installOrder)
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(mcs, p, clusterSummary(installOrder)).Build()
		r := &ServiceSetReconciler{Client: cl, timeFunc: now}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, selfManagedServiceSet(), p)
		require.NoError(t, err)
		require.True(t, requeue, "the deletion must wait for the new order to be applied")

		stored := new(addoncontrollerv1beta1.ClusterProfile)
		require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(p), stored))
		require.True(t, sameReleaseOrder(teardownOrder, stored.Spec.HelmCharts),
			"got %v", releaseNames(stored.Spec.HelmCharts))
	})

	t.Run("self-management: both in teardown order, the deletion may proceed", func(t *testing.T) {
		t.Parallel()
		p := clusterProfile(teardownOrder)
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(mcs, p, clusterSummary(teardownOrder)).Build()
		r := &ServiceSetReconciler{Client: cl, timeFunc: now}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, selfManagedServiceSet(), p)
		require.NoError(t, err)
		require.False(t, requeue)
	})

	// A service may leave the namespace empty ("empty value means default
	// namespace"), and the chart builders then derive the release namespace from
	// the service name - a rank keyed by the service would miss those charts and
	// silently leave the install order in place.
	t.Run("a service without a namespace still matches its release", func(t *testing.T) {
		t.Parallel()
		nsLess := func(name string, dependsOn ...string) kcmv1.Service {
			svc := kcmv1.Service{Name: name}
			for _, d := range dependsOn {
				svc.DependsOn = append(svc.DependsOn, kcmv1.ServiceDependsOn{Name: d})
			}
			return svc
		}
		nsLessMCS := &kcmv1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Name: mcsName},
			Spec: kcmv1.MultiClusterServiceSpec{ServiceSpec: kcmv1.ServiceSpec{Services: []kcmv1.Service{
				nsLess("cert-manager"),
				nsLess("kserve-crd", "cert-manager"),
				nsLess("kserve-resources", "kserve-crd"),
			}}},
		}
		ss := serviceSet()
		for i := range ss.Spec.Services {
			ss.Spec.Services[i].Namespace = ""
		}

		p := profile(installOrder)
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nsLessMCS, p, summary(installOrder)).Build()
		r := &ServiceSetReconciler{Client: cl, timeFunc: now}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, ss, p)
		require.NoError(t, err)
		require.True(t, requeue)

		stored := new(addoncontrollerv1beta1.Profile)
		require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(p), stored))
		require.True(t, sameReleaseOrder(teardownOrder, stored.Spec.HelmCharts),
			"got %v", releaseNames(stored.Spec.HelmCharts))
	})

	t.Run("no dependsOn: the order is left alone and nothing is held back", func(t *testing.T) {
		t.Parallel()
		plainMCS := &kcmv1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Name: mcsName},
			Spec: kcmv1.MultiClusterServiceSpec{ServiceSpec: kcmv1.ServiceSpec{Services: []kcmv1.Service{
				service("cert-manager"), service("kserve-crd"), service("kserve-resources"),
			}}},
		}
		p := profile(installOrder)
		summaryReads := 0
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(plainMCS, p, summary(installOrder)).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*addoncontrollerv1beta1.ClusterSummary); ok {
						summaryReads++
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build()
		r := &ServiceSetReconciler{Client: cl, timeFunc: now}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, serviceSet(), p)
		require.NoError(t, err)
		require.False(t, requeue)
		require.Zero(t, summaryReads, "nothing to order, so nothing to wait for")

		stored := new(addoncontrollerv1beta1.Profile)
		require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(p), stored))
		require.True(t, sameReleaseOrder(installOrder, stored.Spec.HelmCharts),
			"got %v", releaseNames(stored.Spec.HelmCharts))
	})

	t.Run("a single release cannot be torn down out of order", func(t *testing.T) {
		t.Parallel()
		p := profile(installOrder[:1])
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(mcs, p).Build()
		r := &ServiceSetReconciler{Client: cl, timeFunc: now}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, serviceSet(), p)
		require.NoError(t, err)
		require.False(t, requeue)
	})

	// The deadline is stamped when the wait begins, so a pass that runs long after
	// the ServiceSet was marked for deletion - a restart, a backlog, the pass that
	// moves the services to Deleting - still gets its turn.
	late := func() time.Time { return deletedAt.Add(teardownOrderTimeout + time.Second) }

	t.Run("a late first pass still reorders and waits", func(t *testing.T) {
		t.Parallel()
		p := profile(installOrder)
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(mcs, p, summary(installOrder)).Build()
		r := &ServiceSetReconciler{Client: cl, timeFunc: late}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, serviceSet(), p)
		require.NoError(t, err)
		require.True(t, requeue, "a write must always be given a pass to propagate")

		stored := new(addoncontrollerv1beta1.Profile)
		require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(p), stored))
		require.True(t, sameReleaseOrder(teardownOrder, stored.Spec.HelmCharts),
			"got %v", releaseNames(stored.Spec.HelmCharts))
	})

	t.Run("a late first wait is stamped, not expired", func(t *testing.T) {
		t.Parallel()
		p := profile(teardownOrder)
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(mcs, p, summary(installOrder)).Build()
		r := &ServiceSetReconciler{Client: cl, timeFunc: late}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, serviceSet(), p)
		require.NoError(t, err)
		require.True(t, requeue, "the deadline runs from the wait, not from the deletion")

		stored := new(addoncontrollerv1beta1.Profile)
		require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(p), stored))
		require.Equal(t, late().Format(time.RFC3339), stored.Annotations[teardownOrderWaitAnnotation])
	})

	t.Run("a stalled handshake stops holding the deletion back", func(t *testing.T) {
		t.Parallel()
		p := profile(teardownOrder)
		p.Annotations = map[string]string{
			teardownOrderWaitAnnotation: deletedAt.Format(time.RFC3339),
		}
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(mcs, p, summary(installOrder)).Build()
		r := &ServiceSetReconciler{Client: cl, timeFunc: late}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, serviceSet(), p)
		require.NoError(t, err)
		require.False(t, requeue, "the Profile must be deleted rather than held forever")
	})

	t.Run("nothing deployed yet: no ClusterSummary to wait for", func(t *testing.T) {
		t.Parallel()
		p := profile(teardownOrder)
		p.Status.MatchingClusterRefs = nil
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(mcs, p).Build()
		r := &ServiceSetReconciler{Client: cl, timeFunc: now}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, serviceSet(), p)
		require.NoError(t, err)
		require.False(t, requeue)
	})
}

func releaseNames(charts []addoncontrollerv1beta1.HelmChart) []string {
	names := make([]string, 0, len(charts))
	for _, chart := range charts {
		names = append(names, chart.ReleaseName)
	}
	return names
}
