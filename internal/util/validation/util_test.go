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

package validation

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func TestGetParent(t *testing.T) {
	t.Run("no region: returns Management", func(t *testing.T) {
		mgmt := &kcmv1.Management{ObjectMeta: metav1.ObjectMeta{Name: kcmv1.ManagementName}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(mgmt).Build()

		got, err := getParent(context.Background(), c, &kcmv1.Credential{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := got.(*kcmv1.Management); !ok {
			t.Errorf("got %T, want *kcmv1.Management", got)
		}
	})

	t.Run("no region, Management missing: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		_, err := getParent(context.Background(), c, &kcmv1.Credential{})
		if err == nil || !strings.Contains(err.Error(), "failed to get Management") {
			t.Fatalf("err = %v, want get Management error", err)
		}
	})

	t.Run("region set: returns Region", func(t *testing.T) {
		rgn := &kcmv1.Region{ObjectMeta: metav1.ObjectMeta{Name: "region1"}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(rgn).Build()

		cred := &kcmv1.Credential{Spec: kcmv1.CredentialSpec{Region: "region1"}}
		got, err := getParent(context.Background(), c, cred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r, ok := got.(*kcmv1.Region)
		if !ok || r.Name != "region1" {
			t.Errorf("got %+v, want Region region1", got)
		}
	})

	t.Run("region set but missing: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()

		cred := &kcmv1.Credential{Spec: kcmv1.CredentialSpec{Region: "missing-region"}}
		_, err := getParent(context.Background(), c, cred)
		if err == nil || !strings.Contains(err.Error(), "failed to get missing-region Region") {
			t.Fatalf("err = %v, want get Region error", err)
		}
	})
}
