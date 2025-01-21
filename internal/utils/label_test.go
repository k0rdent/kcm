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

package utils_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/utils"
)

func TestAddLabel(t *testing.T) {
	obj := &kcmv1.Management{ObjectMeta: metav1.ObjectMeta{
		Labels: make(map[string]string),
	}}

	withLabels := func(kv ...string) {
		if len(kv) == 0 {
			return
		}
		if len(kv)&1 != 0 {
			panic("expected even number of args")
		}
		for k := range obj.Labels {
			delete(obj.Labels, k)
		}
		for i := range len(kv) / 2 {
			obj.Labels[kv[i*2]] = kv[i*2+1]
		}
	}

	type args struct {
		mutate     func()
		labelKey   string
		labelValue string
	}
	tests := []struct {
		name              string
		args              args
		wantLabelsUpdated bool
	}{
		{
			name: "no labels, expect updated map",
			args: args{
				mutate:     func() { withLabels() },
				labelKey:   "foo",
				labelValue: "bar",
			},
			wantLabelsUpdated: true,
		},
		{
			name: "key exist diff value, expect updated map",
			args: args{
				mutate:     func() { withLabels("foo", "diff") },
				labelKey:   "foo",
				labelValue: "bar",
			},
			wantLabelsUpdated: true,
		},
		{
			name: "key exist value is equal, expect no update required",
			args: args{
				mutate:     func() { withLabels("foo", "bar") },
				labelKey:   "foo",
				labelValue: "bar",
			},
		},
	}
	for _, tt := range tests {
		_ = tt
		t.Run(tt.name, func(t *testing.T) {
			tt.args.mutate()
			if gotLabelsUpdated := utils.AddLabel(obj, tt.args.labelKey, tt.args.labelValue); gotLabelsUpdated != tt.wantLabelsUpdated {
				t.Errorf("AddLabel() = %v, want %v", gotLabelsUpdated, tt.wantLabelsUpdated)
			}
		})
	}
}
