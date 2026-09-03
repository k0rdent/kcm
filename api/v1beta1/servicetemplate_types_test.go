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

package v1beta1

import (
	"testing"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestServiceTemplateFillStatusWithProviders(t *testing.T) {
	t.Run("no constraint anywhere: no-op", func(t *testing.T) {
		st := &ServiceTemplate{}
		if err := st.FillStatusWithProviders(nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if st.Status.KubernetesConstraint != "" {
			t.Errorf("got %q, want empty", st.Status.KubernetesConstraint)
		}
	})

	t.Run("constraint from annotation", func(t *testing.T) {
		st := &ServiceTemplate{}
		if err := st.FillStatusWithProviders(map[string]string{ChartAnnotationKubernetesConstraint: ">= 1.29.0"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if st.Status.KubernetesConstraint != ">= 1.29.0" {
			t.Errorf("got %q, want >= 1.29.0", st.Status.KubernetesConstraint)
		}
	})

	t.Run("constraint from spec overrides annotation", func(t *testing.T) {
		st := &ServiceTemplate{Spec: ServiceTemplateSpec{KubernetesConstraint: ">= 1.30.0"}}
		if err := st.FillStatusWithProviders(map[string]string{ChartAnnotationKubernetesConstraint: ">= 1.29.0"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if st.Status.KubernetesConstraint != ">= 1.30.0" {
			t.Errorf("got %q, want >= 1.30.0", st.Status.KubernetesConstraint)
		}
	})

	t.Run("invalid constraint returns error", func(t *testing.T) {
		st := &ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "st1", Namespace: "ns1"},
			Spec:       ServiceTemplateSpec{KubernetesConstraint: "not-a-constraint!!"},
		}
		if err := st.FillStatusWithProviders(nil); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestServiceTemplateGetHelmSpec(t *testing.T) {
	t.Run("helm set", func(t *testing.T) {
		helm := &HelmSpec{ChartSpec: &sourcev1.HelmChartSpec{Chart: "mychart"}}
		st := &ServiceTemplate{Spec: ServiceTemplateSpec{Helm: helm}}
		if got := st.GetHelmSpec(); got != helm {
			t.Errorf("got %v, want %v", got, helm)
		}
	})

	t.Run("helm nil", func(t *testing.T) {
		st := &ServiceTemplate{}
		if got := st.GetHelmSpec(); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestServiceTemplateGetCommonStatus(t *testing.T) {
	st := &ServiceTemplate{Status: ServiceTemplateStatus{
		TemplateStatusCommon: TemplateStatusCommon{ChartVersion: "1.0.0"},
	}}
	got := st.GetCommonStatus()
	if got != &st.Status.TemplateStatusCommon {
		t.Error("GetCommonStatus() did not return a pointer to Status.TemplateStatusCommon")
	}
}

func TestServiceTemplateHelmChartSpecAndRef(t *testing.T) {
	t.Run("no helm: both nil", func(t *testing.T) {
		st := &ServiceTemplate{}
		if got := st.HelmChartSpec(); got != nil {
			t.Errorf("HelmChartSpec() = %v, want nil", got)
		}
		if got := st.HelmChartRef(); got != nil {
			t.Errorf("HelmChartRef() = %v, want nil", got)
		}
	})

	t.Run("helm with ChartSpec and ChartRef", func(t *testing.T) {
		chartSpec := &sourcev1.HelmChartSpec{Chart: "mychart"}
		chartRef := &helmcontrollerv2.CrossNamespaceSourceReference{Name: "chart1"}
		st := &ServiceTemplate{Spec: ServiceTemplateSpec{Helm: &HelmSpec{ChartSpec: chartSpec, ChartRef: chartRef}}}

		if got := st.HelmChartSpec(); got != chartSpec {
			t.Errorf("HelmChartSpec() = %v, want %v", got, chartSpec)
		}
		if got := st.HelmChartRef(); got != chartRef {
			t.Errorf("HelmChartRef() = %v, want %v", got, chartRef)
		}
	})
}

func TestServiceTemplateLocalSourceRefAndRemoteSourceSpec(t *testing.T) {
	localRef := &LocalSourceRef{Kind: ConfigMapKind, Name: "cm1"}
	remoteSpec := &RemoteSourceSpec{Git: &EmbeddedGitRepositorySpec{}}

	t.Run("from Helm.ChartSource", func(t *testing.T) {
		st := &ServiceTemplate{Spec: ServiceTemplateSpec{Helm: &HelmSpec{ChartSource: &SourceSpec{LocalSourceRef: localRef, RemoteSourceSpec: remoteSpec}}}}
		if got := st.LocalSourceRef(); got != localRef {
			t.Errorf("LocalSourceRef() = %v, want %v", got, localRef)
		}
		if got := st.RemoteSourceSpec(); got != remoteSpec {
			t.Errorf("RemoteSourceSpec() = %v, want %v", got, remoteSpec)
		}
	})

	t.Run("from Kustomize", func(t *testing.T) {
		st := &ServiceTemplate{Spec: ServiceTemplateSpec{Kustomize: &SourceSpec{LocalSourceRef: localRef, RemoteSourceSpec: remoteSpec}}}
		if got := st.LocalSourceRef(); got != localRef {
			t.Errorf("LocalSourceRef() = %v, want %v", got, localRef)
		}
		if got := st.RemoteSourceSpec(); got != remoteSpec {
			t.Errorf("RemoteSourceSpec() = %v, want %v", got, remoteSpec)
		}
	})

	t.Run("from Resources", func(t *testing.T) {
		st := &ServiceTemplate{Spec: ServiceTemplateSpec{Resources: &SourceSpec{LocalSourceRef: localRef, RemoteSourceSpec: remoteSpec}}}
		if got := st.LocalSourceRef(); got != localRef {
			t.Errorf("LocalSourceRef() = %v, want %v", got, localRef)
		}
		if got := st.RemoteSourceSpec(); got != remoteSpec {
			t.Errorf("RemoteSourceSpec() = %v, want %v", got, remoteSpec)
		}
	})

	t.Run("none set: nil", func(t *testing.T) {
		st := &ServiceTemplate{}
		if got := st.LocalSourceRef(); got != nil {
			t.Errorf("LocalSourceRef() = %v, want nil", got)
		}
		if got := st.RemoteSourceSpec(); got != nil {
			t.Errorf("RemoteSourceSpec() = %v, want nil", got)
		}
	})
}

func TestServiceTemplateLocalSourceObject(t *testing.T) {
	t.Run("no local source ref: nil", func(t *testing.T) {
		st := &ServiceTemplate{}
		obj, kind := st.LocalSourceObject()
		if obj != nil || kind != "" {
			t.Errorf("got (%v, %q), want (nil, \"\")", obj, kind)
		}
	})

	t.Run("Secret: namespace defaults to template namespace regardless of ref namespace", func(t *testing.T) {
		st := &ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Namespace: "tmpl-ns"},
			Spec:       ServiceTemplateSpec{Kustomize: &SourceSpec{LocalSourceRef: &LocalSourceRef{Kind: SecretKind, Name: "sec1", Namespace: "ignored-ns"}}},
		}
		obj, kind := st.LocalSourceObject()
		if kind != SecretKind {
			t.Fatalf("kind = %q, want %q", kind, SecretKind)
		}
		secret, ok := obj.(*corev1.Secret)
		if !ok || secret.Name != "sec1" || secret.Namespace != "tmpl-ns" {
			t.Errorf("got %+v", obj)
		}
	})

	t.Run("ConfigMap", func(t *testing.T) {
		st := &ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Namespace: "tmpl-ns"},
			Spec:       ServiceTemplateSpec{Kustomize: &SourceSpec{LocalSourceRef: &LocalSourceRef{Kind: ConfigMapKind, Name: "cm1"}}},
		}
		obj, kind := st.LocalSourceObject()
		if kind != ConfigMapKind {
			t.Fatalf("kind = %q, want %q", kind, ConfigMapKind)
		}
		if _, ok := obj.(*corev1.ConfigMap); !ok {
			t.Errorf("got %T, want *corev1.ConfigMap", obj)
		}
	})

	t.Run("GitRepository: cross-namespace ref respected", func(t *testing.T) {
		st := &ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Namespace: "tmpl-ns"},
			Spec: ServiceTemplateSpec{Kustomize: &SourceSpec{LocalSourceRef: &LocalSourceRef{
				Kind: sourcev1.GitRepositoryKind, Name: "repo1", Namespace: "other-ns",
			}}},
		}
		obj, kind := st.LocalSourceObject()
		if kind != sourcev1.GitRepositoryKind {
			t.Fatalf("kind = %q, want %q", kind, sourcev1.GitRepositoryKind)
		}
		repo, ok := obj.(*sourcev1.GitRepository)
		if !ok || repo.Namespace != "other-ns" {
			t.Errorf("got %+v", obj)
		}
	})

	t.Run("Bucket", func(t *testing.T) {
		st := &ServiceTemplate{Spec: ServiceTemplateSpec{Kustomize: &SourceSpec{LocalSourceRef: &LocalSourceRef{Kind: sourcev1.BucketKind, Name: "b1"}}}}
		obj, kind := st.LocalSourceObject()
		if kind != sourcev1.BucketKind {
			t.Fatalf("kind = %q", kind)
		}
		if _, ok := obj.(*sourcev1.Bucket); !ok {
			t.Errorf("got %T, want *sourcev1.Bucket", obj)
		}
	})

	t.Run("OCIRepository", func(t *testing.T) {
		st := &ServiceTemplate{Spec: ServiceTemplateSpec{Kustomize: &SourceSpec{LocalSourceRef: &LocalSourceRef{Kind: sourcev1.OCIRepositoryKind, Name: "oci1"}}}}
		obj, kind := st.LocalSourceObject()
		if kind != sourcev1.OCIRepositoryKind {
			t.Fatalf("kind = %q", kind)
		}
		if _, ok := obj.(*sourcev1.OCIRepository); !ok {
			t.Errorf("got %T, want *sourcev1.OCIRepository", obj)
		}
	})

	t.Run("unknown kind: nil", func(t *testing.T) {
		st := &ServiceTemplate{Spec: ServiceTemplateSpec{Kustomize: &SourceSpec{LocalSourceRef: &LocalSourceRef{Kind: "Unknown", Name: "x"}}}}
		obj, kind := st.LocalSourceObject()
		if obj != nil || kind != "" {
			t.Errorf("got (%v, %q), want (nil, \"\")", obj, kind)
		}
	})
}

func TestServiceTemplateRemoteSourceObject(t *testing.T) {
	t.Run("no remote source: nil", func(t *testing.T) {
		st := &ServiceTemplate{}
		obj, kind := st.RemoteSourceObject()
		if obj != nil || kind != "" {
			t.Errorf("got (%v, %q), want (nil, \"\")", obj, kind)
		}
	})

	t.Run("Git", func(t *testing.T) {
		st := &ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "st1", Namespace: "ns1"},
			Spec: ServiceTemplateSpec{Kustomize: &SourceSpec{RemoteSourceSpec: &RemoteSourceSpec{
				Git: &EmbeddedGitRepositorySpec{GitRepositorySpec: sourcev1.GitRepositorySpec{URL: "https://example.com/repo.git"}},
			}}},
		}
		obj, kind := st.RemoteSourceObject()
		if kind != sourcev1.GitRepositoryKind {
			t.Fatalf("kind = %q", kind)
		}
		repo, ok := obj.(*sourcev1.GitRepository)
		if !ok || repo.Name != "st1" || repo.Namespace != "ns1" || repo.Spec.URL != "https://example.com/repo.git" {
			t.Errorf("got %+v", obj)
		}
		if repo.Labels[KCMManagedLabelKey] != KCMManagedLabelValue {
			t.Errorf("missing managed label: %+v", repo.Labels)
		}
	})

	t.Run("Bucket", func(t *testing.T) {
		st := &ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "st1", Namespace: "ns1"},
			Spec: ServiceTemplateSpec{Kustomize: &SourceSpec{RemoteSourceSpec: &RemoteSourceSpec{
				Bucket: &EmbeddedBucketSpec{BucketSpec: sourcev1.BucketSpec{BucketName: "my-bucket"}},
			}}},
		}
		obj, kind := st.RemoteSourceObject()
		if kind != sourcev1.BucketKind {
			t.Fatalf("kind = %q", kind)
		}
		bucket, ok := obj.(*sourcev1.Bucket)
		if !ok || bucket.Spec.BucketName != "my-bucket" {
			t.Errorf("got %+v", obj)
		}
	})

	t.Run("OCI", func(t *testing.T) {
		st := &ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "st1", Namespace: "ns1"},
			Spec: ServiceTemplateSpec{Kustomize: &SourceSpec{RemoteSourceSpec: &RemoteSourceSpec{
				OCI: &EmbeddedOCIRepositorySpec{OCIRepositorySpec: sourcev1.OCIRepositorySpec{URL: "oci://example.com/repo"}},
			}}},
		}
		obj, kind := st.RemoteSourceObject()
		if kind != sourcev1.OCIRepositoryKind {
			t.Fatalf("kind = %q", kind)
		}
		oci, ok := obj.(*sourcev1.OCIRepository)
		if !ok || oci.Spec.URL != "oci://example.com/repo" {
			t.Errorf("got %+v", obj)
		}
	})
}
