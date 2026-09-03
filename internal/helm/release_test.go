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

package helm

import (
	"context"
	"testing"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func TestReleaseName(t *testing.T) {
	tests := []struct {
		prefix, name, want string
	}{
		{prefix: "region1", name: "kcm", want: "region1-kcm"},
		{prefix: "", name: "kcm", want: "kcm"},
	}
	for _, tt := range tests {
		if got := ReleaseName(tt.prefix, tt.name); got != tt.want {
			t.Errorf("ReleaseName(%q, %q) = %q, want %q", tt.prefix, tt.name, got, tt.want)
		}
	}
}

func TestReconcileHelmRelease(t *testing.T) {
	t.Run("creates a new HelmRelease with defaults and required fields", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		hr, op, err := ReconcileHelmRelease(context.Background(), c, "release1", "ns1", ReconcileHelmReleaseOpts{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if op != controllerutil.OperationResultCreated {
			t.Errorf("op = %v, want Created", op)
		}
		if hr.Labels[kcmv1.KCMManagedLabelKey] != kcmv1.KCMManagedLabelValue {
			t.Errorf("managed label not set: %+v", hr.Labels)
		}
		if hr.Spec.Interval.Duration != DefaultReconcileInterval {
			t.Errorf("Interval = %v, want default %v", hr.Spec.Interval.Duration, DefaultReconcileInterval)
		}
		if hr.Spec.ReleaseName != "release1" {
			t.Errorf("ReleaseName = %q, want %q", hr.Spec.ReleaseName, "release1")
		}
	})

	t.Run("applies all optional fields", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		owner := &metav1.OwnerReference{APIVersion: "v1", Kind: "ConfigMap", Name: "owner1", UID: types.UID("uid1")}
		chartRef := &helmcontrollerv2.CrossNamespaceSourceReference{Kind: "HelmChart", Name: "chart1"}
		interval := 5 * time.Minute
		install := &helmcontrollerv2.Install{Remediation: &helmcontrollerv2.InstallRemediation{Retries: 3}}
		upgrade := &helmcontrollerv2.Upgrade{Remediation: &helmcontrollerv2.UpgradeRemediation{Retries: 3}}
		kubeConfigRef := &fluxmeta.SecretKeyReference{Name: "kubeconfig-secret"}
		values := &apiextv1.JSON{Raw: []byte(`{"key":"value"}`)}
		dependsOn := []helmcontrollerv2.DependencyReference{{Name: "dep1"}}

		hr, op, err := ReconcileHelmRelease(context.Background(), c, "release2", "ns1", ReconcileHelmReleaseOpts{
			Values:            values,
			OwnerReference:    owner,
			ChartRef:          chartRef,
			ReconcileInterval: &interval,
			Install:           install,
			Upgrade:           upgrade,
			KubeConfigRef:     kubeConfigRef,
			Labels:            map[string]string{"extra": "label"},
			ReleaseName:       "custom-release-name",
			TargetNamespace:   "target-ns",
			DependsOn:         dependsOn,
			Timeout:           30 * time.Second,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if op != controllerutil.OperationResultCreated {
			t.Errorf("op = %v, want Created", op)
		}
		if hr.Labels["extra"] != "label" {
			t.Errorf("extra label missing: %+v", hr.Labels)
		}
		if len(hr.OwnerReferences) != 1 || hr.OwnerReferences[0].Name != "owner1" {
			t.Errorf("OwnerReferences = %+v", hr.OwnerReferences)
		}
		if hr.Spec.ChartRef != chartRef {
			t.Errorf("ChartRef = %+v, want %+v", hr.Spec.ChartRef, chartRef)
		}
		if hr.Spec.Interval.Duration != interval {
			t.Errorf("Interval = %v, want %v", hr.Spec.Interval.Duration, interval)
		}
		if hr.Spec.ReleaseName != "custom-release-name" {
			t.Errorf("ReleaseName = %q, want %q", hr.Spec.ReleaseName, "custom-release-name")
		}
		if hr.Spec.Values == nil || string(hr.Spec.Values.Raw) != `{"key":"value"}` {
			t.Errorf("Values = %+v", hr.Spec.Values)
		}
		if len(hr.Spec.DependsOn) != 1 || hr.Spec.DependsOn[0].Name != "dep1" {
			t.Errorf("DependsOn = %+v", hr.Spec.DependsOn)
		}
		if hr.Spec.TargetNamespace != "target-ns" {
			t.Errorf("TargetNamespace = %q, want %q", hr.Spec.TargetNamespace, "target-ns")
		}
		if hr.Spec.Timeout == nil || hr.Spec.Timeout.Duration != 30*time.Second {
			t.Errorf("Timeout = %+v", hr.Spec.Timeout)
		}
		if hr.Spec.Install != install {
			t.Errorf("Install = %+v, want %+v", hr.Spec.Install, install)
		}
		if hr.Spec.Upgrade != upgrade {
			t.Errorf("Upgrade = %+v, want %+v", hr.Spec.Upgrade, upgrade)
		}
		if hr.Spec.KubeConfig == nil || hr.Spec.KubeConfig.SecretRef != kubeConfigRef {
			t.Errorf("KubeConfig = %+v", hr.Spec.KubeConfig)
		}
	})

	t.Run("updates an existing HelmRelease and preserves existing labels", func(t *testing.T) {
		existing := &helmcontrollerv2.HelmRelease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "release3",
				Namespace: "ns1",
				Labels:    map[string]string{"preexisting": "label"},
			},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(existing).Build()

		hr, op, err := ReconcileHelmRelease(context.Background(), c, "release3", "ns1", ReconcileHelmReleaseOpts{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if op != controllerutil.OperationResultUpdated {
			t.Errorf("op = %v, want Updated", op)
		}
		if hr.Labels["preexisting"] != "label" {
			t.Errorf("preexisting label lost: %+v", hr.Labels)
		}
		if hr.Labels[kcmv1.KCMManagedLabelKey] != kcmv1.KCMManagedLabelValue {
			t.Errorf("managed label not set: %+v", hr.Labels)
		}
	})
}

func TestDeleteHelmRelease(t *testing.T) {
	t.Run("deletes an existing HelmRelease", func(t *testing.T) {
		existing := &helmcontrollerv2.HelmRelease{
			ObjectMeta: metav1.ObjectMeta{Name: "release1", Namespace: "ns1"},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(existing).Build()

		if err := DeleteHelmRelease(context.Background(), c, "release1", "ns1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		err := c.Get(context.Background(), client.ObjectKey{Name: "release1", Namespace: "ns1"}, &helmcontrollerv2.HelmRelease{})
		if !apierrors.IsNotFound(err) {
			t.Errorf("expected NotFound after delete, got: %v", err)
		}
	})

	t.Run("deleting a non-existent HelmRelease is a no-op", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		if err := DeleteHelmRelease(context.Background(), c, "missing", "ns1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
