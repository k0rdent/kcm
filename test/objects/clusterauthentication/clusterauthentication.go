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

package clusterauthentication

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	DefaultName = "cl-auth"
)

type Opt func(clAuth *kcmv1.ClusterAuthentication)

func New(opts ...Opt) *kcmv1.ClusterAuthentication {
	clAuth := &kcmv1.ClusterAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultName,
			Namespace: metav1.NamespaceDefault,
		},
	}

	for _, opt := range opts {
		opt(clAuth)
	}
	return clAuth
}

func WithName(name string) Opt {
	return func(clAuth *kcmv1.ClusterAuthentication) {
		clAuth.Name = name
	}
}

func WithNamespace(namespace string) Opt {
	return func(clAuth *kcmv1.ClusterAuthentication) {
		clAuth.Namespace = namespace
	}
}

func WithAuthenticationConfiguration(authConf *kcmv1.AuthenticationConfiguration) Opt {
	return func(clAuth *kcmv1.ClusterAuthentication) {
		clAuth.Spec.AuthenticationConfiguration = authConf
	}
}

func WithCASecretRef(caSecretRef kcmv1.SecretKeyReference) Opt {
	return func(clAuth *kcmv1.ClusterAuthentication) {
		clAuth.Spec.CASecret = &caSecretRef
	}
}

func ManagedByKCM() Opt {
	return func(t *kcmv1.ClusterAuthentication) {
		if t.Labels == nil {
			t.Labels = make(map[string]string)
		}
		t.Labels[kcmv1.KCMManagedLabelKey] = kcmv1.KCMManagedLabelValue
	}
}
