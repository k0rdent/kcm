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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	"helm.sh/helm/v3/pkg/chart"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	fakerestmapper "k8s.io/client-go/restmapper"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func TestNewActor(t *testing.T) {
	cfg := &rest.Config{Host: "https://127.0.0.1:6443"}
	mapper := fakerestmapper.NewDiscoveryRESTMapper(nil)

	a := NewActor(cfg, mapper)
	if a.Config != cfg {
		t.Errorf("Config = %+v, want %+v", a.Config, cfg)
	}
	if a.RESTMapper == nil {
		t.Error("RESTMapper = nil, want the provided mapper")
	}
}

func TestActor_DownloadChartFromArtifact(t *testing.T) {
	a := &Actor{}

	t.Run("nil artifact returns error", func(t *testing.T) {
		_, err := a.DownloadChartFromArtifact(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), "not ready yet") {
			t.Fatalf("err = %v, want not-ready error", err)
		}
	})

	t.Run("downloads chart from artifact URL", func(t *testing.T) {
		chartBytes := buildTestChartArchive(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(chartBytes)
		}))
		defer srv.Close()

		got, err := a.DownloadChartFromArtifact(context.Background(), &fluxmeta.Artifact{URL: srv.URL})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Metadata.Name != "testchart" {
			t.Errorf("got chart name %q, want %q", got.Metadata.Name, "testchart")
		}
	})
}

func TestActor_InitializeConfiguration(t *testing.T) {
	mapper := fakerestmapper.NewDiscoveryRESTMapper(nil)
	a := NewActor(&rest.Config{Host: "https://127.0.0.1:1"}, mapper)

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"},
	}

	cfg, err := a.InitializeConfiguration(cd, func(string, ...any) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("InitializeConfiguration() returned nil configuration")
	}
}

func TestActor_EnsureReleaseWithValues(t *testing.T) {
	restMapper := fakerestmapper.NewDiscoveryRESTMapper(nil)
	a := NewActor(&rest.Config{Host: "https://127.0.0.1:1"}, restMapper)

	cd := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "cd1", Namespace: "ns1"},
	}

	cfg, err := a.InitializeConfiguration(cd, func(string, ...any) {})
	if err != nil {
		t.Fatalf("unexpected error initializing configuration: %v", err)
	}

	hcChart := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:       "testchart",
			Version:    "0.1.0",
			APIVersion: "v2",
		},
	}

	err = a.EnsureReleaseWithValues(context.Background(), cfg, hcChart, cd)
	if err != nil {
		t.Fatalf("unexpected error running dry-run install: %v", err)
	}
}
