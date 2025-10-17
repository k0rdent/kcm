// Copyright 2024
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

package telemetry

import (
	"github.com/segmentio/analytics-go/v3"
)

func trackEvent(anonymousID, event string, properties map[string]any) error {
	if analyticsClient == nil {
		return nil
	}

	return analyticsClient.Enqueue(analytics.Track{
		AnonymousId: anonymousID,
		Event:       event,
		Properties:  properties,
	})
}

func TrackClusterDeploymentCreate(id, clusterDeploymentID, template string, dryRun bool) error {
	return trackEvent(id, "cluster-deployment-create", map[string]any{
		"clusterDeploymentID": clusterDeploymentID,
		"template":            template,
		"dryRun":              dryRun,
	})
}

func TrackClusterIPAMCreate(clusterIPAMId, cluster, ipamProvider string) error {
	return trackEvent(clusterIPAMId, "cluster-ipam-create", map[string]any{
		"cluster":      cluster,
		"ipamProvider": ipamProvider,
	})
}
