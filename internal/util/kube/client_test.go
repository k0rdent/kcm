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
	"fmt"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetChildClient(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	const (
		secretName = "some-cluster-kubeconfig"
		ns         = "some-namespace"

		secretKey = "value"
	)
	secretRef := client.ObjectKey{Name: secretName, Namespace: ns}

	t.Run("missing secret", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		_, err := GetChildClient(t.Context(), cl, secretRef, secretKey, scheme, DefaultClientFactory)
		require.ErrorContains(t, err, "failed to get Secret")
	})

	t.Run("secret missing 'value' key", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
			Data:       map[string][]byte{"wrong": []byte("data")},
		}
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		_, err := GetChildClient(t.Context(), cl, secretRef, secretKey, scheme, DefaultClientFactory)
		require.ErrorContains(t, err, "is empty")
	})

	t.Run("malformed kubeconfig", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
			Data:       map[string][]byte{"value": []byte("not a kubeconfig")},
		}
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		_, err := GetChildClient(t.Context(), cl, secretRef, secretKey, scheme, DefaultClientFactory)
		require.ErrorContains(t, err, "cannot unmarshal")
	})

	t.Run("valid kubeconfig", func(t *testing.T) {
		const template = `apiVersion: v1
kind: Config
clusters:
- name: local
  cluster:
    server: %s
users:
- name: %s
  user:
    token: "fake"
contexts:
- name: default
  context:
    cluster: local
    user: %s
current-context: default`

		data := fmt.Sprintf(template, "https://localhost:6443", "admin", "admin")
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
			Data:       map[string][]byte{"value": unsafe.Slice(unsafe.StringData(data), len(data))},
		}
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

		child, err := GetChildClient(t.Context(), cl, secretRef, secretKey, scheme, DefaultClientFactory)
		require.NoError(t, err)
		require.NotNil(t, child)
	})

	t.Run("smoke for fake factory", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
			Data:       map[string][]byte{"value": []byte("foobar")},
		}
		mgmt := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

		factory := func(_ []byte, sch *runtime.Scheme) (client.Client, error) {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "test-cm"},
				Data:       map[string]string{"field": "value"},
			}
			return fake.NewClientBuilder().WithScheme(sch).WithObjects(cm).Build(), nil
		}

		childClient, err := GetChildClient(t.Context(), mgmt, secretRef, secretKey, scheme, factory)
		require.NoError(t, err)

		retrieved := &corev1.ConfigMap{}
		err = childClient.Get(t.Context(), client.ObjectKey{Namespace: ns, Name: "test-cm"}, retrieved)
		require.NoError(t, err)
		require.Equal(t, "value", retrieved.Data["field"])
	})
}
