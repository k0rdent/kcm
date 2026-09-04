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

package helm

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	godigest "github.com/opencontainers/go-digest"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
)

func buildTestChartArchive(t *testing.T) []byte {
	t.Helper()

	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:       "testchart",
			Version:    "0.1.0",
			APIVersion: "v2",
		},
	}

	dir := t.TempDir()
	path, err := chartutil.Save(c, dir)
	if err != nil {
		t.Fatalf("failed to build test chart archive: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read test chart archive: %v", err)
	}
	return data
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestCopyChart(t *testing.T) {
	data := []byte("some chart bytes")

	t.Run("no digest just copies", func(t *testing.T) {
		var out bytes.Buffer
		if err := copyChart(bytes.NewReader(data), &out, ""); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.String() != string(data) {
			t.Errorf("out = %q, want %q", out.String(), string(data))
		}
	})

	t.Run("matching digest succeeds", func(t *testing.T) {
		dig := godigest.Canonical.FromBytes(data).String()

		var out bytes.Buffer
		if err := copyChart(bytes.NewReader(data), &out, dig); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid digest format returns error", func(t *testing.T) {
		var out bytes.Buffer
		err := copyChart(bytes.NewReader(data), &out, "not-a-valid-digest")
		if err == nil || !strings.Contains(err.Error(), "failed to parse digest") {
			t.Fatalf("err = %v, want parse digest error", err)
		}
	})

	t.Run("digest mismatch returns error", func(t *testing.T) {
		wrongDigest := godigest.Canonical.FromBytes([]byte("other data")).String()

		var out bytes.Buffer
		err := copyChart(bytes.NewReader(data), &out, wrongDigest)
		if err == nil || !strings.Contains(err.Error(), "verification for digest") {
			t.Fatalf("err = %v, want verification error", err)
		}
	})

	t.Run("reader error is wrapped", func(t *testing.T) {
		var out bytes.Buffer
		err := copyChart(errReader{}, &out, "")
		if err == nil || !strings.Contains(err.Error(), "failed to copy chart") {
			t.Fatalf("err = %v, want copy error", err)
		}
	})
}

func TestDownloadChart(t *testing.T) {
	chartBytes := buildTestChartArchive(t)

	t.Run("successful download and load, no digest", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(chartBytes)
		}))
		defer srv.Close()

		got, err := DownloadChart(context.Background(), srv.URL, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Metadata.Name != "testchart" {
			t.Errorf("got chart name %q, want %q", got.Metadata.Name, "testchart")
		}
	})

	t.Run("successful download with matching digest", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(chartBytes)
		}))
		defer srv.Close()

		dig := godigest.Canonical.FromBytes(chartBytes).String()
		got, err := DownloadChart(context.Background(), srv.URL, dig)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Metadata.Name != "testchart" {
			t.Errorf("got chart name %q, want %q", got.Metadata.Name, "testchart")
		}
	})

	t.Run("digest mismatch returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(chartBytes)
		}))
		defer srv.Close()

		_, err := DownloadChart(context.Background(), srv.URL, godigest.Canonical.FromBytes([]byte("nope")).String())
		if err == nil || !strings.Contains(err.Error(), "verification for digest") {
			t.Fatalf("err = %v, want verification error", err)
		}
	})

	t.Run("non-200 status returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		_, err := DownloadChart(context.Background(), srv.URL, "")
		if err == nil || !strings.Contains(err.Error(), "chart download request failed") {
			t.Fatalf("err = %v, want download failed error", err)
		}
	})

	t.Run("invalid archive body returns load error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not a valid tgz"))
		}))
		defer srv.Close()

		_, err := DownloadChart(context.Background(), srv.URL, "")
		if err == nil || !strings.Contains(err.Error(), "failed to load archive") {
			t.Fatalf("err = %v, want load archive error", err)
		}
	})

	t.Run("malformed URL returns request creation error", func(t *testing.T) {
		_, err := DownloadChart(context.Background(), "://bad-url\x7f", "")
		if err == nil {
			t.Fatal("expected error for malformed URL, got nil")
		}
	})
}
