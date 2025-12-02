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

package datasource

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	DefaultName = "test-datasource"
)

type Opt func(*kcmv1.DataSource)

func New(opts ...Opt) *kcmv1.DataSource {
	ds := &kcmv1.DataSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultName,
			Namespace: metav1.NamespaceDefault,
		},
		Spec: kcmv1.DataSourceSpec{
			Type:      kcmv1.KineTypePostresql,
			Endpoints: []string{},
			Auth: kcmv1.DataSourceAuth{
				Username: kcmv1.SecretKeyReference{
					SecretReference: corev1.SecretReference{Name: "fake-secret"},
					Key:             "fake-uname",
				},
				Password: kcmv1.SecretKeyReference{
					SecretReference: corev1.SecretReference{Name: "fake-secret"},
					Key:             "fake-password",
				},
			},
		},
	}

	for _, opt := range opts {
		opt(ds)
	}

	return ds
}

func WithName(name string) Opt {
	return func(ds *kcmv1.DataSource) {
		ds.Name = name
	}
}

func WithNamespace(namespace string) Opt {
	return func(ds *kcmv1.DataSource) {
		ds.Namespace = namespace
	}
}

func WithLabels(kvs ...string) Opt {
	return func(ds *kcmv1.DataSource) {
		if ds.Labels == nil {
			ds.Labels = make(map[string]string)
		}

		if len(kvs) == 0 {
			return
		}

		if len(kvs)&1 != 0 {
			panic("expected even number of args")
		}

		for i := range len(kvs) / 2 {
			ds.Labels[kvs[i*2]] = kvs[i*2+1]
		}
	}
}
