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
	"time"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

type Poller struct {
	client.Client
	eventChan       chan event.GenericEvent
	systemNamespace string
	requeueInterval time.Duration
}

func (p *Poller) Start(ctx context.Context) error {
	logger := ctrl.LoggerFrom(ctx)
	logger.Info("Starting polling ClusterSummaries", "polling_interval", p.requeueInterval, "event_chan_cap", cap(p.eventChan))
	p.pollClusterSummaries(ctx)
	return nil
}

// pollClusterSummaries continuously fetches ClusterSummary objects corresponding to existing
// ServiceSet objects from all clusters and sends GenericEvent for ServiceSet object to the
// channel being watched by controller in case ClusterSummary status does not match observed
// ServiceSet object's status.
func (p *Poller) pollClusterSummaries(ctx context.Context) {
	ticker := time.NewTicker(p.requeueInterval)
	defer ticker.Stop()

	logger := ctrl.LoggerFrom(ctx).WithName("poller-cluster-summaries")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			serviceSetList := new(kcmv1.ServiceSetList)
			if err := p.List(ctx, serviceSetList); err != nil {
				logger.V(1).Error(err, "failed to list ServiceSet objects")
				continue
			}
			logger.V(1).Info("Listing ServiceSet objects", "service_set_count", len(serviceSetList.Items))

			for _, item := range serviceSetList.Items {
				if !item.GetDeletionTimestamp().IsZero() {
					logger.V(1).Info("ServiceSet is being deleted, skipping polling", "service_set", client.ObjectKeyFromObject(&item))
					continue
				}

				serviceSet := item.DeepCopy()
				rgnClient, err := getRegionalClient(ctx, p.Client, serviceSet, p.systemNamespace)
				if err != nil {
					logger.V(1).Error(err, "failed to get regional client")
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
					logger.V(1).Error(err, "failed to get ClusterSummary")
					continue
				}

				logger.V(1).Info("Fetched ClusterSummary", "cluster_summary", client.ObjectKeyFromObject(summary))
				serviceStatesFromSummary := servicesStateFromSummary(logger, summary, serviceSet)
				if !equality.Semantic.DeepEqual(serviceSet.Status.Services, serviceStatesFromSummary) {
					logger.V(1).Info("ClusterSummary status does not match observed ServiceSet status, sending event", "service_set", key)
					// we won't block in case event channel is full, hence will just drop event, because
					// on next tick it will be processed again.
					select {
					case p.eventChan <- event.GenericEvent{Object: serviceSet}:
					default:
						logger.V(1).Info("Event channel is full, dropping event", "service_set", key)
					}
				}
			}
		}
	}
}
