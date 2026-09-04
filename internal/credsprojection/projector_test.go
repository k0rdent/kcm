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

package credsprojection

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, kcmv1.AddToScheme(s))
	return s
}

// --- Project (public) ---

func TestProject_NilProjectionConfig(t *testing.T) {
	scheme := testScheme(t)
	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	childClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	p := NewProjector(mgmtClient, childClient)
	cred := &kcmv1.Credential{
		ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns"},
		Spec: kcmv1.CredentialSpec{
			IdentityRef: &corev1.ObjectReference{
				APIVersion: "v1", Kind: "Secret", Name: "s", Namespace: "ns",
			},
			// ProjectionConfig is nil => no-op
		},
	}

	require.NoError(t, p.Project(t.Context(), cred, nil))
}

func TestProject_EndToEnd(t *testing.T) {
	scheme := testScheme(t)

	identitySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "os-cloud-config", Namespace: "ns"},
		Data: map[string][]byte{
			"clouds.yaml": []byte("clouds:\n  openstack:\n    auth:\n      auth_url: https://example.com\n"),
		},
	}

	templateCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "os-cloud-config-resource-template",
			Namespace: "ns",
		},
		Data: map[string]string{
			"secret.yaml": `apiVersion: v1
kind: Secret
metadata:
  name: openstack-cloud-config
  namespace: kube-system
type: Opaque
stringData:
  cloud.conf: "{{ b64dec (index .Identity "data" "clouds.yaml") }}"
`,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(identitySecret, templateCM).Build()
	childClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	p := NewProjector(mgmtClient, childClient)
	cred := &kcmv1.Credential{
		ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns"},
		Spec: kcmv1.CredentialSpec{
			IdentityRef: &corev1.ObjectReference{
				APIVersion: "v1",
				Kind:       "Secret",
				Name:       "os-cloud-config",
				Namespace:  "ns",
			},
			ProjectionConfig: &kcmv1.CredentialProjectionConfig{
				ResourceTemplateRef: &corev1.LocalObjectReference{
					Name: "os-cloud-config-resource-template",
				},
			},
		},
	}

	require.NoError(t, p.Project(t.Context(), cred, nil))

	// Verify the secret was created on the child cluster.
	childSecret := &corev1.Secret{}
	err := childClient.Get(t.Context(), client.ObjectKey{Name: "openstack-cloud-config", Namespace: "kube-system"}, childSecret)
	require.NoError(t, err)
	assert.Contains(t, childSecret.StringData["cloud.conf"], "auth_url: https://example.com")
}

func TestProject_InlineTemplate(t *testing.T) {
	scheme := testScheme(t)

	identitySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "os-cloud-config", Namespace: "ns"},
		Data: map[string][]byte{
			"clouds.yaml": []byte("clouds:\n  openstack:\n    auth:\n      auth_url: https://example.com\n"),
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(identitySecret).Build()
	childClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	p := NewProjector(mgmtClient, childClient)
	cred := &kcmv1.Credential{
		ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns"},
		Spec: kcmv1.CredentialSpec{
			IdentityRef: &corev1.ObjectReference{
				APIVersion: "v1",
				Kind:       "Secret",
				Name:       "os-cloud-config",
				Namespace:  "ns",
			},
			ProjectionConfig: &kcmv1.CredentialProjectionConfig{
				ResourceTemplate: `apiVersion: v1
kind: Secret
metadata:
  name: openstack-inline
  namespace: kube-system
type: Opaque
stringData:
  cloud.conf: "{{ b64dec (index .Identity "data" "clouds.yaml") }}"
`,
			},
		},
	}

	require.NoError(t, p.Project(t.Context(), cred, nil))

	childSecret := &corev1.Secret{}
	err := childClient.Get(t.Context(), client.ObjectKey{Name: "openstack-inline", Namespace: "kube-system"}, childSecret)
	require.NoError(t, err)
	assert.Contains(t, childSecret.StringData["cloud.conf"], "auth_url: https://example.com")
}

// --- buildTemplateData (private) ---

func Test_buildTemplateData(t *testing.T) {
	scheme := testScheme(t)

	tests := []struct {
		name            string
		cred            *kcmv1.Credential
		objects         []client.Object
		wantErr         string
		hasCompanion    bool
		identityName    string
		companionSecret string
	}{
		{
			name: "Secret identity (no companion)",
			cred: &kcmv1.Credential{
				ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns"},
				Spec: kcmv1.CredentialSpec{
					IdentityRef: &corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "Secret",
						Name:       "my-secret",
						Namespace:  "ns",
					},
				},
			},
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "ns"},
					Data:       map[string][]byte{"key": []byte("val")},
				},
			},
			hasCompanion: false,
			identityName: "my-secret",
		},
		{
			name: "non-Secret identity (with companion via CompanionSecretRef)",
			cred: &kcmv1.Credential{
				ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns"},
				Spec: kcmv1.CredentialSpec{
					IdentityRef: &corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Name:       "my-identity",
						Namespace:  "ns",
					},
					ProjectionConfig: &kcmv1.CredentialProjectionConfig{
						ResourceTemplateRef: &corev1.LocalObjectReference{Name: "unused"},
						SecretDataRef:       &corev1.LocalObjectReference{Name: "my-identity-secret"},
					},
				},
			},
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "my-identity", Namespace: "ns"},
					Data:       map[string]string{"field": "data"},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "my-identity-secret", Namespace: "ns"},
					Data:       map[string][]byte{"key": []byte("companion-val")},
				},
			},
			hasCompanion:    true,
			identityName:    "my-identity",
			companionSecret: "my-identity-secret",
		},
		{
			name: "non-Secret identity without CompanionSecretRef (no companion populated)",
			cred: &kcmv1.Credential{
				ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns"},
				Spec: kcmv1.CredentialSpec{
					IdentityRef: &corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Name:       "my-identity",
						Namespace:  "ns",
					},
				},
			},
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "my-identity", Namespace: "ns"},
					Data:       map[string]string{"field": "data"},
				},
			},
			hasCompanion: false,
			identityName: "my-identity",
		},
		{
			name: "missing identity object",
			cred: &kcmv1.Credential{
				ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns"},
				Spec: kcmv1.CredentialSpec{
					IdentityRef: &corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "Secret",
						Name:       "nonexistent",
						Namespace:  "ns",
					},
				},
			},
			objects: nil,
			wantErr: "getting identity object",
		},
		{
			name: "missing companion secret referenced by CompanionSecretRef",
			cred: &kcmv1.Credential{
				ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns"},
				Spec: kcmv1.CredentialSpec{
					IdentityRef: &corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Name:       "my-identity",
						Namespace:  "ns",
					},
					ProjectionConfig: &kcmv1.CredentialProjectionConfig{
						ResourceTemplateRef: &corev1.LocalObjectReference{Name: "unused"},
						SecretDataRef:       &corev1.LocalObjectReference{Name: "nonexistent-secret"},
					},
				},
			},
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "my-identity", Namespace: "ns"},
					Data:       map[string]string{"field": "data"},
				},
			},
			wantErr: "getting companion secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			for _, obj := range tt.objects {
				builder = builder.WithObjects(obj)
			}
			mgmtClient := builder.Build()

			p := NewProjector(mgmtClient, nil)
			data, err := p.buildTemplateData(t.Context(), tt.cred, nil)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, data)
			require.NotNil(t, data.Identity)

			if tt.hasCompanion {
				require.NotNil(t, data.IdentitySecret)
			} else {
				require.Nil(t, data.IdentitySecret)
			}
		})
	}

	t.Run("with infra provider", func(t *testing.T) {
		identitySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "ns"},
			Data:       map[string][]byte{"key": []byte("val")},
		}

		mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(identitySecret).Build()

		infraProvider := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "infrastructure.cluster.x-k8s.io/v1beta1",
				"kind":       "AzureCluster",
				"metadata":   map[string]any{"name": "my-cluster", "namespace": "ns"},
				"spec":       map[string]any{"location": "westus2", "subscriptionID": "sub-123"},
			},
		}

		p := NewProjector(mgmtClient, nil)
		cred := &kcmv1.Credential{
			ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns"},
			Spec: kcmv1.CredentialSpec{
				IdentityRef: &corev1.ObjectReference{
					APIVersion: "v1", Kind: "Secret", Name: "my-secret", Namespace: "ns",
				},
			},
		}

		data, err := p.buildTemplateData(t.Context(), cred, infraProvider)
		require.NoError(t, err)
		require.NotNil(t, data.InfrastructureProvider)
		assert.Equal(t, "AzureCluster", data.InfrastructureProvider["kind"])

		spec, ok := data.InfrastructureProvider["spec"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "westus2", spec["location"])
	})
}

// --- secretToTemplateMap (private) ---

func Test_secretToTemplateMap(t *testing.T) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "test-ns",
			Labels:    map[string]string{"app": "test"},
			Annotations: map[string]string{
				"note": "important",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		},
	}

	m := secretToTemplateMap(s)

	assert.Equal(t, "v1", m["apiVersion"])
	assert.Equal(t, "Secret", m["kind"])
	assert.Equal(t, corev1.SecretTypeOpaque, m["type"])

	meta, ok := m["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test-secret", meta["name"])
	assert.Equal(t, "test-ns", meta["namespace"])

	labels, ok := meta["labels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test", labels["app"])

	annotations, ok := meta["annotations"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "important", annotations["note"])

	data, ok := m["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "dmFsdWUx", data["key1"]) // base64("value1")
	assert.Equal(t, "dmFsdWUy", data["key2"]) // base64("value2")

	_, hasStringData := m["stringData"]
	assert.False(t, hasStringData, "stringData should not be present")

	// verify empty labels/annotations are omitted
	bare := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bare", Namespace: "ns"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	bareMap := secretToTemplateMap(bare)
	bareMeta, ok := bareMap["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Nil(t, bareMeta["labels"])
	assert.Nil(t, bareMeta["annotations"])
	assert.Nil(t, bareMap["type"]) // empty type omitted
}

// --- templateFuncMap (private) ---

func Test_templateFuncMap(t *testing.T) {
	data := &templateData{
		Identity: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"data":       map[string]any{"key": "val"},
		},
		IdentitySecret: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"data":       map[string]any{"secret-key": "secret-val"},
		},
	}
	fns := templateFuncMap(data)

	t.Run("b64dec valid", func(t *testing.T) {
		fn, ok := fns["b64dec"].(func(string) (string, error))
		require.True(t, ok)
		result, err := fn("aGVsbG8gd29ybGQ=")
		require.NoError(t, err)
		assert.Equal(t, "hello world", result)
	})

	t.Run("b64dec invalid", func(t *testing.T) {
		fn, ok := fns["b64dec"].(func(string) (string, error))
		require.True(t, ok)
		_, err := fn("!!!invalid!!!")
		require.Error(t, err)
	})

	t.Run("b64enc", func(t *testing.T) {
		fn, ok := fns["b64enc"].(func(string) string)
		require.True(t, ok)
		assert.Equal(t, "aGVsbG8=", fn("hello"))
	})

	t.Run("hasKey true", func(t *testing.T) {
		fn, ok := fns["hasKey"].(func(map[string]any, string) bool)
		require.True(t, ok)
		assert.True(t, fn(map[string]any{"a": 1}, "a"))
	})

	t.Run("hasKey false", func(t *testing.T) {
		fn, ok := fns["hasKey"].(func(map[string]any, string) bool)
		require.True(t, ok)
		assert.False(t, fn(map[string]any{"a": 1}, "b"))
	})

	t.Run("default with nil", func(t *testing.T) {
		fn, ok := fns["default"].(func(any, any) any)
		require.True(t, ok)
		assert.Equal(t, "fallback", fn("fallback", nil))
	})

	t.Run("default with empty string", func(t *testing.T) {
		fn, ok := fns["default"].(func(any, any) any)
		require.True(t, ok)
		assert.Equal(t, "fallback", fn("fallback", ""))
	})

	t.Run("default with value", func(t *testing.T) {
		fn, ok := fns["default"].(func(any, any) any)
		require.True(t, ok)
		assert.Equal(t, "real", fn("fallback", "real"))
	})

	t.Run("getResource identity", func(t *testing.T) {
		fn, ok := fns["getResource"].(func(string) (map[string]any, error))
		require.True(t, ok)
		result, err := fn("Identity")
		require.NoError(t, err)
		assert.Equal(t, "Secret", result["kind"])
	})

	t.Run("getResource identity (legacy alias)", func(t *testing.T) {
		fn, ok := fns["getResource"].(func(string) (map[string]any, error))
		require.True(t, ok)
		result, err := fn("InfrastructureProviderIdentity")
		require.NoError(t, err)
		assert.Equal(t, "Secret", result["kind"])
	})

	t.Run("getResource identity secret", func(t *testing.T) {
		fn, ok := fns["getResource"].(func(string) (map[string]any, error))
		require.True(t, ok)
		result, err := fn("IdentitySecret")
		require.NoError(t, err)
		d, ok := result["data"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "secret-val", d["secret-key"])
	})

	t.Run("getResource identity secret (legacy alias)", func(t *testing.T) {
		fn, ok := fns["getResource"].(func(string) (map[string]any, error))
		require.True(t, ok)
		result, err := fn("InfrastructureProviderIdentitySecret")
		require.NoError(t, err)
		d, ok := result["data"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "secret-val", d["secret-key"])
	})

	t.Run("getResource unknown", func(t *testing.T) {
		fn, ok := fns["getResource"].(func(string) (map[string]any, error))
		require.True(t, ok)
		_, err := fn("Unknown")
		require.ErrorContains(t, err, "unknown resource identifier")
	})

	t.Run("fromYaml valid", func(t *testing.T) {
		fn, ok := fns["fromYaml"].(func(string) (map[string]any, error))
		require.True(t, ok)
		result, err := fn("key: value\nnested:\n  a: b\n")
		require.NoError(t, err)
		assert.Equal(t, "value", result["key"])
		nested, ok := result["nested"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "b", nested["a"])
	})

	t.Run("fromYaml invalid", func(t *testing.T) {
		fn, ok := fns["fromYaml"].(func(string) (map[string]any, error))
		require.True(t, ok)
		_, err := fn(":\n  :\n  :")
		require.Error(t, err)
	})

	t.Run("toJson", func(t *testing.T) {
		fn, ok := fns["toJson"].(func(any) (string, error))
		require.True(t, ok)
		result, err := fn(map[string]any{"key": "value"})
		require.NoError(t, err)
		assert.JSONEq(t, `{"key":"value"}`, result)
	})

	t.Run("toYaml", func(t *testing.T) {
		fn, ok := fns["toYaml"].(func(any) (string, error))
		require.True(t, ok)
		result, err := fn(map[string]any{"key": "value"})
		require.NoError(t, err)
		assert.Contains(t, result, "key: value")
	})

	t.Run("fail", func(t *testing.T) {
		fn, ok := fns["fail"].(func(string) (string, error))
		require.True(t, ok)
		_, err := fn("something went wrong")
		require.ErrorContains(t, err, "something went wrong")
	})

	t.Run("dict valid", func(t *testing.T) {
		fn, ok := fns["dict"].(func(...any) (map[string]any, error))
		require.True(t, ok)
		result, err := fn("a", 1, "b", "two")
		require.NoError(t, err)
		assert.Equal(t, 1, result["a"])
		assert.Equal(t, "two", result["b"])
	})

	t.Run("dict odd args", func(t *testing.T) {
		fn, ok := fns["dict"].(func(...any) (map[string]any, error))
		require.True(t, ok)
		_, err := fn("a", 1, "b")
		require.ErrorContains(t, err, "even number")
	})

	t.Run("dict non-string key", func(t *testing.T) {
		fn, ok := fns["dict"].(func(...any) (map[string]any, error))
		require.True(t, ok)
		_, err := fn(42, "value")
		require.ErrorContains(t, err, "not a string")
	})
}

// --- renderConfigMapTemplates (private) ---

func Test_renderConfigMapTemplates(t *testing.T) {
	tests := []struct {
		name      string
		cmData    map[string]string
		data      *templateData
		wantCount int
		wantErr   string
		validate  func(t *testing.T, objs []unstructured.Unstructured)
	}{
		{
			name: "simple secret template",
			cmData: map[string]string{
				"secret.yaml": `apiVersion: v1
kind: Secret
metadata:
  name: cloud-config
  namespace: kube-system
type: Opaque
stringData:
  config: "{{ index .Identity "data" "clouds.yaml" }}"
`,
			},
			data: &templateData{
				Identity: map[string]any{
					"data": map[string]any{
						"clouds.yaml": "cloud-data-here",
					},
				},
			},
			wantCount: 1,
			validate: func(t *testing.T, objs []unstructured.Unstructured) {
				t.Helper()
				obj := objs[0]
				assert.Equal(t, "Secret", obj.GetKind())
				assert.Equal(t, "cloud-config", obj.GetName())
				assert.Equal(t, "kube-system", obj.GetNamespace())
			},
		},
		{
			name: "multi-document YAML",
			cmData: map[string]string{
				"resources.yaml": `apiVersion: v1
kind: Secret
metadata:
  name: secret-one
  namespace: kube-system
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: config-one
  namespace: kube-system
data:
  key: value
`,
			},
			data:      &templateData{},
			wantCount: 2,
			validate: func(t *testing.T, objs []unstructured.Unstructured) {
				t.Helper()
				assert.Equal(t, "Secret", objs[0].GetKind())
				assert.Equal(t, "ConfigMap", objs[1].GetKind())
			},
		},
		{
			name: "template with b64dec function",
			cmData: map[string]string{
				"secret.yaml": `apiVersion: v1
kind: Secret
metadata:
  name: decoded
  namespace: kube-system
stringData:
  decoded: "{{ b64dec "aGVsbG8=" }}"
`,
			},
			data:      &templateData{},
			wantCount: 1,
			validate: func(t *testing.T, objs []unstructured.Unstructured) {
				t.Helper()
				sd, _, _ := unstructured.NestedStringMap(objs[0].Object, "stringData")
				assert.Equal(t, "hello", sd["decoded"])
			},
		},
		{
			name: "template with hasKey function",
			cmData: map[string]string{
				"secret.yaml": `apiVersion: v1
kind: Secret
metadata:
  name: checked
  namespace: kube-system
stringData:
  has-auth: "{{ hasKey .Identity "auth" }}"
`,
			},
			data: &templateData{
				Identity: map[string]any{
					"auth": "some-val",
				},
			},
			wantCount: 1,
			validate: func(t *testing.T, objs []unstructured.Unstructured) {
				t.Helper()
				sd, _, _ := unstructured.NestedStringMap(objs[0].Object, "stringData")
				assert.Equal(t, "true", sd["has-auth"])
			},
		},
		{
			name: "invalid template syntax",
			cmData: map[string]string{
				"bad.yaml": `{{ .Nonexistent | badFunc }}`,
			},
			data:    &templateData{},
			wantErr: "parsing template",
		},
		{
			name:      "empty configmap data",
			cmData:    map[string]string{},
			data:      &templateData{},
			wantCount: 0,
		},
		{
			name: "empty YAML after rendering",
			cmData: map[string]string{
				"empty.yaml": `---
`,
			},
			data:      &templateData{},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &corev1.ConfigMap{Data: tt.cmData}
			objs, err := renderConfigMapTemplates(cm, tt.data)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Len(t, objs, tt.wantCount)
			if tt.validate != nil {
				tt.validate(t, objs)
			}
		})
	}
}

// --- renderInlineTemplate (private) ---

func Test_renderInlineTemplate(t *testing.T) {
	tests := []struct {
		name      string
		tplText   string
		data      *templateData
		wantCount int
		wantErr   string
	}{
		{
			name: "simple secret",
			tplText: `apiVersion: v1
kind: Secret
metadata:
  name: test
  namespace: default
stringData:
  key: "{{ index .Identity "data" "mykey" }}"
`,
			data: &templateData{
				Identity: map[string]any{
					"data": map[string]any{"mykey": "myval"},
				},
			},
			wantCount: 1,
		},
		{
			name: "multi-doc inline",
			tplText: `apiVersion: v1
kind: Secret
metadata:
  name: a
  namespace: default
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: b
  namespace: default
`,
			data:      &templateData{},
			wantCount: 2,
		},
		{
			name:    "invalid template",
			tplText: `{{ .Bad | noSuchFunc }}`,
			data:    &templateData{},
			wantErr: "parsing inline template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs, err := renderInlineTemplate(tt.tplText, tt.data)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Len(t, objs, tt.wantCount)
		})
	}
}

// --- parseMultiDocYAML (private) ---

func Test_parseMultiDocYAML(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCount int
		wantErr   string
	}{
		{
			name:      "single document",
			input:     "apiVersion: v1\nkind: Secret\nmetadata:\n  name: test\n",
			wantCount: 1,
		},
		{
			name:      "two documents",
			input:     "apiVersion: v1\nkind: Secret\nmetadata:\n  name: a\n---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: b\n",
			wantCount: 2,
		},
		{
			name:      "empty between separators",
			input:     "---\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: test\n---\n",
			wantCount: 1,
		},
		{
			name:      "completely empty",
			input:     "",
			wantCount: 0,
		},
		{
			name:      "only separators",
			input:     "---\n---\n---\n",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs, err := parseMultiDocYAML([]byte(tt.input))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Len(t, objs, tt.wantCount)
		})
	}
}

// --- serverSideApply (private) ---

func Test_serverSideApply(t *testing.T) {
	scheme := testScheme(t)

	t.Run("create new object", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		obj := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"name":      "test-cm",
					"namespace": "default",
				},
				"data": map[string]any{
					"key": "value",
				},
			},
		}

		require.NoError(t, serverSideApply(t.Context(), cl, obj))

		got := &corev1.ConfigMap{}
		require.NoError(t, cl.Get(t.Context(), client.ObjectKey{Name: "test-cm", Namespace: "default"}, got))
		assert.Equal(t, "value", got.Data["key"])
	})

	t.Run("update existing object", func(t *testing.T) {
		existing := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cm", Namespace: "default"},
			Data:       map[string]string{"key": "old-value"},
		}
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

		obj := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"name":      "test-cm",
					"namespace": "default",
				},
				"data": map[string]any{
					"key": "new-value",
				},
			},
		}

		require.NoError(t, serverSideApply(t.Context(), cl, obj))

		got := &corev1.ConfigMap{}
		require.NoError(t, cl.Get(t.Context(), client.ObjectKey{Name: "test-cm", Namespace: "default"}, got))
		assert.Equal(t, "new-value", got.Data["key"])
	})

	t.Run("missing apiVersion", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		obj := &unstructured.Unstructured{
			Object: map[string]any{
				"kind": "ConfigMap",
				"metadata": map[string]any{
					"name":      "test",
					"namespace": "default",
				},
			},
		}
		require.ErrorContains(t, serverSideApply(t.Context(), cl, obj), "missing apiVersion or kind")
	})
}
