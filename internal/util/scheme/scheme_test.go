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

package scheme

import (
	"testing"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func TestGetRegionalScheme(t *testing.T) {
	s, err := GetRegionalScheme()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, obj := range []struct {
		name string
		obj  runtime.Object
	}{
		{"corev1.Pod", &corev1.Pod{}},
		{"clusterapiv1.Cluster", &clusterapiv1.Cluster{}},
		{"kcmv1.ProviderInterface", &kcmv1.ProviderInterface{}},
	} {
		if _, _, err := s.ObjectKinds(obj.obj); err != nil {
			t.Errorf("%s not registered in regional scheme: %v", obj.name, err)
		}
	}

	if _, _, err := s.ObjectKinds(&addoncontrollerv1beta1.ClusterProfile{}); err == nil {
		t.Error("sveltos ClusterProfile should not be registered in plain regional scheme")
	}

	if _, _, err := s.ObjectKinds(&kcmv1.ClusterDeployment{}); err == nil {
		t.Error("kcmv1.ClusterDeployment should not be registered in regional scheme (only added at management level)")
	}
}

func TestGetRegionalSchemeWithSveltos(t *testing.T) {
	s, err := GetRegionalSchemeWithSveltos()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, _, err := s.ObjectKinds(&addoncontrollerv1beta1.ClusterProfile{}); err != nil {
		t.Errorf("sveltos ClusterProfile not registered: %v", err)
	}
	if _, _, err := s.ObjectKinds(&kcmv1.ProviderInterface{}); err != nil {
		t.Errorf("kcmv1.ProviderInterface not registered: %v", err)
	}
}

func TestMustGetManagementScheme(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MustGetManagementScheme() panicked: %v", r)
		}
	}()

	s := MustGetManagementScheme()

	for _, obj := range []struct {
		name string
		obj  runtime.Object
	}{
		{"corev1.Pod", &corev1.Pod{}},
		{"clusterapiv1.Cluster", &clusterapiv1.Cluster{}},
		{"kcmv1.ClusterDeployment", &kcmv1.ClusterDeployment{}},
		{"sourcev1.HelmRepository", &sourcev1.HelmRepository{}},
		{"helmcontrollerv2.HelmRelease", &helmcontrollerv2.HelmRelease{}},
	} {
		if _, _, err := s.ObjectKinds(obj.obj); err != nil {
			t.Errorf("%s not registered in management scheme: %v", obj.name, err)
		}
	}
}
