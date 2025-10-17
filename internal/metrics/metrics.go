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

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	metricLabelTemplateKind      = "template_kind"
	metricLabelTemplateNamespace = "template_namespace"
	metricLabelTemplateName      = "template_name"
	metricLabelParentKind        = "parent_kind"
	metricLabelParentNamespace   = "parent_namespace"
	metricLabelParentName        = "parent_name"

	metricLabelIPAMKind      = "ipam_kind"
	metricLabelIPAMNamespace = "ipam_namespace"
	metricLabelIPAMName      = "ipam_name"
)

var (
	metricTemplateUsage = newGaugeVec("template_usage", "Number of templates currently in use",
		metricLabelTemplateKind, metricLabelTemplateName, metricLabelParentKind, metricLabelParentNamespace, metricLabelParentName)

	metricTemplateInvalidity = newGaugeVec("template_invalidity", "Number of invalid templates",
		metricLabelTemplateKind, metricLabelTemplateNamespace, metricLabelTemplateName)

	metricIPAMClaimUse = newGaugeVec("ipam_claim_use", "Number of IPAM claims currently in use",
		metricLabelIPAMKind, metricLabelIPAMNamespace, metricLabelIPAMName)

	metricIPAMClaimsBound = newGaugeVec("ipam_claims_bound", "Number of IPAM claims which are bound",
		metricLabelIPAMKind, metricLabelIPAMNamespace, metricLabelIPAMName)
)

func init() {
	metrics.Registry.MustRegister(
		metricTemplateUsage,
		metricTemplateInvalidity,
		metricIPAMClaimUse,
		metricIPAMClaimsBound,
	)
}

func newGaugeVec(name, help string, labels ...string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: kcmv1.CoreKCMName,
			Name:      name,
			Help:      help,
		},
		labels,
	)
}

func setGaugeAndLog(ctx context.Context, gauge *prometheus.GaugeVec, labels prometheus.Labels, active bool, logMsg string) { //nolint:revive
	value := 0.0
	if active {
		value = 1
	}
	gauge.With(labels).Set(value)
	l := ctrl.LoggerFrom(ctx)

	if l.V(1).Enabled() {
		l.V(1).Info(logMsg, append(labelMapToSlice(labels), "value", value)...)
	}
}

func labelMapToSlice(labels prometheus.Labels) []any {
	out := make([]any, 0, len(labels)*2)
	for k, v := range labels {
		out = append(out, k, v)
	}
	return out
}

func TrackMetricIPAMUsage(ctx context.Context, ipamKind, ipamName, ipamNamespace string, inUse bool) {
	setGaugeAndLog(ctx, metricIPAMClaimUse, prometheus.Labels{
		metricLabelIPAMKind:      ipamKind,
		metricLabelIPAMName:      ipamName,
		metricLabelIPAMNamespace: ipamNamespace,
	}, inUse, "Tracking cluster IPAM usage metric")
}

func TrackMetricIPAMClaimsBound(ctx context.Context, ipamKind, ipamName, ipamNamespace string, bound bool) {
	setGaugeAndLog(ctx, metricIPAMClaimsBound, prometheus.Labels{
		metricLabelIPAMKind:      ipamKind,
		metricLabelIPAMName:      ipamName,
		metricLabelIPAMNamespace: ipamNamespace,
	}, bound, "Tracking cluster IPAM bound metric")
}

func TrackMetricTemplateUsage(ctx context.Context, templateKind, templateName, parentKind string, parent metav1.ObjectMeta, inUse bool) {
	setGaugeAndLog(ctx, metricTemplateUsage, prometheus.Labels{
		metricLabelTemplateKind:    templateKind,
		metricLabelTemplateName:    templateName,
		metricLabelParentKind:      parentKind,
		metricLabelParentNamespace: parent.Namespace,
		metricLabelParentName:      parent.Name,
	}, inUse, "Tracking template usage metric")
}

func TrackMetricTemplateInvalidity(ctx context.Context, templateKind, templateNamespace, templateName string, valid bool) {
	setGaugeAndLog(ctx, metricTemplateInvalidity, prometheus.Labels{
		metricLabelTemplateKind:      templateKind,
		metricLabelTemplateNamespace: templateNamespace,
		metricLabelTemplateName:      templateName,
	}, !valid, "Tracking template invalidity metric")
}
