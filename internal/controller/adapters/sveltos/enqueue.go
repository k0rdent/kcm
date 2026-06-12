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

package sveltos

import (
	"context"
	"fmt"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	pollerutil "github.com/K0rdent/kcm/internal/util/poller"
)

// enqueueClusterSummary returns a [pollerutil.EnqueueFunc] that walks every
// [kcmv1.ServiceSet] and emits the ones whose observed Status.Services drift
// from the regional ClusterSummary. Regional clients are cached per tick.
func enqueueClusterSummary(cl client.Client, systemNamespace string) pollerutil.EnqueueFunc[*kcmv1.ServiceSet] {
	return func(ctx context.Context) ([]*kcmv1.ServiceSet, error) {
		logger := ctrl.LoggerFrom(ctx)

		serviceSetList := new(kcmv1.ServiceSetList)
		if err := cl.List(ctx, serviceSetList); err != nil {
			return nil, fmt.Errorf("failed to list ServiceSet objects: %w", err)
		}

		logger.V(1).Info("Listing ServiceSet objects", "service_set_count", len(serviceSetList.Items))

		// cluster object key -> regional client, reused within a single tick
		rgnClients := make(map[client.ObjectKey]client.Client)

		out := make([]*kcmv1.ServiceSet, 0, len(serviceSetList.Items))
		for i := range serviceSetList.Items {
			serviceSet := &serviceSetList.Items[i]
			if !serviceSet.GetDeletionTimestamp().IsZero() {
				logger.V(1).Info("ServiceSet is being deleted, skipping polling", "service_set", client.ObjectKeyFromObject(serviceSet))
				continue
			}

			rgnClient, err := resolveRegionalClient(ctx, cl, serviceSet, systemNamespace, rgnClients)
			if err != nil {
				logger.V(1).Error(err, "failed to get regional client", "service_set", client.ObjectKeyFromObject(serviceSet))
				continue
			}

			var profile client.Object
			if serviceSet.Spec.Provider.SelfManagement {
				profile = new(addoncontrollerv1beta1.ClusterProfile)
			} else {
				profile = new(addoncontrollerv1beta1.Profile)
			}

			key := client.ObjectKeyFromObject(serviceSet)
			if err := rgnClient.Get(ctx, key, profile); err != nil {
				logger.V(1).Error(err, "failed to get Profile or ClusterProfile", "object_name", key)
				continue
			}

			summary, err := getClusterSummaryForServiceSet(ctx, rgnClient, serviceSet, profile)
			if err != nil {
				logger.V(1).Error(err, "failed to get ClusterSummary", "service_set", key)
				continue
			}

			logger.V(1).Info("Fetched ClusterSummary", "cluster_summary", client.ObjectKeyFromObject(summary))
			serviceStatesFromSummary := servicesStateFromSummary(logger, summary, serviceSet)
			if !equality.Semantic.DeepEqual(serviceSet.Status.Services, serviceStatesFromSummary) {
				logger.V(1).Info("ClusterSummary status does not match observed ServiceSet status, scheduling reconcile", "service_set", key)
				out = append(out, serviceSet)
			}
		}

		return out, nil
	}
}
