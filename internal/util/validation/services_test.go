// Copyright 2024
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

package validation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	discoveryfake "k8s.io/client-go/discovery/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

func TestValidateServiceDependency(t *testing.T) {
	for _, tc := range []struct {
		name        string
		services    []kcmv1.Service
		expectedErr string
	}{
		{
			name: "empty",
		},
		{
			name: "happy path",
			services: []kcmv1.Service{
				{Namespace: "A", Name: "a"},
				{Namespace: "B", Name: "b"},
				{Namespace: "C", Name: "c"},
			},
		},
		{
			name: "dependency that is not defined as a service",
			services: []kcmv1.Service{
				{Namespace: "A", Name: "a", DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "C", Name: "c"}}},
				{Namespace: "B", Name: "b"},
			},
			expectedErr: "dependency C/c of service A/a is not defined as a service",
		},
		{
			name: "multiple dependencies that are not defined as services",
			services: []kcmv1.Service{
				{Namespace: "A", Name: "a", DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "C", Name: "c"}, {Namespace: "D", Name: "d"}}},
				{Namespace: "B", Name: "b"},
			},
			expectedErr: "dependency C/c of service A/a is not defined as a service" +
				"\n" + "dependency D/d of service A/a is not defined as a service",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateServiceDependency(tc.services); err != nil {
				require.EqualError(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateServiceDependencyCycle(t *testing.T) {
	for _, tc := range []struct {
		testName string
		services []kcmv1.Service
		isErr    bool
	}{
		{
			testName: "empty",
			services: []kcmv1.Service{},
		},
		{
			testName: "single service",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
			},
		},
		{
			testName: "single service illegally repeated but no cycle",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
				{
					Namespace: "A", Name: "a",
				},
			},
		},
		{
			testName: "services A->B",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}},
				},
				{
					Namespace: "B", Name: "b",
				},
			},
		},
		{
			testName: "services B->A",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
			},
		},
		{
			testName: "services B->A",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
			},
		},
		{
			testName: "services A->A",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
			},
			isErr: true,
		},
		{
			testName: "services A<->B",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}},
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
			},
			isErr: true,
		},
		{
			testName: "services A, C<->B",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "C", Name: "c"}},
				},
				{
					Namespace: "C", Name: "c",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}},
				},
			},
			isErr: true,
		},
		{
			testName: "services C<->B->A",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}, {Namespace: "C", Name: "c"}},
				},
				{
					Namespace: "C", Name: "c",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}},
				},
			},
			isErr: true,
		},
		{
			testName: "services BC->A, D->BC",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
				{
					Namespace: "C", Name: "c",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
				{
					Namespace: "D", Name: "d",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}, {Namespace: "C", Name: "c"}},
				},
			},
		},
		{
			testName: "services A->BC, B->DE, C, D, E",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}, {Namespace: "C", Name: "c"}},
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "D", Name: "d"}, {Namespace: "E", Name: "e"}},
				},
				{
					Namespace: "C", Name: "c",
				},
				{
					Namespace: "D", Name: "d",
				},
				{
					Namespace: "E", Name: "e",
				},
			},
		},
		{
			testName: "services A->BC, B->DE, C, D, E->A",
			services: []kcmv1.Service{
				{
					Namespace: "A", Name: "a",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}, {Namespace: "C", Name: "c"}},
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "D", Name: "d"}, {Namespace: "E", Name: "e"}},
				},
				{
					Namespace: "C", Name: "c",
				},
				{
					Namespace: "D", Name: "d",
				},
				{
					Namespace: "E", Name: "e",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
			},
			isErr: true,
		},
		{
			testName: "services C, A->BC, D, B->DE, E->A",
			services: []kcmv1.Service{
				{
					Namespace: "C", Name: "c",
				},
				{
					Namespace: "A", Name: "a",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "B", Name: "b"}, {Namespace: "C", Name: "c"}},
				},
				{
					Namespace: "D", Name: "d",
				},
				{
					Namespace: "B", Name: "b",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "D", Name: "d"}, {Namespace: "E", Name: "e"}},
				},
				{
					Namespace: "E", Name: "e",
					DependsOn: []kcmv1.ServiceDependsOn{{Namespace: "A", Name: "a"}},
				},
			},
			isErr: true,
		},
	} {
		t.Run(tc.testName, func(t *testing.T) {
			err := validateServiceDependencyCycle(tc.services)
			if tc.isErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func newValidationScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, kcmv1.AddToScheme(s))
	require.NoError(t, apiextv1.AddToScheme(s))
	return s
}

func fakeDiscoveryWithResources(t *testing.T, resources []*metav1.APIResourceList) *discoveryfake.FakeDiscovery {
	t.Helper()
	clientset := kubefake.NewClientset()
	disco, ok := clientset.Discovery().(*discoveryfake.FakeDiscovery)
	if !ok {
		t.Fatal("failed to cast to FakeDiscovery")
	}
	disco.Resources = resources
	return disco
}

func TestValidateConfigAgainstSchema(t *testing.T) {
	schema := &apiextensions.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensions.JSONSchemaProps{
			"priority": {Type: "integer"},
		},
	}

	for _, tc := range []struct {
		name    string
		raw     json.RawMessage
		wantErr string
	}{
		{
			name:    "empty raw returns error",
			wantErr: `provider "p" config is empty`,
		},
		{
			name: "valid config passes",
			raw:  json.RawMessage(`{"priority": 1}`),
		},
		{
			name:    "invalid type fails",
			raw:     json.RawMessage(`{"priority": "not-an-integer"}`),
			wantErr: `provider "p" config validation failed`,
		},
		{
			name:    "malformed JSON fails",
			raw:     json.RawMessage(`{bad`),
			wantErr: "failed to unmarshal provider config",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfigAgainstSchema(tc.raw, schema, "p")
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestResolveCRDNameFiltersSubresources(t *testing.T) {
	gvk := kcmv1.GroupVersionKind{
		Group:   "example.com",
		Version: "v1",
		Kind:    "Foo",
	}

	t.Run("subresource entry is skipped, primary resource is found", func(t *testing.T) {
		disco := fakeDiscoveryWithResources(t, []*metav1.APIResourceList{
			{
				GroupVersion: "example.com/v1",
				APIResources: []metav1.APIResource{
					{Name: "foos/status", Kind: "Foo"},
					{Name: "foos", Kind: "Foo"},
				},
			},
		})
		name, err := resolveCRDName(disco, gvk)
		require.NoError(t, err)
		assert.Equal(t, "foos.example.com", name)
	})

	t.Run("only subresource present returns error", func(t *testing.T) {
		disco := fakeDiscoveryWithResources(t, []*metav1.APIResourceList{
			{
				GroupVersion: "example.com/v1",
				APIResources: []metav1.APIResource{
					{Name: "foos/status", Kind: "Foo"},
				},
			},
		})
		_, err := resolveCRDName(disco, gvk)
		require.Error(t, err)
	})

	t.Run("group version not found returns error", func(t *testing.T) {
		disco := fakeDiscoveryWithResources(t, []*metav1.APIResourceList{})
		_, err := resolveCRDName(disco, gvk)
		require.Error(t, err)
	})
}

func TestServicesHaveValidProviderConfiguration(t *testing.T) {
	ctx := context.Background()

	t.Run("nil Provider.Config skips validation", func(t *testing.T) {
		s := newValidationScheme(t)
		c := clientfake.NewClientBuilder().WithScheme(s).Build()
		err := ServicesHaveValidProviderConfiguration(ctx, c, nil, kcmv1.ServiceSpec{})
		require.NoError(t, err)
	})

	t.Run("nil ConfigSchemaRef on provider skips validation", func(t *testing.T) {
		s := newValidationScheme(t)
		provider := &kcmv1.StateManagementProvider{
			ObjectMeta: metav1.ObjectMeta{Name: kubeutil.DefaultStateManagementProvider},
		}
		c := clientfake.NewClientBuilder().WithScheme(s).WithObjects(provider).Build()
		err := ServicesHaveValidProviderConfiguration(ctx, c, nil, kcmv1.ServiceSpec{
			Provider: kcmv1.StateManagementProviderConfig{
				Config: &apiextv1.JSON{Raw: []byte(`{"priority": 1}`)},
			},
		})
		require.NoError(t, err)
	})

	t.Run("valid config against schema passes", func(t *testing.T) {
		s := newValidationScheme(t)

		crd := &apiextv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: "profileconfigs.k0rdent.mirantis.com"},
			Spec: apiextv1.CustomResourceDefinitionSpec{
				Versions: []apiextv1.CustomResourceDefinitionVersion{
					{
						Name: kcmv1.GroupVersion.Version,
						Schema: &apiextv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextv1.JSONSchemaProps{
								Type: "object",
								Properties: map[string]apiextv1.JSONSchemaProps{
									"priority": {Type: "integer"},
								},
							},
						},
					},
				},
			},
		}
		provider := &kcmv1.StateManagementProvider{
			ObjectMeta: metav1.ObjectMeta{Name: kubeutil.DefaultStateManagementProvider},
			Spec: kcmv1.StateManagementProviderSpec{
				ConfigSchemaRef: &kcmv1.GroupVersionKind{
					Group:   kcmv1.GroupVersion.Group,
					Version: kcmv1.GroupVersion.Version,
					Kind:    kcmv1.ProfileConfigKind,
				},
			},
		}
		c := clientfake.NewClientBuilder().WithScheme(s).WithObjects(provider, crd).Build()
		disco := fakeDiscoveryWithResources(t, []*metav1.APIResourceList{
			{
				GroupVersion: kcmv1.GroupVersion.String(),
				APIResources: []metav1.APIResource{
					{Name: "profileconfigs", Kind: kcmv1.ProfileConfigKind},
				},
			},
		})

		err := ServicesHaveValidProviderConfiguration(ctx, c, disco, kcmv1.ServiceSpec{
			Provider: kcmv1.StateManagementProviderConfig{
				Config: &apiextv1.JSON{Raw: []byte(`{"priority": 5}`)},
			},
		})
		require.NoError(t, err)
	})

	t.Run("invalid config against schema fails", func(t *testing.T) {
		s := newValidationScheme(t)

		crd := &apiextv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: "profileconfigs.k0rdent.mirantis.com"},
			Spec: apiextv1.CustomResourceDefinitionSpec{
				Versions: []apiextv1.CustomResourceDefinitionVersion{
					{
						Name: kcmv1.GroupVersion.Version,
						Schema: &apiextv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextv1.JSONSchemaProps{
								Type: "object",
								Properties: map[string]apiextv1.JSONSchemaProps{
									"priority": {Type: "integer"},
								},
							},
						},
					},
				},
			},
		}
		provider := &kcmv1.StateManagementProvider{
			ObjectMeta: metav1.ObjectMeta{Name: kubeutil.DefaultStateManagementProvider},
			Spec: kcmv1.StateManagementProviderSpec{
				ConfigSchemaRef: &kcmv1.GroupVersionKind{
					Group:   kcmv1.GroupVersion.Group,
					Version: kcmv1.GroupVersion.Version,
					Kind:    kcmv1.ProfileConfigKind,
				},
			},
		}
		c := clientfake.NewClientBuilder().WithScheme(s).WithObjects(provider, crd).Build()
		disco := fakeDiscoveryWithResources(t, []*metav1.APIResourceList{
			{
				GroupVersion: kcmv1.GroupVersion.String(),
				APIResources: []metav1.APIResource{
					{Name: "profileconfigs", Kind: kcmv1.ProfileConfigKind},
				},
			},
		})

		err := ServicesHaveValidProviderConfiguration(ctx, c, disco, kcmv1.ServiceSpec{
			Provider: kcmv1.StateManagementProviderConfig{
				Config: &apiextv1.JSON{Raw: []byte(`{"priority": "not-an-integer"}`)},
			},
		})
		require.ErrorContains(t, err, "config validation failed")
	})

	t.Run("CRD not found in discovery returns error", func(t *testing.T) {
		s := newValidationScheme(t)
		provider := &kcmv1.StateManagementProvider{
			ObjectMeta: metav1.ObjectMeta{Name: kubeutil.DefaultStateManagementProvider},
			Spec: kcmv1.StateManagementProviderSpec{
				ConfigSchemaRef: &kcmv1.GroupVersionKind{
					Group:   kcmv1.GroupVersion.Group,
					Version: kcmv1.GroupVersion.Version,
					Kind:    kcmv1.ProfileConfigKind,
				},
			},
		}
		c := clientfake.NewClientBuilder().WithScheme(s).WithObjects(provider).Build()
		disco := fakeDiscoveryWithResources(t, []*metav1.APIResourceList{})

		err := ServicesHaveValidProviderConfiguration(ctx, c, disco, kcmv1.ServiceSpec{
			Provider: kcmv1.StateManagementProviderConfig{
				Config: &apiextv1.JSON{Raw: []byte(`{"priority": 1}`)},
			},
		})
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to resolve CRD name")
	})
}
