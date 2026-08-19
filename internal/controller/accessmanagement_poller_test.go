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

package controller

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

func Test_accessManagementPollEnqueue(t *testing.T) {
	t.Parallel()

	t.Run("no AccessManagement exists: no-op, no error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		r := &AccessManagementReconciler{Client: fake.NewClientBuilder().WithScheme(testscheme.Scheme).Build()}

		got, err := r.accessManagementPollEnqueue(t.Context())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(got).To(BeEmpty())
	})

	t.Run("AccessManagement exists: always emitted", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		am := &kcmv1.AccessManagement{ObjectMeta: metav1.ObjectMeta{Name: kcmv1.AccessManagementName}}
		r := &AccessManagementReconciler{Client: fake.NewClientBuilder().WithScheme(testscheme.Scheme).WithObjects(am).Build()}

		got, err := r.accessManagementPollEnqueue(t.Context())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(got).To(HaveLen(1))
		g.Expect(got[0].Name).To(Equal(kcmv1.AccessManagementName))
	})

	t.Run("Get fails with a non-NotFound error: propagated", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		wantErr := errors.New("boom")
		r := &AccessManagementReconciler{
			Client: fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return wantErr
					},
				}).
				Build(),
		}

		got, err := r.accessManagementPollEnqueue(t.Context())
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.Is(err, wantErr)).To(BeTrue())
		g.Expect(got).To(BeEmpty())
	})
}
