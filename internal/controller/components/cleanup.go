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

package components

import (
	"context"
	"errors"
	"fmt"
	"slices"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/record"
	releaseutil "github.com/K0rdent/kcm/internal/util/release"
)

func Cleanup(
	ctx context.Context,
	mgmtClient client.Client,
	cluster clusterInterface,
	labelSelector *metav1.LabelSelector,
	namespace string,
) error {
	var (
		errs error
		l    = ctrl.LoggerFrom(ctx)
	)

	managedHelmReleases := new(helmcontrollerv2.HelmReleaseList)
	listOpts := []client.ListOption{
		client.MatchingLabels{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue},
		client.InNamespace(namespace),
	}
	if labelSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(labelSelector)
		if err != nil {
			return fmt.Errorf("failed to convert label selector: %w", err)
		}
		listOpts = append(listOpts, client.MatchingLabelsSelector{Selector: selector})
	}
	if err := mgmtClient.List(ctx, managedHelmReleases, listOpts...); err != nil {
		return fmt.Errorf("failed to list %s: %w", helmcontrollerv2.GroupVersion.WithKind(helmcontrollerv2.HelmReleaseKind), err)
	}

	releasesList := &metav1.PartialObjectMetadataList{}
	if len(managedHelmReleases.Items) > 0 {
		releasesList.SetGroupVersionKind(kcmv1.GroupVersion.WithKind(kcmv1.ReleaseKind))
		if err := mgmtClient.List(ctx, releasesList); err != nil {
			return fmt.Errorf("failed to list releases: %w", err)
		}
	}

	for _, hr := range managedHelmReleases.Items {
		// do not remove non-management and non-regional related components (#703)
		if len(hr.OwnerReferences) > 0 {
			continue
		}

		componentName := hr.Name // providers(components) names map 1-1 to the helmreleases names

		if componentName == cluster.HelmReleaseName(kcmv1.CoreCAPIName) ||
			componentName == cluster.HelmReleaseName(cluster.KCMHelmChartName()) ||
			slices.ContainsFunc(releasesList.Items, func(r metav1.PartialObjectMetadata) bool {
				return componentName == releaseutil.TemplatesChartFromReleaseName(r.Name)
			}) ||
			slices.ContainsFunc(cluster.Components().Providers, func(newComp kcmv1.Provider) bool { return componentName == cluster.HelmReleaseName(newComp.Name) }) {
			continue
		}

		l.V(1).Info("Found component to remove", "component_name", componentName)
		record.Eventf(cluster, &hr, "ComponentRemoved", "DeleteComponent", "The %s component was removed: removing HelmRelease", componentName)

		if err := mgmtClient.Delete(ctx, &hr); client.IgnoreNotFound(err) != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to delete %s: %w", client.ObjectKeyFromObject(&hr), err))
			continue
		}
		l.V(1).Info("Removed HelmRelease", "reference", client.ObjectKeyFromObject(&hr).String())
	}

	return errs
}
