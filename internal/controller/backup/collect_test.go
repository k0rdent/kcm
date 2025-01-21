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

package backup

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_sortDedup(t *testing.T) {
	type testInput struct {
		name      string
		selectors []*metav1.LabelSelector
		expected  []*metav1.LabelSelector
	}

	tests := []testInput{
		{
			name: "no dups unordered",
			selectors: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
				{
					MatchLabels: map[string]string{"app": "bar"},
				},
			},
			expected: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "bar"},
				},
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
			},
		},
		{
			name: "some dups in keys",
			selectors: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
				{
					MatchLabels: map[string]string{"app": "bar"},
				},
			},
			expected: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "bar"},
				},
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
			},
		},
		{
			name: "all dups",
			selectors: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
			},
			expected: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "foo"},
				},
			},
		},
		{
			name: "huge dups unordered",
			selectors: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"hmc.mirantis.com/component": "hmc"},
				},
				{
					MatchLabels: map[string]string{"controller.cert-manager.io/fao": "true"},
				},
				{
					MatchLabels: map[string]string{"helm.toolkit.fluxcd.io/name": "hmc"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "cluster-api"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"helm.toolkit.fluxcd.io/name": "unusual-cluster-name"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/cluster-name": "unusual-cluster-name"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-azure"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-azure"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-vsphere"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-internal"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-openstack"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-vsphere"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-azure"},
				},
			},
			expected: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/cluster-name": "unusual-cluster-name"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "bootstrap-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "cluster-api"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "control-plane-k0smotron"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-aws"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-azure"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-internal"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-openstack"},
				},
				{
					MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "infrastructure-vsphere"},
				},
				{
					MatchLabels: map[string]string{"controller.cert-manager.io/fao": "true"},
				},
				{
					MatchLabels: map[string]string{"helm.toolkit.fluxcd.io/name": "hmc"},
				},
				{
					MatchLabels: map[string]string{"helm.toolkit.fluxcd.io/name": "unusual-cluster-name"},
				},
				{
					MatchLabels: map[string]string{"hmc.mirantis.com/component": "hmc"},
				},
			},
		},
	}

	for _, test := range tests {
		actual := sortDedup(test.selectors)
		if !reflect.DeepEqual(actual, test.expected) {
			t.Errorf("sortDedup(%s): \n\tactual:\n\t%v\n\n\twant:\n\t%v", test.name, actual, test.expected)
		}
	}
}
