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

package helm

import (
	"context"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/rest"

	"github.com/Mirantis/hmc/api/v1alpha1"
)

type Actor struct {
	Config     *rest.Config
	RESTMapper apimeta.RESTMapper
}

func NewActor(config *rest.Config, mapper apimeta.RESTMapper) *Actor {
	return &Actor{
		Config:     config,
		RESTMapper: mapper,
	}
}

func (*Actor) DownloadChartFromArtifact(ctx context.Context, artifact *sourcev1.Artifact) (*chart.Chart, error) {
	return DownloadChart(ctx, artifact.URL, artifact.Digest)
}

func (a *Actor) InitializeConfiguration(
	clusterDeployment *v1alpha1.ClusterDeployment,
	log action.DebugLog,
) (*action.Configuration, error) {
	getter := NewMemoryRESTClientGetter(a.Config, a.RESTMapper)
	actionConfig := new(action.Configuration)
	err := actionConfig.Init(getter, clusterDeployment.Namespace, "secret", log)
	if err != nil {
		return nil, err
	}
	return actionConfig, nil
}

func (*Actor) EnsureReleaseWithValues(
	ctx context.Context,
	actionConfig *action.Configuration,
	hcChart *chart.Chart,
	clusterDeployment *v1alpha1.ClusterDeployment,
) error {
	install := action.NewInstall(actionConfig)
	install.DryRun = true
	install.ReleaseName = clusterDeployment.Name
	install.Namespace = clusterDeployment.Namespace
	install.ClientOnly = true

	vals, err := clusterDeployment.HelmValues()
	if err != nil {
		return err
	}

	_, err = install.RunWithContext(ctx, hcChart, vals)
	return err
}
