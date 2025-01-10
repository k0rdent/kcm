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

package accessmanagement

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/K0rdent/kcm/api/v1alpha1"
)

const (
	DefaultName = "kcm-am"
)

type Opt func(am *v1alpha1.AccessManagement)

func NewAccessManagement(opts ...Opt) *v1alpha1.AccessManagement {
	am := &v1alpha1.AccessManagement{
		ObjectMeta: metav1.ObjectMeta{
			Name: DefaultName,
		},
	}

	for _, opt := range opts {
		opt(am)
	}
	return am
}

func WithName(name string) Opt {
	return func(am *v1alpha1.AccessManagement) {
		am.Name = name
	}
}

func WithAccessRules(accessRules []v1alpha1.AccessRule) Opt {
	return func(am *v1alpha1.AccessManagement) {
		am.Spec.AccessRules = accessRules
	}
}
