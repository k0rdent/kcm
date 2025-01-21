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

package multiclusterservice

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/K0rdent/kcm/api/v1alpha1"
)

const (
	DefaultName = "multiclusterservice"
)

type Opt func(multiClusterService *v1alpha1.MultiClusterService)

func NewMultiClusterService(opts ...Opt) *v1alpha1.MultiClusterService {
	p := &v1alpha1.MultiClusterService{
		ObjectMeta: metav1.ObjectMeta{
			Name: DefaultName,
		},
	}

	for _, opt := range opts {
		opt(p)
	}
	return p
}

func WithName(name string) Opt {
	return func(p *v1alpha1.MultiClusterService) {
		p.Name = name
	}
}

func WithServiceTemplate(templateName string) Opt {
	return func(p *v1alpha1.MultiClusterService) {
		p.Spec.ServiceSpec.Services = append(p.Spec.ServiceSpec.Services, v1alpha1.Service{
			Template: templateName,
		})
	}
}
