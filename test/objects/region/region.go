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

package region

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	DefaultName = "rgn"
)

type Opt func(region *kcmv1.Region)

func New(opts ...Opt) *kcmv1.Region {
	p := &kcmv1.Region{
		TypeMeta: metav1.TypeMeta{
			Kind:       kcmv1.RegionKind,
			APIVersion: kcmv1.GroupVersion.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       DefaultName,
			Finalizers: []string{kcmv1.RegionFinalizer},
		},
	}

	for _, opt := range opts {
		opt(p)
	}
	return p
}

func WithName(name string) Opt {
	return func(p *kcmv1.Region) {
		p.Name = name
	}
}

func WithProviders(providers ...kcmv1.Provider) Opt {
	return func(p *kcmv1.Region) {
		p.Spec.Providers = providers
	}
}

func WithAvailableProviders(providers kcmv1.Providers) Opt {
	return func(p *kcmv1.Region) {
		p.Status.AvailableProviders = providers
	}
}

func WithComponentsStatus(components map[string]kcmv1.ComponentStatus) Opt {
	return func(p *kcmv1.Region) {
		p.Status.Components = components
	}
}
