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
	"context"
	"errors"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

type Tracker struct {
	client.Client

	SystemNamespace string
}

const interval = 24 * time.Hour

func (t *Tracker) Start(ctx context.Context) error {
	timer := time.NewTimer(0)
	for {
		select {
		case <-timer.C:
			t.Tick(ctx)
			timer.Reset(interval)
		case <-ctx.Done():
			return nil
		}
	}
}

func (t *Tracker) Tick(ctx context.Context) {
	l := log.FromContext(ctx).WithName("telemetry tracker")

	logger := l.WithValues("event", clusterDeploymentHeartbeatEvent)
	err := t.trackClusterDeploymentHeartbeat(ctx)
	if err != nil {
		logger.Error(err, "failed to track an event")
	} else {
		logger.Info("successfully tracked an event")
	}
}

func (t *Tracker) trackClusterDeploymentHeartbeat(ctx context.Context) error {
	mgmt := &kcmv1.Management{}
	if err := t.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, mgmt); err != nil {
		return err
	}

	templatesList := &kcmv1.ClusterTemplateList{}
	if err := t.List(ctx, templatesList, client.InNamespace(t.SystemNamespace)); err != nil {
		return err
	}

	templates := make(map[string]kcmv1.ClusterTemplate)
	for _, template := range templatesList.Items {
		templates[template.Name] = template
	}

	var errs error
	clusterDeployments := &kcmv1.ClusterDeploymentList{}
	if err := t.List(ctx, clusterDeployments); err != nil {
		return err
	}

	for _, clusterDeployment := range clusterDeployments.Items {
		template := templates[clusterDeployment.Spec.Template]
		// TODO: get k0s cluster ID once it's exposed in k0smotron API
		clusterID := ""

		err := TrackClusterDeploymentHeartbeat(
			string(mgmt.UID),
			string(clusterDeployment.UID),
			clusterID,
			clusterDeployment.Spec.Template,
			template.Status.ChartVersion,
			template.Status.Providers,
		)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to track the heartbeat of the clusterDeployment %s/%s", clusterDeployment.Namespace, clusterDeployment.Name))
			continue
		}
	}
	return errs
}
