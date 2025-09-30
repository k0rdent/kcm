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
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	"github.com/hashicorp/go-retryablehttp"
	godigest "github.com/opencontainers/go-digest"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func DownloadChartFromArtifact(ctx context.Context, artifact *fluxmeta.Artifact) (*chart.Chart, error) {
	return DownloadChart(ctx, artifact.URL, artifact.Digest)
}

func DownloadChart(ctx context.Context, chartURL, digest string) (*chart.Chart, error) {
	l := log.FromContext(ctx, "chart", chartURL)

	client := retryablehttp.NewClient()
	req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodGet, chartURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			l.Error(err, "Error closing response body after chart download")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chart download request failed: %s", resp.Status)
	}

	var buf bytes.Buffer
	if err := copyChart(resp.Body, &buf, digest); err != nil {
		return nil, err
	}

	helmChart, err := loader.LoadArchive(&buf)
	if err != nil {
		return nil, fmt.Errorf("failed to load archive for chart %s, %w", chartURL, err)
	}
	return helmChart, nil
}

func copyChart(reader io.Reader, writer io.Writer, digest string) error {
	writers := []io.Writer{writer}
	var verifier godigest.Verifier
	// verify data integrity if digest is provided
	if digest != "" {
		dig, err := godigest.Parse(digest)
		if err != nil {
			return fmt.Errorf("failed to parse digest %s: %w", digest, err)
		}
		verifier = dig.Verifier()
		writers = append(writers, verifier)
	}

	mw := io.MultiWriter(writers...)
	if _, err := io.Copy(mw, reader); err != nil {
		return fmt.Errorf("failed to copy chart: %w", err)
	}

	if digest != "" && !verifier.Verified() {
		return fmt.Errorf("verification for digest %s failed", digest)
	}
	return nil
}
