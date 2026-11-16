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
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func Test_syncRemoteHostObjects(t *testing.T) {
	const (
		namespace     = "test-ns"
		cdName        = "test-cd"
		kcpName       = cdName + "-cp"
		hostSecretNs  = "kcm-system"
		hostSecret    = "host-kubeconfig"
		requeueAfter  = 5 * time.Second
		rootOwnerName = cdName + k0smotronExternalOwnerSuffix
	)

	cd := func() *kcmv1.ClusterDeployment {
		return &kcmv1.ClusterDeployment{Name: cdName, Namespace: namespace}
	}

	capiCluster := func(group, kind string) *clusterapiv1.Cluster {
		c := &clusterapiv1.Cluster{Name: cdName, Namespace: namespace}
		c.Spec.ControlPlaneRef = clusterapiv1.ContractVersionedObjectReference{APIGroup: group, Kind: kind, Name: kcpName}
		return c
	}
	k0smotronCluster := func() *clusterapiv1.Cluster {
		return capiCluster(k0smotronControlPlaneGVK.Group, k0smotronControlPlaneGVK.Kind)
	}

	remoteHostCluster := map[string]any{
		"kubeconfigRef": map[string]any{"name": hostSecret, "namespace": hostSecretNs, "key": "value"},
	}
	kcp := func(spec map[string]any) *unstructured.Unstructured {
		u := new(unstructured.Unstructured)
		u.SetGroupVersionKind(k0smotronControlPlaneGVK)
		u.SetNamespace(namespace)
		u.SetName(kcpName)
		u.Object["spec"] = spec
		return u
	}
	secretMount := func(name string) map[string]any {
		return map[string]any{"path": "/mnt/" + name, "secret": map[string]any{"secretName": name}}
	}

	secret := func(name, data string) *corev1.Secret {
		return &corev1.Secret{
			Name: name, Namespace: namespace,
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{"config": []byte(data)},
		}
	}
	hostKubeconfig := &corev1.Secret{
		Name: hostSecret, Namespace: hostSecretNs,
		Data: map[string][]byte{"value": []byte("dummy")},
	}
	rootOwner := func(name string, uid types.UID) *corev1.ConfigMap {
		return &corev1.ConfigMap{Name: name, Namespace: namespace, UID: uid}
	}
	ownerRef := func(owner *corev1.ConfigMap) metav1.OwnerReference {
		return metav1.OwnerReference{APIVersion: "v1", Kind: "ConfigMap", Name: owner.Name, UID: owner.UID}
	}
	managedLabels := map[string]string{remoteHostCopyLabel: kcmv1.KCMManagedLabelValue}

	// The source client needs the K0smotronControlPlane kind in its RESTMapper, which the
	// default fake mapper (built from the scheme) does not have.
	sourceClientBuilder := func(objs ...crclient.Object) *fake.ClientBuilder {
		mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{corev1.SchemeGroupVersion, clusterapiv1.GroupVersion, k0smotronControlPlaneGVK.GroupVersion()})
		mapper.Add(corev1.SchemeGroupVersion.WithKind("Secret"), meta.RESTScopeNamespace)
		mapper.Add(corev1.SchemeGroupVersion.WithKind("ConfigMap"), meta.RESTScopeNamespace)
		mapper.Add(clusterapiv1.GroupVersion.WithKind(clusterapiv1.ClusterKind), meta.RESTScopeNamespace)
		mapper.Add(k0smotronControlPlaneGVK, meta.RESTScopeNamespace)
		return fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithRESTMapper(mapper).WithObjects(objs...)
	}
	sourceClient := func(objs ...crclient.Object) crclient.Client {
		return sourceClientBuilder(objs...).Build()
	}
	// kcpNotServed fails every K0smotronControlPlane read the way a cluster without k0smotron
	// v1beta2 does; the fake client itself reports NotFound for kinds it does not know.
	kcpNotServed := func(objs ...crclient.Object) crclient.Client {
		return sourceClientBuilder(objs...).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
				if obj.GetObjectKind().GroupVersionKind() == k0smotronControlPlaneGVK {
					return &meta.NoKindMatchError{GroupKind: k0smotronControlPlaneGVK.GroupKind(), SearchedVersions: []string{k0smotronControlPlaneGVK.Version}}
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
	}
	hostClient := func(objs ...crclient.Object) crclient.Client {
		return fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(objs...).Build()
	}

	type run struct {
		res           ctrl.Result
		err           error
		factoryCalled bool
	}
	sync := func(t *testing.T, src, host crclient.Client) run {
		t.Helper()
		var out run
		r := &ClusterDeploymentReconciler{
			defaultRequeueTime: requeueAfter,
			childClientFactory: func([]byte, *runtime.Scheme) (crclient.Client, error) {
				out.factoryCalled = true
				return host, nil
			},
		}
		out.res, out.err = r.syncRemoteHostObjects(t.Context(), &clusterScope{cd: cd(), rgnClient: src})
		return out
	}

	getSecret := func(t *testing.T, cl crclient.Client, name string) *corev1.Secret {
		t.Helper()
		s := new(corev1.Secret)
		NewWithT(t).Expect(cl.Get(t.Context(), crclient.ObjectKey{Namespace: namespace, Name: name}, s)).To(Succeed())
		return s
	}

	t.Run("no-op without a remote host", func(t *testing.T) {
		for name, src := range map[string]crclient.Client{
			"no CAPI Cluster":                 sourceClient(),
			"not a K0smotronControlPlane":     sourceClient(capiCluster("controlplane.cluster.x-k8s.io", "K0sControlPlane")),
			"no remoteHostCluster":            sourceClient(k0smotronCluster(), kcp(map[string]any{"mounts": []any{secretMount("auth")}})),
			"k0smotron v1beta2 is not served": kcpNotServed(k0smotronCluster()),
		} {
			t.Run(name, func(t *testing.T) {
				g := NewWithT(t)
				out := sync(t, src, hostClient())
				g.Expect(out.err).To(Succeed())
				g.Expect(out.res).To(Equal(ctrl.Result{}))
				g.Expect(out.factoryCalled).To(BeFalse())
			})
		}
	})

	t.Run("requeues while the K0smotronControlPlane does not exist yet", func(t *testing.T) {
		g := NewWithT(t)
		out := sync(t, sourceClient(k0smotronCluster()), hostClient())
		g.Expect(out.err).To(Succeed())
		g.Expect(out.res.RequeueAfter).To(Equal(requeueAfter))
		g.Expect(out.factoryCalled).To(BeFalse())
	})

	t.Run("requeues until k0smotron creates its external owner on the host", func(t *testing.T) {
		g := NewWithT(t)
		src := sourceClient(k0smotronCluster(), hostKubeconfig, secret("auth", "a"),
			kcp(map[string]any{"remoteHostCluster": remoteHostCluster, "mounts": []any{secretMount("auth")}}))
		host := hostClient()

		out := sync(t, src, host)
		g.Expect(out.err).To(Succeed())
		g.Expect(out.res.RequeueAfter).To(Equal(requeueAfter))
		err := host.Get(t.Context(), crclient.ObjectKey{Namespace: namespace, Name: "auth"}, new(corev1.Secret))
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	t.Run("fails when the host kubeconfig Secret is missing", func(t *testing.T) {
		g := NewWithT(t)
		src := sourceClient(k0smotronCluster(), kcp(map[string]any{"remoteHostCluster": remoteHostCluster}))

		out := sync(t, src, hostClient())
		g.Expect(out.err).To(MatchError(ContainSubstring("failed to get a client for the remote host cluster")))
		g.Expect(out.factoryCalled).To(BeFalse())
	})

	t.Run("copies every referenced object present on the source", func(t *testing.T) {
		g := NewWithT(t)
		owner := rootOwner(rootOwnerName, "owner-uid")
		audit := &corev1.ConfigMap{Name: "audit", Namespace: namespace, Data: map[string]string{"policy": "p"}}
		src := sourceClient(k0smotronCluster(), hostKubeconfig,
			secret("auth", "a"), secret("registry-ca", "ca"), secret("kine-dsn", "dsn"), audit,
			kcp(map[string]any{
				"remoteHostCluster": remoteHostCluster,
				"mounts": []any{
					secretMount("auth"),
					secretMount("host-provided"), // not on the source: left to the host
					map[string]any{"path": "/mnt/audit", "configMap": map[string]any{"name": "audit"}},
					map[string]any{"path": "/mnt/projected", "projected": map[string]any{"sources": []any{
						map[string]any{"secret": map[string]any{"name": "registry-ca"}},
					}}},
				},
				"storage": map[string]any{"type": "kine", "kine": map[string]any{"dataSourceSecretName": "kine-dsn"}},
			}))
		host := hostClient(owner)

		out := sync(t, src, host)
		g.Expect(out.err).To(Succeed())
		g.Expect(out.res).To(Equal(ctrl.Result{}))

		for name, data := range map[string]string{"auth": "a", "registry-ca": "ca", "kine-dsn": "dsn"} {
			s := getSecret(t, host, name)
			g.Expect(s.Data).To(Equal(map[string][]byte{"config": []byte(data)}), name)
			g.Expect(s.Type).To(Equal(corev1.SecretTypeOpaque), name)
			g.Expect(s.Labels).To(Equal(managedLabels), name)
			g.Expect(s.OwnerReferences).To(ConsistOf(ownerRef(owner)), name)
		}

		cm := new(corev1.ConfigMap)
		g.Expect(host.Get(t.Context(), crclient.ObjectKey{Namespace: namespace, Name: "audit"}, cm)).To(Succeed())
		g.Expect(cm.Data).To(Equal(audit.Data))
		g.Expect(cm.Labels).To(Equal(managedLabels))
		g.Expect(cm.OwnerReferences).To(ConsistOf(ownerRef(owner)))

		err := host.Get(t.Context(), crclient.ObjectKey{Namespace: namespace, Name: "host-provided"}, new(corev1.Secret))
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	t.Run("updates a copy whose source changed and keeps the other owners", func(t *testing.T) {
		g := NewWithT(t)
		owner := rootOwner(rootOwnerName, "owner-uid")
		otherOwner := rootOwner("other-cd"+k0smotronExternalOwnerSuffix, "other-uid")
		stale := secret("auth", "old")
		stale.Labels = managedLabels
		stale.OwnerReferences = []metav1.OwnerReference{ownerRef(otherOwner)}
		src := sourceClient(k0smotronCluster(), hostKubeconfig, secret("auth", "new"),
			kcp(map[string]any{"remoteHostCluster": remoteHostCluster, "mounts": []any{secretMount("auth")}}))
		host := hostClient(owner, otherOwner, stale)

		g.Expect(sync(t, src, host).err).To(Succeed())

		s := getSecret(t, host, "auth")
		g.Expect(s.Data).To(Equal(map[string][]byte{"config": []byte("new")}))
		g.Expect(s.OwnerReferences).To(ConsistOf(ownerRef(otherOwner), ownerRef(owner)))
	})

	t.Run("leaves a host object it did not create unchanged", func(t *testing.T) {
		g := NewWithT(t)
		owner := rootOwner(rootOwnerName, "owner-uid")
		src := sourceClient(k0smotronCluster(), hostKubeconfig, secret("auth", "from-kcm"),
			kcp(map[string]any{"remoteHostCluster": remoteHostCluster, "mounts": []any{secretMount("auth")}}))
		host := hostClient(owner, secret("auth", "operator-provided"))

		out := sync(t, src, host)
		g.Expect(out.err).To(Succeed())

		s := getSecret(t, host, "auth")
		g.Expect(s.Data).To(Equal(map[string][]byte{"config": []byte("operator-provided")}))
		g.Expect(s.Labels).To(BeEmpty())
		g.Expect(s.OwnerReferences).To(BeEmpty())
	})

	t.Run("prunes copies the cluster no longer references", func(t *testing.T) {
		g := NewWithT(t)
		owner := rootOwner(rootOwnerName, "owner-uid")
		otherOwner := rootOwner("other-cd"+k0smotronExternalOwnerSuffix, "other-uid")

		onlyOurs := secret("old-auth", "x")
		onlyOurs.Labels = managedLabels
		onlyOurs.OwnerReferences = []metav1.OwnerReference{ownerRef(owner)}
		shared := secret("shared-ca", "x")
		shared.Labels = managedLabels
		shared.OwnerReferences = []metav1.OwnerReference{ownerRef(owner), ownerRef(otherOwner)}
		othersOnly := secret("other-auth", "x")
		othersOnly.Labels = managedLabels
		othersOnly.OwnerReferences = []metav1.OwnerReference{ownerRef(otherOwner)}
		oldConfigMap := &corev1.ConfigMap{
			Name: "old-audit", Namespace: namespace,
			Labels: managedLabels, OwnerReferences: []metav1.OwnerReference{ownerRef(owner)},
		}

		src := sourceClient(k0smotronCluster(), hostKubeconfig,
			kcp(map[string]any{"remoteHostCluster": remoteHostCluster}))
		host := hostClient(owner, otherOwner, onlyOurs, shared, othersOnly, oldConfigMap)

		g.Expect(sync(t, src, host).err).To(Succeed())

		err := host.Get(t.Context(), crclient.ObjectKeyFromObject(onlyOurs), new(corev1.Secret))
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		err = host.Get(t.Context(), crclient.ObjectKeyFromObject(oldConfigMap), new(corev1.ConfigMap))
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		g.Expect(getSecret(t, host, "shared-ca").OwnerReferences).To(ConsistOf(ownerRef(otherOwner)))
		g.Expect(getSecret(t, host, "other-auth").OwnerReferences).To(ConsistOf(ownerRef(otherOwner)))
	})
}
