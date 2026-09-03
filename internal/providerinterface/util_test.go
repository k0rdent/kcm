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

package providerinterface

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func TestFindClusterIdentity(t *testing.T) {
	t.Run("no ProviderInterfaces returns ErrMissingClusterIdentityRef", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		_, err := FindClusterIdentity(context.Background(), c, &corev1.ObjectReference{})
		if !errors.Is(err, ErrMissingClusterIdentityRef) {
			t.Fatalf("err = %v, want ErrMissingClusterIdentityRef", err)
		}
	})

	t.Run("no matching identity returns ErrMissingClusterIdentityRef", func(t *testing.T) {
		pi := &kcmv1.ProviderInterface{
			ObjectMeta: metav1.ObjectMeta{Name: "pi1"},
			Spec: kcmv1.ProviderInterfaceSpec{
				ClusterIdentities: []kcmv1.ClusterIdentity{
					{GroupVersionKind: kcmv1.GroupVersionKind{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1", Kind: "AWSClusterStaticIdentity"}},
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(pi).Build()

		_, err := FindClusterIdentity(context.Background(), c, &corev1.ObjectReference{APIVersion: "other/v1", Kind: "Other"})
		if !errors.Is(err, ErrMissingClusterIdentityRef) {
			t.Fatalf("err = %v, want ErrMissingClusterIdentityRef", err)
		}
	})

	t.Run("matching identity is returned", func(t *testing.T) {
		want := kcmv1.ClusterIdentity{
			GroupVersionKind: kcmv1.GroupVersionKind{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1", Kind: "AWSClusterStaticIdentity"},
		}
		pi := &kcmv1.ProviderInterface{
			ObjectMeta: metav1.ObjectMeta{Name: "pi1"},
			Spec: kcmv1.ProviderInterfaceSpec{
				ClusterIdentities: []kcmv1.ClusterIdentity{want},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(pi).Build()

		got, err := FindClusterIdentity(context.Background(), c, &corev1.ObjectReference{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
			Kind:       "AWSClusterStaticIdentity",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Kind != want.Kind || got.Group != want.Group {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})
}

func TestFindComponentForInfra(t *testing.T) {
	components := map[string]kcmv1.ComponentStatus{
		"aws-provider": {ExposedProviders: kcmv1.Providers{"aws"}},
		"gcp-provider": {ExposedProviders: kcmv1.Providers{"gcp"}},
	}

	if got := findComponentForInfra(components, "gcp"); got != "gcp-provider" {
		t.Errorf("findComponentForInfra() = %q, want %q", got, "gcp-provider")
	}
	if got := findComponentForInfra(components, "azure"); got != "" {
		t.Errorf("findComponentForInfra() = %q, want empty", got)
	}
}

func TestFindProviderInterfaceForInfra(t *testing.T) {
	t.Run("found via CAPI provider label", func(t *testing.T) {
		pi := &kcmv1.ProviderInterface{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "aws-pi",
				Labels: map[string]string{clusterapiv1.ProviderNameLabel: "aws"},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(pi).Build()

		got := FindProviderInterfaceForInfra(context.Background(), c, &kcmv1.Region{}, "aws")
		if got == nil || got.Name != "aws-pi" {
			t.Errorf("got %v, want aws-pi", got)
		}
	})

	t.Run("no CAPI label match, no component exposes infra: returns nil", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()
		region := &kcmv1.Region{}

		got := FindProviderInterfaceForInfra(context.Background(), c, region, "aws")
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("falls back to flux helm-chart-name label", func(t *testing.T) {
		region := &kcmv1.Region{
			ObjectMeta: metav1.ObjectMeta{Name: "region1"},
			Status: kcmv1.RegionStatus{
				ComponentsCommonStatus: kcmv1.ComponentsCommonStatus{
					Components: map[string]kcmv1.ComponentStatus{
						"aws-provider": {ExposedProviders: kcmv1.Providers{"aws"}},
					},
				},
			},
		}

		pi := &kcmv1.ProviderInterface{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "aws-pi-legacy",
				Labels: map[string]string{kcmv1.FluxHelmChartNameKey: "region1-aws-provider"},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(pi).Build()

		got := FindProviderInterfaceForInfra(context.Background(), c, region, "aws")
		if got == nil || got.Name != "aws-pi-legacy" {
			t.Errorf("got %v, want aws-pi-legacy", got)
		}
	})

	t.Run("falls back but finds nothing: returns nil", func(t *testing.T) {
		region := &kcmv1.Region{
			ObjectMeta: metav1.ObjectMeta{Name: "region1"},
			Status: kcmv1.RegionStatus{
				ComponentsCommonStatus: kcmv1.ComponentsCommonStatus{
					Components: map[string]kcmv1.ComponentStatus{
						"aws-provider": {ExposedProviders: kcmv1.Providers{"aws"}},
					},
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		got := FindProviderInterfaceForInfra(context.Background(), c, region, "aws")
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}
