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
	metricLabelTemplateKind    = "template_kind"
	metricLabelTemplateName    = "template_name"
	metricLabelParentKind      = "parent_kind"
	metricLabelParentNamespace = "parent_namespace"
	metricLabelParentName      = "parent_name"
)

var metricTemplateUsage = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: kcm.CoreKCMName,
		Name:      "template_usage",
		Help:      "Number of templates currently in use",
	},
	[]string{metricLabelTemplateKind, metricLabelTemplateName, metricLabelParentKind, metricLabelParentNamespace, metricLabelParentName},
)

func init() {
	metrics.Registry.MustRegister(
		metricTemplateUsage,
	)
}

func TrackMetricTemplateUsageSet(ctx context.Context, templateKind, templateName, parentKind string, parent metav1.ObjectMeta) {
	metricTemplateUsage.With(prometheus.Labels{
		metricLabelTemplateKind:    templateKind,
		metricLabelTemplateName:    templateName,
		metricLabelParentKind:      parentKind,
		metricLabelParentNamespace: parent.Namespace,
		metricLabelParentName:      parent.Name,
	}).Set(1)

	ctrl.LoggerFrom(ctx).V(1).Info("Tracking template usage metric (set to 1)",
		metricLabelTemplateKind, templateKind,
		metricLabelTemplateName, templateName,
		metricLabelParentKind, parentKind,
		metricLabelParentNamespace, parent.Namespace,
		metricLabelParentName, parent.Name,
	)
}

func TrackMetricTemplateUsageDelete(ctx context.Context, templateKind, templateName, parentKind string, parent metav1.ObjectMeta) {
	metricTemplateUsage.Delete(prometheus.Labels{
		metricLabelTemplateKind:    templateKind,
		metricLabelTemplateName:    templateName,
		metricLabelParentKind:      parentKind,
		metricLabelParentNamespace: parent.Namespace,
		metricLabelParentName:      parent.Name,
	})

	ctrl.LoggerFrom(ctx).V(1).Info("Tracking template usage metric (delete)",
		metricLabelTemplateKind, templateKind,
		metricLabelTemplateName, templateName,
		metricLabelParentKind, parentKind,
		metricLabelParentNamespace, parent.Namespace,
		metricLabelParentName, parent.Name,
	)
}
