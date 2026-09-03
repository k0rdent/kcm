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

func TestValidateMCSDependencyOverall(t *testing.T) {
	t.Run("no dependencies: valid", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()
		mcs := &kcmv1.MultiClusterService{ObjectMeta: metav1.ObjectMeta{Name: "mcs1"}}

		if err := ValidateMCSDependencyOverall(context.Background(), c, mcs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("dependency does not exist: error", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()
		mcs := &kcmv1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Name: "mcs1"},
			Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"missing"}},
		}

		err := ValidateMCSDependencyOverall(context.Background(), c, mcs)
		if err == nil || !strings.Contains(err.Error(), "failed MCS dependency validation") {
			t.Fatalf("err = %v, want dependency validation error", err)
		}
	})

	t.Run("dependency cycle: error", func(t *testing.T) {
		other := kcmv1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Name: "mcs2"},
			Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"mcs1"}},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(&other).Build()

		mcs := &kcmv1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Name: "mcs1"},
			Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"mcs2"}},
		}

		err := ValidateMCSDependencyOverall(context.Background(), c, mcs)
		if err == nil || !strings.Contains(err.Error(), "failed MCS dependency cycle validation") {
			t.Fatalf("err = %v, want dependency cycle validation error", err)
		}
	})

	t.Run("valid dependency chain: no error", func(t *testing.T) {
		other := kcmv1.MultiClusterService{ObjectMeta: metav1.ObjectMeta{Name: "mcs2"}}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(&other).Build()

		mcs := &kcmv1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Name: "mcs1"},
			Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"mcs2"}},
		}

		if err := ValidateMCSDependencyOverall(context.Background(), c, mcs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestValidateMCSDelete(t *testing.T) {
	t.Run("no dependents: allowed", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()
		mcs := &kcmv1.MultiClusterService{ObjectMeta: metav1.ObjectMeta{Name: "mcs1"}}

		if err := ValidateMCSDelete(context.Background(), c, mcs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("another MCS depends on it: not allowed", func(t *testing.T) {
		dependent := kcmv1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{Name: "mcs2"},
			Spec:       kcmv1.MultiClusterServiceSpec{DependsOn: []string{"mcs1"}},
		}
		c := fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(&dependent).Build()

		mcs := &kcmv1.MultiClusterService{ObjectMeta: metav1.ObjectMeta{Name: "mcs1"}}
		err := ValidateMCSDelete(context.Background(), c, mcs)
		if err == nil || !strings.Contains(err.Error(), "other MultiClusterServices depend on it") {
			t.Fatalf("err = %v, want dependents-exist error", err)
		}
	})
}
