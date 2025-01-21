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

package release

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/K0rdent/kcm/api/v1alpha1"
)

const (
	DefaultName = "release-test-0-0-1"

	DefaultCAPITemplateName = "cluster-api-test-0-0-1"
	DefaultKCMTemplateName  = "kcm-test-0-0-1"
)

type Opt func(*v1alpha1.Release)

func New(opts ...Opt) *v1alpha1.Release {
	release := &v1alpha1.Release{
		ObjectMeta: metav1.ObjectMeta{
			Name: DefaultName,
		},
		Spec: v1alpha1.ReleaseSpec{
			KCM: v1alpha1.CoreProviderTemplate{
				Template: DefaultKCMTemplateName,
			},
			CAPI: v1alpha1.CoreProviderTemplate{
				Template: DefaultCAPITemplateName,
			},
		},
		Status: v1alpha1.ReleaseStatus{
			Ready: true,
		},
	}

	for _, opt := range opts {
		opt(release)
	}

	return release
}

func WithName(name string) Opt {
	return func(r *v1alpha1.Release) {
		r.Name = name
	}
}

func WithKCMTemplateName(v string) Opt {
	return func(r *v1alpha1.Release) {
		r.Spec.KCM.Template = v
	}
}

func WithCAPITemplateName(v string) Opt {
	return func(r *v1alpha1.Release) {
		r.Spec.CAPI.Template = v
	}
}

func WithProviders(v ...v1alpha1.NamedProviderTemplate) Opt {
	return func(r *v1alpha1.Release) {
		r.Spec.Providers = v
	}
}

func WithReadyStatus(ready bool) Opt {
	return func(r *v1alpha1.Release) {
		r.Status.Ready = ready
	}
}
