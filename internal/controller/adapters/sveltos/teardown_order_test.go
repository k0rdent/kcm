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

	t.Run("a stalled handshake stops holding the deletion back", func(t *testing.T) {
		t.Parallel()
		p := profile(installOrder)
		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(mcs, p, summary(installOrder)).Build()
		late := func() time.Time { return deletedAt.Add(teardownOrderTimeout + time.Second) }
		r := &ServiceSetReconciler{Client: cl, timeFunc: late}

		requeue, err := r.ensureTeardownOrder(t.Context(), cl, serviceSet(), p)
		require.NoError(t, err)
		require.False(t, requeue, "the Profile must be deleted unordered rather than never")
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
