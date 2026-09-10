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
	"strings"
	"testing"

	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestShouldReportStatusOnArtifactReadiness(t *testing.T) {
	t.Run("no conditions, no artifact: not ready, error", func(t *testing.T) {
		chart := &sourcev1.HelmChart{}

		report, err := ShouldReportStatusOnArtifactReadiness(chart)
		if report {
			t.Error("report = true, want false")
		}
		if err == nil || !strings.Contains(err.Error(), "artifact is not ready yet") {
			t.Errorf("err = %v, want not-ready error", err)
		}
	})

	t.Run("Ready condition stale (generation mismatch): not ready, error", func(t *testing.T) {
		chart := &sourcev1.HelmChart{
			ObjectMeta: metav1.ObjectMeta{Generation: 2},
			Status: sourcev1.HelmChartStatus{
				Conditions: []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1},
				},
			},
		}

		report, err := ShouldReportStatusOnArtifactReadiness(chart)
		if report {
			t.Error("report = true, want false")
		}
		if err == nil || !strings.Contains(err.Error(), "was not reconciled yet") {
			t.Errorf("err = %v, want not-reconciled error", err)
		}
	})

	t.Run("Ready condition false: report, error", func(t *testing.T) {
		chart := &sourcev1.HelmChart{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status: sourcev1.HelmChartStatus{
				Conditions: []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionFalse, ObservedGeneration: 1, Message: "download failed"},
				},
			},
		}

		report, err := ShouldReportStatusOnArtifactReadiness(chart)
		if !report {
			t.Error("report = false, want true")
		}
		if err == nil || !strings.Contains(err.Error(), "download failed") {
			t.Errorf("err = %v, want download error containing message", err)
		}
	})

	t.Run("Ready condition true and artifact populated: ready, no error", func(t *testing.T) {
		chart := &sourcev1.HelmChart{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status: sourcev1.HelmChartStatus{
				Conditions: []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1},
				},
				Artifact: &fluxmeta.Artifact{},
				URL:      "http://example.com/chart.tgz",
			},
		}

		report, err := ShouldReportStatusOnArtifactReadiness(chart)
		if report {
			t.Error("report = true, want false")
		}
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("Ready condition true but artifact still missing: not ready, error", func(t *testing.T) {
		chart := &sourcev1.HelmChart{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status: sourcev1.HelmChartStatus{
				Conditions: []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1},
				},
			},
		}

		report, err := ShouldReportStatusOnArtifactReadiness(chart)
		if report {
			t.Error("report = true, want false")
		}
		if err == nil || !strings.Contains(err.Error(), "artifact is not ready yet") {
			t.Errorf("err = %v, want not-ready error", err)
		}
	})
}
