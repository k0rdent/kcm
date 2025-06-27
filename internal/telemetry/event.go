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

	"github.com/K0rdent/kcm/internal/build"
)

const (
	clusterDeploymentCreateEvent    = "cluster-deployment-create"
	clusterDeploymentHeartbeatEvent = "cluster-deployment-heartbeat"
)

func TrackClusterDeploymentCreate(id, clusterDeploymentID, template string, dryRun bool) error {
	props := map[string]any{
		"kcmVersion":          build.Version,
		"clusterDeploymentID": clusterDeploymentID,
		"template":            template,
		"dryRun":              dryRun,
	}
	return TrackEvent(clusterDeploymentCreateEvent, id, props)
}

func TrackClusterDeploymentHeartbeat(id, clusterDeploymentID, clusterID, template, templateHelmChartVersion string, providers []string) error {
	props := map[string]any{
		"kcmVersion":               build.Version,
		"clusterDeploymentID":      clusterDeploymentID,
		"clusterID":                clusterID,
		"template":                 template,
		"templateHelmChartVersion": templateHelmChartVersion,
		"providers":                providers,
	}
	return TrackEvent(clusterDeploymentHeartbeatEvent, id, props)
}

func TrackEvent(name, id string, properties map[string]any) error {
	if analyticsClient == nil {
		return nil
	}
	return analyticsClient.Enqueue(analytics.Track{
		AnonymousId: id,
		Event:       name,
		Properties:  properties,
	})
}
