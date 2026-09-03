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

package metrics

import (
	"context"
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestMain(m *testing.M) {
	// Enabling V(1) logging exercises the log-on-change branch of setGaugeAndLog.
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	os.Exit(m.Run())
}

func TestTrackMetricIPAMUsage(t *testing.T) {
	ctx := context.Background()

	TrackMetricIPAMUsage(ctx, "IPClaim", "claim1", "ns1", true)
	labels := prometheus.Labels{metricLabelIPAMKind: "IPClaim", metricLabelIPAMName: "claim1", metricLabelIPAMNamespace: "ns1"}
	if got := testutil.ToFloat64(metricIPAMClaimUse.With(labels)); got != 1 {
		t.Errorf("gauge = %v, want 1", got)
	}

	TrackMetricIPAMUsage(ctx, "IPClaim", "claim1", "ns1", false)
	if got := testutil.ToFloat64(metricIPAMClaimUse.With(labels)); got != 0 {
		t.Errorf("gauge = %v, want 0", got)
	}
}

func TestTrackMetricIPAMClaimsBound(t *testing.T) {
	ctx := context.Background()

	TrackMetricIPAMClaimsBound(ctx, "IPClaim", "claim1", "ns1", true)
	labels := prometheus.Labels{metricLabelIPAMKind: "IPClaim", metricLabelIPAMName: "claim1", metricLabelIPAMNamespace: "ns1"}
	if got := testutil.ToFloat64(metricIPAMClaimsBound.With(labels)); got != 1 {
		t.Errorf("gauge = %v, want 1", got)
	}

	TrackMetricIPAMClaimsBound(ctx, "IPClaim", "claim1", "ns1", false)
	if got := testutil.ToFloat64(metricIPAMClaimsBound.With(labels)); got != 0 {
		t.Errorf("gauge = %v, want 0", got)
	}
}

func TestTrackMetricTemplateUsage(t *testing.T) {
	ctx := context.Background()
	parent := metav1.ObjectMeta{Name: "parent1", Namespace: "ns1"}

	TrackMetricTemplateUsage(ctx, "ClusterTemplate", "tmpl1", "ClusterDeployment", parent, true)
	labels := prometheus.Labels{
		metricLabelTemplateKind:    "ClusterTemplate",
		metricLabelTemplateName:    "tmpl1",
		metricLabelParentKind:      "ClusterDeployment",
		metricLabelParentNamespace: "ns1",
		metricLabelParentName:      "parent1",
	}
	if got := testutil.ToFloat64(metricTemplateUsage.With(labels)); got != 1 {
		t.Errorf("gauge = %v, want 1", got)
	}

	TrackMetricTemplateUsage(ctx, "ClusterTemplate", "tmpl1", "ClusterDeployment", parent, false)
	if got := testutil.ToFloat64(metricTemplateUsage.With(labels)); got != 0 {
		t.Errorf("gauge = %v, want 0", got)
	}
}

func TestTrackMetricTemplateInvalidity(t *testing.T) {
	ctx := context.Background()

	TrackMetricTemplateInvalidity(ctx, "ClusterTemplate", "ns1", "tmpl1", false)
	labels := prometheus.Labels{metricLabelTemplateKind: "ClusterTemplate", metricLabelTemplateNamespace: "ns1", metricLabelTemplateName: "tmpl1"}
	if got := testutil.ToFloat64(metricTemplateInvalidity.With(labels)); got != 1 {
		t.Errorf("gauge = %v, want 1 (invalid)", got)
	}

	TrackMetricTemplateInvalidity(ctx, "ClusterTemplate", "ns1", "tmpl1", true)
	if got := testutil.ToFloat64(metricTemplateInvalidity.With(labels)); got != 0 {
		t.Errorf("gauge = %v, want 0 (valid)", got)
	}
}
