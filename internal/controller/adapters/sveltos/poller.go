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
	go p.pollClusterSummaries(ctx)
	return nil
}

// pollClusterSummaries continuously fetches ClusterSummary objects corresponding to existing
// ServiceSet objects from all clusters and sends GenericEvent for ServiceSet object to the
// channel being watched by controller in case ClusterSummary status does not match observed
// ServiceSet object's status.
func (p *Poller) pollClusterSummaries(ctx context.Context) {
	ticker := time.NewTicker(p.requeueInterval)
	defer ticker.Stop()

	logger := ctrl.LoggerFrom(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			serviceSetList := new(kcmv1.ServiceSetList)
			if err := p.List(ctx, serviceSetList); err != nil {
				continue
			}

			for _, item := range serviceSetList.Items {
				serviceSet := item.DeepCopy()
				rgnClient, err := getRegionalClient(ctx, p.Client, serviceSet, p.systemNamespace)
				if err != nil {
					continue
				}

				var profile client.Object
				if serviceSet.Spec.Provider.SelfManagement {
					profile = new(addoncontrollerv1beta1.ClusterProfile)
				} else {
					profile = new(addoncontrollerv1beta1.Profile)
				}
				key := client.ObjectKeyFromObject(profile)
				if err := rgnClient.Get(ctx, key, profile); err != nil {
					continue
				}

				summary, err := getClusterSummaryForServiceSet(ctx, rgnClient, serviceSet, profile)
				if err != nil {
					continue
				}

				serviceStatesFromSummary := servicesStateFromSummary(logger, summary, serviceSet)
				if !equality.Semantic.DeepEqual(serviceSet.Status.Services, serviceStatesFromSummary) {
					p.eventChan <- event.GenericEvent{Object: serviceSet}
				}
			}
		}
	}
}
