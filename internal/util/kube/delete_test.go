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

package kube

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pointerutil "github.com/K0rdent/kcm/internal/util/pointer"
)

func TestDeleteAllExceptAndWait(t *testing.T) {
	namespace := func(name string) *corev1.Namespace {
		return &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		}
	}

	pod := func(name, ns string, lbls ...map[string]string) *corev1.Pod {
		p := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
			},
		}

		if len(lbls) > 0 && lbls[0] != nil {
			p.Labels = lbls[0]
		}

		return p
	}

	type inject struct {
		listNamespacesErr error
		listPodsErr       error
		deleteErr         error // affects DeleteAllOf (collection delete) path
	}

	type tc struct {
		name         string
		seedObjs     []client.Object
		exclude      DeletionExcludeFn[*corev1.Pod]
		inject       inject
		timeout      time.Duration
		expectErrSub string
		postCheck    func(t *testing.T, cl client.Client)
	}

	tests := []tc{
		{
			name: "deletes all and waits until gone",
			seedObjs: []client.Object{
				namespace("ns-a"),
				namespace("ns-b"),
				pod("p1", "ns-a"),
				pod("p2", "ns-b"),
			},
			timeout: 1 * time.Second,
			postCheck: func(t *testing.T, cl client.Client) {
				t.Helper()

				for _, ns := range []string{"ns-a", "ns-b"} {
					pods := new(corev1.PodList)
					require.NoError(t, cl.List(t.Context(), pods, client.InNamespace(ns)))
					require.Empty(t, pods.Items)
				}
			},
		},
		{
			name:         "listing namespaces fails",
			timeout:      500 * time.Millisecond,
			expectErrSub: "failed to list namespaces",
			inject: inject{
				listNamespacesErr: errors.New("fake error listing namespaces"),
			},
		},
		{
			name: "deletecollection fails",
			seedObjs: []client.Object{
				namespace("ns-a"),
				pod("p1", "ns-a"),
			},
			timeout:      500 * time.Millisecond,
			expectErrSub: "failed to delete collection",
			inject: inject{
				deleteErr: errors.New("fake error delete"),
			},
		},
		{
			name: "polling list fails",
			seedObjs: []client.Object{
				namespace("ns-a"),
				pod("p1", "ns-a"),
			},
			timeout:      1 * time.Second,
			expectErrSub: "failed to list",
			inject: inject{
				listPodsErr: errors.New("fake error list pods during poll"),
			},
		},
		{
			name: "exclude preserves named pod in default and deletes others",
			seedObjs: []client.Object{
				namespace(metav1.NamespaceDefault),
				namespace("ns-a"),
				pod("keep-me", metav1.NamespaceDefault),
				pod("delete-me-1", metav1.NamespaceDefault),
				pod("delete-me-2", "ns-a"),
			},
			exclude: func(o *corev1.Pod) bool {
				return o.Namespace == metav1.NamespaceDefault && o.Name == "keep-me"
			},
			timeout: 1 * time.Second,
			postCheck: func(t *testing.T, cl client.Client) {
				t.Helper()

				defaultPods := new(corev1.PodList)
				require.NoError(t, cl.List(t.Context(), defaultPods, client.InNamespace(metav1.NamespaceDefault)))
				require.Len(t, defaultPods.Items, 1)
				require.Equal(t, "keep-me", defaultPods.Items[0].Name)

				otherPods := new(corev1.PodList)
				require.NoError(t, cl.List(t.Context(), otherPods, client.InNamespace("ns-a")))
				require.Empty(t, otherPods.Items)
			},
		},
		{
			name: "exclude preserves pods by label selector",
			seedObjs: []client.Object{
				namespace(metav1.NamespaceDefault),
				pod("kept-1", metav1.NamespaceDefault, map[string]string{"app": "keep"}),
				pod("kept-2", metav1.NamespaceDefault, map[string]string{"app": "keep"}),
				pod("gone-1", metav1.NamespaceDefault, map[string]string{"app": "other"}),
				pod("gone-2", metav1.NamespaceDefault),
			},
			exclude: func(o *corev1.Pod) bool {
				lbls := o.Labels
				return lbls != nil && lbls["app"] == "keep"
			},
			timeout: 1 * time.Second,
			postCheck: func(t *testing.T, cl client.Client) {
				t.Helper()

				pods := new(corev1.PodList)
				require.NoError(t, cl.List(t.Context(), pods, client.InNamespace(metav1.NamespaceDefault)))
				require.Len(t, pods.Items, 2)

				names := []string{pods.Items[0].Name, pods.Items[1].Name}
				require.ElementsMatch(t, []string{"kept-1", "kept-2"}, names)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newScheme(t)
			base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.seedObjs...).Build()

			var cl client.Client = base

			if tt.inject.listNamespacesErr != nil || tt.inject.listPodsErr != nil || tt.inject.deleteErr != nil {
				cl = &errClient{
					Client:            base,
					listNamespacesErr: tt.inject.listNamespacesErr,
					listPodsErr:       tt.inject.listPodsErr,
					deleteErr:         tt.inject.deleteErr,
				}
			}

			err := DeleteAllExceptAndWait(t.Context(), cl, &corev1.Pod{}, &corev1.PodList{}, tt.timeout, tt.exclude)

			if tt.expectErrSub != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.expectErrSub)
			} else {
				require.NoError(t, err)
			}

			if tt.postCheck != nil {
				tt.postCheck(t, base)
			}
		})
	}
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s), "adding to scheme")
	return s
}

type errClient struct {
	client.Client

	listNamespacesErr error
	listPodsErr       error
	deleteErr         error
}

func (e *errClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	switch list.(type) {
	case *corev1.NamespaceList:
		if e.listNamespacesErr != nil {
			return e.listNamespacesErr
		}
	case *corev1.PodList:
		if e.listPodsErr != nil {
			return e.listPodsErr
		}
	}

	return e.Client.List(ctx, list, opts...)
}

func (e *errClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	if e.deleteErr != nil {
		return e.deleteErr
	}

	return e.Client.DeleteAllOf(ctx, obj, opts...)
}

func Test_findPodsUsingPVC(t *testing.T) {
	tests := []struct {
		name      string
		objects   []client.Object
		claimName string
		namespace string
		wantPods  int
	}{
		{
			name:      "no pods present",
			objects:   nil,
			claimName: "test",
			namespace: "ns",
			wantPods:  0,
		},
		{
			name: "pod uses the PVC",
			objects: []client.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "ns"},
					Spec: corev1.PodSpec{
						Volumes: []corev1.Volume{
							{Name: "v1", VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"},
							}},
						},
					},
				},
			},
			claimName: "data",
			namespace: "ns",
			wantPods:  1,
		},
		{
			name: "pod does not use the PVC",
			objects: []client.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "ns"},
				},
			},
			claimName: "data",
			namespace: "ns",
			wantPods:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().WithObjects(tt.objects...).Build()
			pods, err := findPodsUsingPVC(t.Context(), cl, tt.namespace, tt.claimName)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(pods) != tt.wantPods {
				t.Fatalf("want %d pods, got %d", tt.wantPods, len(pods))
			}
		})
	}
}

func ownerRef(t *testing.T, obj metav1.Object, kind, apiVersion string) metav1.OwnerReference {
	t.Helper()

	return metav1.OwnerReference{
		APIVersion: apiVersion,
		Kind:       kind,
		Name:       obj.GetName(),
		Controller: pointerutil.To(true),
	}
}

func Test_findTopLevelAllowedController(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	tests := []struct {
		name      string
		objects   []client.Object
		inputObj  *metav1.ObjectMeta
		wantOwner client.Object
		wantErr   bool
	}{
		{
			name: "pod has no owner, expect nil",
			objects: func() []client.Object {
				p := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "p",
						Namespace: "ns",
					},
				}
				return []client.Object{p}
			}(),
			inputObj: &metav1.ObjectMeta{
				Name:      "p",
				Namespace: "ns",
			},
			wantOwner: nil,
		},
		{
			name: "pod is owned by deployment, expect deployment",
			objects: func() []client.Object {
				dep := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep",
						Namespace: "ns",
					},
				}
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "p",
						Namespace: "ns",
						OwnerReferences: []metav1.OwnerReference{
							ownerRef(t, &dep.ObjectMeta, "Deployment", "apps/v1"),
						},
					},
				}
				return []client.Object{dep, pod}
			}(),
			inputObj: func() *metav1.ObjectMeta {
				dep := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep",
						Namespace: "ns",
					},
				}
				return &metav1.ObjectMeta{
					Name:      "p",
					Namespace: "ns",
					OwnerReferences: []metav1.OwnerReference{
						ownerRef(t, &dep.ObjectMeta, "Deployment", "apps/v1"),
					},
				}
			}(),
			wantOwner: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "dep", Namespace: "ns"},
			},
		},
		{
			name: "pod owned by replicaset owned by deployment, expect deployment",
			objects: func() []client.Object {
				dep := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: "dep", Namespace: "ns"},
				}
				rs := &appsv1.ReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rs",
						Namespace: "ns",
						OwnerReferences: []metav1.OwnerReference{
							ownerRef(t, &dep.ObjectMeta, "Deployment", "apps/v1"),
						},
					},
				}
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "p",
						Namespace: "ns",
						OwnerReferences: []metav1.OwnerReference{
							ownerRef(t, &rs.ObjectMeta, "ReplicaSet", "apps/v1"),
						},
					},
				}
				return []client.Object{dep, rs, pod}
			}(),
			inputObj: func() *metav1.ObjectMeta {
				dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "dep", Namespace: "ns"}}
				rs := &appsv1.ReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rs",
						Namespace: "ns",
						OwnerReferences: []metav1.OwnerReference{
							ownerRef(t, &dep.ObjectMeta, "Deployment", "apps/v1"),
						},
					},
				}
				return &metav1.ObjectMeta{
					Name:      "p",
					Namespace: "ns",
					OwnerReferences: []metav1.OwnerReference{
						ownerRef(t, &rs.ObjectMeta, "ReplicaSet", "apps/v1"),
					},
				}
			}(),
			wantOwner: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "dep", Namespace: "ns"},
			},
		},
		{
			name: "pod owned by replicaset owned by deployment owned by non-allowed, expect deployment",
			objects: func() []client.Object {
				cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
					Name: "weird-owner", Namespace: "ns",
				}}
				dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
					Name: "dep", Namespace: "ns",
					OwnerReferences: []metav1.OwnerReference{
						ownerRef(t, &cm.ObjectMeta, "ConfigMap", "v1"),
					},
				}}
				rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
					Name: "rs", Namespace: "ns",
					OwnerReferences: []metav1.OwnerReference{
						ownerRef(t, &dep.ObjectMeta, "Deployment", "apps/v1"),
					},
				}}
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
					Name: "p", Namespace: "ns",
					OwnerReferences: []metav1.OwnerReference{
						ownerRef(t, &rs.ObjectMeta, "ReplicaSet", "apps/v1"),
					},
				}}
				return []client.Object{dep, rs, pod, cm}
			}(),
			inputObj: func() *metav1.ObjectMeta {
				cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
					Name: "weird-owner", Namespace: "ns",
				}}
				dep := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep",
						Namespace: "ns",
						OwnerReferences: []metav1.OwnerReference{
							ownerRef(t, &cm.ObjectMeta, "Deployment", "apps/v1"),
						},
					},
				}
				rs := &appsv1.ReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rs",
						Namespace: "ns",
						OwnerReferences: []metav1.OwnerReference{
							ownerRef(t, &dep.ObjectMeta, "Deployment", "apps/v1"),
						},
					},
				}
				return &metav1.ObjectMeta{
					Name:      "p",
					Namespace: "ns",
					OwnerReferences: []metav1.OwnerReference{
						ownerRef(t, &rs.ObjectMeta, "ReplicaSet", "apps/v1"),
					},
				}
			}(),
			wantOwner: &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "dep", Namespace: "ns"}},
		},
		{
			name: "pod owned by non-allowed, expect nil",
			objects: func() []client.Object {
				cm := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "weird-owner",
						Namespace: "ns",
					},
				}
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "p",
						Namespace: "ns",
						OwnerReferences: []metav1.OwnerReference{
							ownerRef(t, &cm.ObjectMeta, "ConfigMap", "v1"),
						},
					},
				}
				return []client.Object{cm, pod}
			}(),
			inputObj: &metav1.ObjectMeta{
				Name:      "p",
				Namespace: "ns",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Name:       "weird-owner",
						Controller: pointerutil.To(true),
					},
				},
			},
			wantOwner: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			got, err := findTopLevelAllowedController(t.Context(), cl, tt.inputObj)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantOwner == nil {
				if got != nil {
					t.Fatalf("expected nil owner, got %T %v", got, got)
				}
				return
			}

			if got == nil {
				t.Fatalf("expected owner %T but got nil", tt.wantOwner)
			}

			if got.GetName() != tt.wantOwner.GetName() ||
				got.GetNamespace() != tt.wantOwner.GetNamespace() {
				t.Fatalf("owner mismatch: want %s/%s, got %s/%s",
					tt.wantOwner.GetNamespace(), tt.wantOwner.GetName(),
					got.GetNamespace(), got.GetName(),
				)
			}
		})
	}
}

func TestDeletePVCsAndOwnersAndWait(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "dep",
			Namespace:  "ns",
			Finalizers: []string{"finalizer.test"},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod",
			Namespace: "ns",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "dep",
					Controller: pointerutil.To(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "v",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"},
					},
				},
			},
		},
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "data",
			Namespace:  "ns",
			Finalizers: []string{"test.finalizer"},
		},
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "ns"},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ns, pvc, pod, dep).
		Build()

	if err := DeletePVCsAndOwnersAndWait(t.Context(), cl, 3*time.Second, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := cl.Get(t.Context(), client.ObjectKey{Name: "data", Namespace: "ns"}, &corev1.PersistentVolumeClaim{}); err == nil {
		t.Fatalf("PVC should have been deleted")
	}

	// NOTE: we do not check pods since we are not in an actual environment so it will not be gc-ed

	if err := cl.Get(t.Context(), client.ObjectKey{Name: "dep", Namespace: "ns"}, &appsv1.Deployment{}); err == nil {
		t.Fatalf("Deployment should have been deleted")
	}
}
