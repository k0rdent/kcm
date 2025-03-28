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

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
)

const (
	metricLabelTemplateKind      = "template_kind"
	metricLabelTemplateNamespace = "template_namespace"
	metricLabelTemplateName      = "template_name"
	metricLabelParentKind        = "parent_kind"
	metricLabelParentNamespace   = "parent_namespace"
	metricLabelParentName        = "parent_name"
)

var metricTemplateUsage = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: kcm.CoreKCMName,
		Name:      "template_usage",
		Help:      "Number of templates currently in use",
	},
	[]string{metricLabelTemplateKind, metricLabelTemplateName, metricLabelParentKind, metricLabelParentNamespace, metricLabelParentName},
)

var metricTemplateInvalidity = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: kcm.CoreKCMName,
		Name:      "template_invalidity",
		Help:      "Number of invalid templates",
	},
	[]string{metricLabelTemplateKind, metricLabelTemplateNamespace, metricLabelTemplateName},
)

func init() {
	metrics.Registry.MustRegister(
		metricTemplateUsage,
		metricTemplateInvalidity,
	)
}

func TrackMetricTemplateUsage(ctx context.Context, templateKind, templateName, parentKind string, parent metav1.ObjectMeta, inUse bool) { //nolint:revive // false-positive
	var value float64
	if inUse {
		value = 1
	}

	metricTemplateUsage.With(prometheus.Labels{
		metricLabelTemplateKind:    templateKind,
		metricLabelTemplateName:    templateName,
		metricLabelParentKind:      parentKind,
		metricLabelParentNamespace: parent.Namespace,
		metricLabelParentName:      parent.Name,
	}).Set(value)

	ctrl.LoggerFrom(ctx).V(1).Info("Tracking template usage metric",
		metricLabelTemplateKind, templateKind,
		metricLabelTemplateName, templateName,
		metricLabelParentKind, parentKind,
		metricLabelParentNamespace, parent.Namespace,
		metricLabelParentName, parent.Name,
		"value", value,
	)
}

func TrackMetricTemplateInvalidity(ctx context.Context, templateKind, templateNamespace, templateName string, valid bool) { //nolint:revive // false-positive
	var value float64
	if !valid {
		value = 1
	}

	metricTemplateInvalidity.With(prometheus.Labels{
		metricLabelTemplateKind:      templateKind,
		metricLabelTemplateNamespace: templateNamespace,
		metricLabelTemplateName:      templateName,
	}).Set(value)

	ctrl.LoggerFrom(ctx).V(1).Info("Tracking template invalidity metric",
		metricLabelTemplateKind, templateKind,
		metricLabelTemplateNamespace, templateNamespace,
		metricLabelTemplateName, templateName,
		"value", value,
	)
}
