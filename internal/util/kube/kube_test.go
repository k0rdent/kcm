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

package kube

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEnsureDeleteAllOf(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	const ns = metav1.NamespaceDefault
	gvk := corev1.SchemeGroupVersion.WithKind("ConfigMap")

	tests := []struct {
		name            string
		seedObjects     []client.Object
		listErr         error
		deleteErr       error
		wantRequeue     bool
		wantErrContains string
		assertBaseState func(t *testing.T, base client.Client)
	}{
		{
			name: "returns requeue when objects are present",
			seedObjects: []client.Object{
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test-cm", Namespace: ns}},
			},
			wantRequeue: true,
			assertBaseState: func(t *testing.T, base client.Client) {
				t.Helper()
				got := &corev1.ConfigMap{}
				err := base.Get(t.Context(), client.ObjectKey{Namespace: ns, Name: "test-cm"}, got)
				require.True(t, apierrors.IsNotFound(err))
			},
		},
		{
			name:        "returns no requeue when no objects are present",
			wantRequeue: false,
		},
		{
			name:            "returns hard error when list fails",
			listErr:         errors.New("list failed"),
			wantRequeue:     false,
			wantErrContains: "failed to list",
		},
		{
			name: "returns hard error when delete fails",
			seedObjects: []client.Object{
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test-cm", Namespace: ns}},
			},
			deleteErr:       errors.New("delete failed"),
			wantRequeue:     false,
			wantErrContains: "failed to delete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.seedObjects...).Build()

			var cl client.Client = base
			if tt.listErr != nil || tt.deleteErr != nil {
				cl = &ensureDeleteAllOfErrClient{
					Client:    base,
					listErr:   tt.listErr,
					deleteErr: tt.deleteErr,
				}
			}

			requeue, err := EnsureDeleteAllOf(t.Context(), cl, gvk, &client.ListOptions{Namespace: ns})

			if tt.wantErrContains != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.wantErrContains)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.wantRequeue, requeue)

			if tt.assertBaseState != nil {
				tt.assertBaseState(t, base)
			}
		})
	}
}

type ensureDeleteAllOfErrClient struct {
	client.Client
	listErr   error
	deleteErr error
}

func (e *ensureDeleteAllOfErrClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*metav1.PartialObjectMetadataList); ok && e.listErr != nil {
		return e.listErr
	}

	return e.Client.List(ctx, list, opts...)
}

func (e *ensureDeleteAllOfErrClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if e.deleteErr != nil {
		return e.deleteErr
	}

	return e.Client.Delete(ctx, obj, opts...)
}
