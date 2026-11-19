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

package sveltos

import (
	"testing"

	"github.com/stretchr/testify/require"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func Test_helmChartFromFluxSource_ChartName(t *testing.T) {
	for _, tc := range []struct {
		name          string
		path          string
		wantURL       string
		wantChartName string
	}{
		{
			name:          "nested path",
			path:          "charts/podinfo",
			wantURL:       "GitRepository://kcm-system/podinfo-src/charts/podinfo",
			wantChartName: "podinfo",
		},
		{
			name:          "relative path with trailing slash",
			path:          "./charts/podinfo/",
			wantURL:       "GitRepository://kcm-system/podinfo-src/charts/podinfo/",
			wantChartName: "podinfo",
		},
		{
			name:          "repository root",
			path:          ".",
			wantURL:       "GitRepository://kcm-system/podinfo-src/",
			wantChartName: "podinfo-6-5-0",
		},
		{
			name:          "empty path",
			path:          "",
			wantURL:       "GitRepository://kcm-system/podinfo-src/",
			wantChartName: "podinfo-6-5-0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl := &kcmv1.ServiceTemplate{
				Name: "podinfo-6-5-0", Namespace: "kcm-system",
				Spec: kcmv1.ServiceTemplateSpec{
					Helm: &kcmv1.HelmSpec{ChartSource: &kcmv1.SourceSpec{Path: tc.path}},
				},
				Status: kcmv1.ServiceTemplateStatus{
					SourceStatus: &kcmv1.SourceStatus{Kind: "GitRepository", Name: "podinfo-src", Namespace: "kcm-system"},
				},
			}
			svc := kcmv1.ServiceWithValues{Name: "mcs-podinfo", Namespace: "podinfo"}

			chart, err := helmChartFromFluxSource(t.Context(), svc, tmpl, "kcm-system")
			require.NoError(t, err)
			require.Equal(t, tc.wantURL, chart.RepositoryURL)
			require.Equal(t, tc.wantChartName, chart.ChartName)
		})
	}
}
