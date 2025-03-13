package controller

import (
	"context"
	"fmt"
	"time"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	metricLabelTemplateKind       = "template_kind"
	metricLabelTemplateName       = "template_name"
	metricLabelParentKind         = "parent_kind"
	metricLabelParentNamespace    = "parent_namespace"
	metricLabelParentName         = "parent_name"
	metricLabelReconcileOperation = "reconcile_operation"
)

var (
	metricTemplateUsage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: kcm.CoreKCMName,
			Name:      "template_usage",
			Help:      "Number of templates currently in use",
		},
		[]string{metricLabelTemplateKind, metricLabelTemplateName, metricLabelParentKind, metricLabelParentNamespace, metricLabelParentName},
	)

	metricMultiClusterServiceReconcileDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace:                   kcm.CoreKCMName,
			Name:                        "multiclusterservice_reconcile_duration_seconds",
			Help:                        "Distribution of duration of reconcile operations in seconds for MultiClusterService",
			NativeHistogramBucketFactor: 1.1,
		},
	)

	metricClusterDeploymentReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace:                   kcm.CoreKCMName,
			Name:                        "clusterdeployment_reconcile_duration_seconds",
			Help:                        "Distribution of duration of reconcile operation for ClusterDeployment in seconds",
			NativeHistogramBucketFactor: 1.1,
		},
		[]string{metricLabelReconcileOperation},
	)
)

func init() {
	metrics.Registry.MustRegister(
		metricTemplateUsage,
		metricMultiClusterServiceReconcileDuration,
		metricClusterDeploymentReconcileDuration,
	)
}

func trackMetricTemplateUsageSet(ctx context.Context, templateKind string, templateName string, parentKind string, parent metav1.ObjectMeta) {
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

func trackMetricTemplateUsageDelete(ctx context.Context, templateKind string, templateName string, parentKind string, parent metav1.ObjectMeta) {
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

func trackMetricReconcileDuration(ctx context.Context, obs prometheus.Observer, reconcilerName string, duration time.Duration) {
	obs.Observe(duration.Seconds())
	ctrl.LoggerFrom(ctx).V(1).Info(fmt.Sprintf("Tracking reconcile duration metric for %s", reconcilerName))
}

func trackMetricReconcileDurationVec(ctx context.Context, obs prometheus.ObserverVec, reconcilerName string, duration time.Duration, lvs ...string) {
	obs.WithLabelValues(lvs...).Observe(duration.Seconds())
	ctrl.LoggerFrom(ctx).V(1).Info(
		fmt.Sprintf("Tracking reconcile duration metric for %s", reconcilerName),
		"label_values", lvs,
	)
}
