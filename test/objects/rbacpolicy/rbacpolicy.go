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

package rbacpolicy

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	DefaultName = "rbac-policy"
)

type Opt func(policy *kcmv1.RBACPolicy)

func New(opts ...Opt) *kcmv1.RBACPolicy {
	policy := &kcmv1.RBACPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultName,
			Namespace: metav1.NamespaceDefault,
		},
	}

	for _, opt := range opts {
		opt(policy)
	}
	return policy
}

func WithName(name string) Opt {
	return func(policy *kcmv1.RBACPolicy) {
		policy.Name = name
	}
}

func WithNamespace(namespace string) Opt {
	return func(policy *kcmv1.RBACPolicy) {
		policy.Namespace = namespace
	}
}

func WithSpec(spec kcmv1.RBACPolicySpec) Opt {
	return func(policy *kcmv1.RBACPolicy) {
		policy.Spec = spec
	}
}
