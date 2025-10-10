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

package clusteridentity

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const DefaultName = "clusterIdentity"

type Opt func(ci *unstructured.Unstructured)

func New(opts ...Opt) *unstructured.Unstructured {
	result := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"name": DefaultName,
			},
		},
	}

	for _, opt := range opts {
		opt(result)
	}
	return result
}

func WithName(name string) Opt {
	return func(ci *unstructured.Unstructured) {
		ci.SetName(name)
	}
}

func WithNamespace(namespace string) Opt {
	return func(ci *unstructured.Unstructured) {
		ci.SetNamespace(namespace)
	}
}

func WithKind(kind string) Opt {
	return func(ci *unstructured.Unstructured) {
		ci.SetKind(kind)
	}
}

func WithAPIVersion(apiVersion string) Opt {
	return func(ci *unstructured.Unstructured) {
		ci.SetAPIVersion(apiVersion)
	}
}

func WithLabels(labels map[string]string) Opt {
	return func(ci *unstructured.Unstructured) {
		ci.SetLabels(labels)
	}
}

func WithData(data map[string]any) Opt {
	return func(ci *unstructured.Unstructured) {
		if ci.Object == nil {
			ci.Object = make(map[string]any)
		}
		for k, v := range data {
			ci.Object[k] = v
		}
	}
}
