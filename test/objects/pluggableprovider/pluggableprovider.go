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

package pluggableprovider

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/K0rdent/kcm/api/v1alpha1"
)

const (
	DefaultName = "foobar"
)

type Opt func(pluggableprovider *v1alpha1.PluggableProvider)

func NewPluggableProvider(opts ...Opt) *v1alpha1.PluggableProvider {
	p := &v1alpha1.PluggableProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultName,
			Namespace: metav1.NamespaceDefault,
		},
	}

	for _, opt := range opts {
		opt(p)
	}
	return p
}

func WithName(name string) Opt {
	return func(p *v1alpha1.PluggableProvider) {
		p.Name = name
	}
}

func WithClusterIdentityKinds(vals ...string) Opt {
	return func(p *v1alpha1.PluggableProvider) {
		p.Spec.ClusterIdentityKinds = vals
	}
}

func WithClusterGVKs(vals ...v1alpha1.GroupVersionKind) Opt {
	return func(p *v1alpha1.PluggableProvider) {
		p.Spec.ClusterGVKs = vals
	}
}

func WithKCMComponentLabel() Opt {
	return func(p *v1alpha1.PluggableProvider) {
		if p.Labels == nil {
			p.Labels = make(map[string]string)
		}
		p.Labels[v1alpha1.GenericComponentNameLabel] = v1alpha1.GenericComponentLabelValueKCM
	}
}

func WithExposedProviders(exposedProviders string) Opt {
	return func(p *v1alpha1.PluggableProvider) {
		p.Status.ExposedProviders = exposedProviders
	}
}
