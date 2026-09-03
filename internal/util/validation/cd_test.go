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

package validation

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func TestClusterDeployCredential(t *testing.T) {
	baseCD := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"},
		Spec:       kcmv1.ClusterDeploymentSpec{Credential: "cred1"},
	}

	t.Run("no providers in ClusterTemplate: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		err := ClusterDeployCredential(context.Background(), c, "kcm-system", baseCD, &kcmv1.ClusterTemplate{})
		if err == nil || !strings.Contains(err.Error(), "no providers have been found") {
			t.Fatalf("err = %v, want no-providers error", err)
		}
	})

	t.Run("no infrastructure provider in ClusterTemplate: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"bootstrap-k0s"}}}
		err := ClusterDeployCredential(context.Background(), c, "kcm-system", baseCD, clusterTemplate)
		if err == nil || !strings.Contains(err.Error(), "no infrastructure providers have been found") {
			t.Fatalf("err = %v, want no-infra-providers error", err)
		}
	})

	t.Run("Credential not found: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"infrastructure-aws"}}}
		err := ClusterDeployCredential(context.Background(), c, "kcm-system", baseCD, clusterTemplate)
		if err == nil || !strings.Contains(err.Error(), "failed to get Credential") {
			t.Fatalf("err = %v, want get Credential error", err)
		}
	})

	t.Run("Credential not Ready: error", func(t *testing.T) {
		cred := &kcmv1.Credential{ObjectMeta: metav1.ObjectMeta{Name: "cred1", Namespace: "ns1"}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(cred).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"infrastructure-aws"}}}
		err := ClusterDeployCredential(context.Background(), c, "kcm-system", baseCD, clusterTemplate)
		if err == nil || !strings.Contains(err.Error(), "is not Ready") {
			t.Fatalf("err = %v, want not-Ready error", err)
		}
	})

	t.Run("Credential missing identityRef: error", func(t *testing.T) {
		cred := &kcmv1.Credential{
			ObjectMeta: metav1.ObjectMeta{Name: "cred1", Namespace: "ns1"},
			Status:     kcmv1.CredentialStatus{Ready: true},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(cred).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"infrastructure-aws"}}}
		err := ClusterDeployCredential(context.Background(), c, "kcm-system", baseCD, clusterTemplate)
		if err == nil || !strings.Contains(err.Error(), "does not have identityRef") {
			t.Fatalf("err = %v, want missing-identityRef error", err)
		}
	})

	t.Run("infrastructure-internal provider requires Secret identity kind", func(t *testing.T) {
		cred := &kcmv1.Credential{
			ObjectMeta: metav1.ObjectMeta{Name: "cred1", Namespace: "ns1"},
			Spec:       kcmv1.CredentialSpec{IdentityRef: &corev1.ObjectReference{Kind: "SomeOtherKind"}},
			Status:     kcmv1.CredentialStatus{Ready: true},
		}
		mgmt := &kcmv1.Management{ObjectMeta: metav1.ObjectMeta{Name: kcmv1.ManagementName}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(cred, mgmt).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"infrastructure-internal"}}}
		err := ClusterDeployCredential(context.Background(), c, "kcm-system", baseCD, clusterTemplate)
		if err == nil || !strings.Contains(err.Error(), "does not support ClusterIdentity Kind") {
			t.Fatalf("err = %v, want unsupported identity kind error", err)
		}
	})

	t.Run("infrastructure-internal provider with Secret identity kind: success", func(t *testing.T) {
		cred := &kcmv1.Credential{
			ObjectMeta: metav1.ObjectMeta{Name: "cred1", Namespace: "ns1"},
			Spec:       kcmv1.CredentialSpec{IdentityRef: &corev1.ObjectReference{Kind: "Secret"}},
			Status:     kcmv1.CredentialStatus{Ready: true},
		}
		mgmt := &kcmv1.Management{ObjectMeta: metav1.ObjectMeta{Name: kcmv1.ManagementName}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(cred, mgmt).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"infrastructure-internal"}}}
		if err := ClusterDeployCredential(context.Background(), c, "kcm-system", baseCD, clusterTemplate); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("infrastructure provider with no matching ProviderInterface: unsupported provider error", func(t *testing.T) {
		cred := &kcmv1.Credential{
			ObjectMeta: metav1.ObjectMeta{Name: "cred1", Namespace: "ns1"},
			Spec:       kcmv1.CredentialSpec{IdentityRef: &corev1.ObjectReference{Kind: "AWSClusterStaticIdentity"}},
			Status:     kcmv1.CredentialStatus{Ready: true},
		}
		mgmt := &kcmv1.Management{ObjectMeta: metav1.ObjectMeta{Name: kcmv1.ManagementName}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(cred, mgmt).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"infrastructure-aws"}}}
		err := ClusterDeployCredential(context.Background(), c, "kcm-system", baseCD, clusterTemplate)
		if err == nil || !strings.Contains(err.Error(), "unsupported infrastructure provider") {
			t.Fatalf("err = %v, want unsupported infrastructure provider error", err)
		}
	})

	t.Run("infrastructure provider identity kind supported via ProviderInterface: success", func(t *testing.T) {
		cred := &kcmv1.Credential{
			ObjectMeta: metav1.ObjectMeta{Name: "cred1", Namespace: "ns1"},
			Spec:       kcmv1.CredentialSpec{IdentityRef: &corev1.ObjectReference{Kind: "AWSClusterStaticIdentity"}},
			Status:     kcmv1.CredentialStatus{Ready: true},
		}
		mgmt := &kcmv1.Management{ObjectMeta: metav1.ObjectMeta{Name: kcmv1.ManagementName}}
		pi := &kcmv1.ProviderInterface{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "aws-pi",
				Labels: map[string]string{clusterapiv1.ProviderNameLabel: "infrastructure-aws"},
			},
			Spec: kcmv1.ProviderInterfaceSpec{
				ClusterIdentities: []kcmv1.ClusterIdentity{
					{GroupVersionKind: kcmv1.GroupVersionKind{Kind: "AWSClusterStaticIdentity"}},
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(cred, mgmt, pi).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"infrastructure-aws"}}}
		if err := ClusterDeployCredential(context.Background(), c, "kcm-system", baseCD, clusterTemplate); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("infrastructure provider identity kind not supported by ProviderInterface: error", func(t *testing.T) {
		cred := &kcmv1.Credential{
			ObjectMeta: metav1.ObjectMeta{Name: "cred1", Namespace: "ns1"},
			Spec:       kcmv1.CredentialSpec{IdentityRef: &corev1.ObjectReference{Kind: "WrongKind"}},
			Status:     kcmv1.CredentialStatus{Ready: true},
		}
		mgmt := &kcmv1.Management{ObjectMeta: metav1.ObjectMeta{Name: kcmv1.ManagementName}}
		pi := &kcmv1.ProviderInterface{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "aws-pi",
				Labels: map[string]string{clusterapiv1.ProviderNameLabel: "infrastructure-aws"},
			},
			Spec: kcmv1.ProviderInterfaceSpec{
				ClusterIdentities: []kcmv1.ClusterIdentity{
					{GroupVersionKind: kcmv1.GroupVersionKind{Kind: "AWSClusterStaticIdentity"}},
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(cred, mgmt, pi).Build()

		clusterTemplate := &kcmv1.ClusterTemplate{Status: kcmv1.ClusterTemplateStatus{Providers: kcmv1.Providers{"infrastructure-aws"}}}
		err := ClusterDeployCredential(context.Background(), c, "kcm-system", baseCD, clusterTemplate)
		if err == nil || !strings.Contains(err.Error(), "does not support ClusterIdentity Kind") {
			t.Fatalf("err = %v, want unsupported identity kind error", err)
		}
	})
}

func TestClusterDeploymentDeletionAllowed(t *testing.T) {
	cld := &kcmv1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"}}

	t.Run("no Regions: allowed", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		if err := ClusterDeploymentDeletionAllowed(context.Background(), c, cld); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("Region without clusterDeployment ref: allowed", func(t *testing.T) {
		rgn := &kcmv1.Region{ObjectMeta: metav1.ObjectMeta{Name: "region1"}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(rgn).Build()

		if err := ClusterDeploymentDeletionAllowed(context.Background(), c, cld); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("Region referencing a different ClusterDeployment: allowed", func(t *testing.T) {
		rgn := &kcmv1.Region{
			ObjectMeta: metav1.ObjectMeta{Name: "region1"},
			Spec:       kcmv1.RegionSpec{ClusterDeployment: &kcmv1.ClusterDeploymentRef{Namespace: "ns1", Name: "other-cd"}},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(rgn).Build()

		if err := ClusterDeploymentDeletionAllowed(context.Background(), c, cld); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("Region references this ClusterDeployment: not allowed", func(t *testing.T) {
		rgn := &kcmv1.Region{
			ObjectMeta: metav1.ObjectMeta{Name: "region1"},
			Spec:       kcmv1.RegionSpec{ClusterDeployment: &kcmv1.ClusterDeploymentRef{Namespace: "ns1", Name: "cd1"}},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(rgn).Build()

		err := ClusterDeploymentDeletionAllowed(context.Background(), c, cld)
		if err == nil || !strings.Contains(err.Error(), "referenced by Region") {
			t.Fatalf("err = %v, want referenced-by-Region error", err)
		}
	})
}
