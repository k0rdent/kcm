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

package credential

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	DefaultName = "credential"
)

type Opt func(credential *kcmv1.Credential)

func NewCredential(opts ...Opt) *kcmv1.Credential {
	p := &kcmv1.Credential{
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
	return func(p *kcmv1.Credential) {
		p.Name = name
	}
}

func WithNamespace(namespace string) Opt {
	return func(p *kcmv1.Credential) {
		p.Namespace = namespace
	}
}

func WithIdentityRef(idtyRef *corev1.ObjectReference) Opt {
	return func(p *kcmv1.Credential) {
		p.Spec.IdentityRef = idtyRef
	}
}

func WithLabels(labels map[string]string) Opt {
	return func(p *kcmv1.Credential) {
		p.Labels = labels
	}
}

func WithRegion(region string) Opt {
	return func(p *kcmv1.Credential) {
		p.Spec.Region = region
	}
}

func WithReady(ready bool) Opt {
	return func(p *kcmv1.Credential) {
		p.Status.Ready = ready
	}
}

func ManagedByKCM() Opt {
	return func(t *kcmv1.Credential) {
		if t.Labels == nil {
			t.Labels = make(map[string]string)
		}
		t.Labels[kcmv1.KCMManagedLabelKey] = kcmv1.KCMManagedLabelValue
	}
}
