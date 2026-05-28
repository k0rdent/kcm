// Copyright 2026
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

package clusterauditpolicy

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	DefaultName = "cl-audit-policy"
)

type Opt func(clAuditPolicy *kcmv1.ClusterAuditPolicy)

func New(opts ...Opt) *kcmv1.ClusterAuditPolicy {
	clAuditPolicy := &kcmv1.ClusterAuditPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultName,
			Namespace: metav1.NamespaceDefault,
		},
	}

	for _, opt := range opts {
		opt(clAuditPolicy)
	}
	return clAuditPolicy
}

func WithName(name string) Opt {
	return func(clAuditPolicy *kcmv1.ClusterAuditPolicy) {
		clAuditPolicy.Name = name
	}
}

func WithNamespace(namespace string) Opt {
	return func(clAuditPolicy *kcmv1.ClusterAuditPolicy) {
		clAuditPolicy.Namespace = namespace
	}
}

func WithSpec(auditPolicySpec kcmv1.ClusterAuditPolicySpec) Opt {
	return func(clAuditPolicy *kcmv1.ClusterAuditPolicy) {
		clAuditPolicy.Spec = auditPolicySpec
	}
}

func ManagedByKCM() Opt {
	return func(t *kcmv1.ClusterAuditPolicy) {
		if t.Labels == nil {
			t.Labels = make(map[string]string)
		}
		t.Labels[kcmv1.KCMManagedLabelKey] = kcmv1.KCMManagedLabelValue
	}
}
