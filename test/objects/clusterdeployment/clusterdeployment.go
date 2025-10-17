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

package clusterdeployment

import (
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	DefaultName      = "clusterdeployment"
	DefaultNamespace = metav1.NamespaceDefault
)

type Opt func(clusterDeployment *kcmv1.ClusterDeployment)

func NewClusterDeployment(opts ...Opt) *kcmv1.ClusterDeployment {
	p := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultName,
			Namespace: DefaultNamespace,
		},
	}

	for _, opt := range opts {
		opt(p)
	}
	return p
}

func WithName(name string) Opt {
	return func(p *kcmv1.ClusterDeployment) {
		p.Name = name
	}
}

func WithNamespace(namespace string) Opt {
	return func(p *kcmv1.ClusterDeployment) {
		p.Namespace = namespace
	}
}

func WithDryRun(dryRun bool) Opt {
	return func(p *kcmv1.ClusterDeployment) {
		p.Spec.DryRun = dryRun
	}
}

func WithClusterTemplate(templateName string) Opt {
	return func(p *kcmv1.ClusterDeployment) {
		p.Spec.Template = templateName
	}
}

func WithConfig(config string) Opt {
	return func(p *kcmv1.ClusterDeployment) {
		p.Spec.Config = &apiextv1.JSON{
			Raw: []byte(config),
		}
	}
}

func WithServiceTemplate(templateName string) Opt {
	return func(p *kcmv1.ClusterDeployment) {
		p.Spec.ServiceSpec.Services = append(p.Spec.ServiceSpec.Services, kcmv1.Service{
			Template: templateName,
		})
	}
}

func WithServiceSpec(serviceSpec kcmv1.ServiceSpec) Opt {
	return func(p *kcmv1.ClusterDeployment) {
		p.Spec.ServiceSpec = serviceSpec
	}
}

func WithCredential(credName string) Opt {
	return func(p *kcmv1.ClusterDeployment) {
		p.Spec.Credential = credName
	}
}

func WithAvailableUpgrades(availableUpgrades []string) Opt {
	return func(p *kcmv1.ClusterDeployment) {
		p.Status.AvailableUpgrades = availableUpgrades
	}
}

func WithRegion(region string) Opt {
	return func(p *kcmv1.ClusterDeployment) {
		p.Status.Region = region
	}
}
