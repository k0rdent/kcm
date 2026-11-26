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

package webhook

import (
	"testing"

	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/test/objects/multiclusterservice"
)

func TestMultiClusterServiceValidateCreate(t *testing.T) {
	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{
		Operation: admissionv1.Create,
	})

	tests := []struct {
		name     string
		mcs      *kcmv1.MultiClusterService
		err      string
		warnings admission.Warnings
	}{
		{
			name: "should succeed if reload is not set",
			mcs:  multiclusterservice.NewMultiClusterService(),
		},
		{
			name: "should fail if serviceSpec.reload is true",
			mcs: multiclusterservice.NewMultiClusterService(
				multiclusterservice.WithServiceSpec(kcmv1.ServiceSpec{Reload: true}), //nolint:staticcheck
			),
			warnings: admission.Warnings{"Failed to validate reload for provided MultiClusterService"},
			err:      "reload is not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			validator := &MultiClusterServiceValidator{}
			warn, err := validator.ValidateCreate(ctx, tt.mcs)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}
			g.Expect(warn).To(Equal(tt.warnings))
		})
	}
}

func TestMultiClusterServiceValidateUpdate(t *testing.T) {
	reloadSpec := kcmv1.ServiceSpec{Reload: true} //nolint:staticcheck

	tests := []struct {
		name     string
		oldMCS   *kcmv1.MultiClusterService
		newMCS   *kcmv1.MultiClusterService
		err      string
		warnings admission.Warnings
	}{
		{
			name:   "should succeed if reload stays unset",
			oldMCS: multiclusterservice.NewMultiClusterService(),
			newMCS: multiclusterservice.NewMultiClusterService(multiclusterservice.WithServiceTemplate("tmpl")),
		},
		{
			name:     "should fail if reload is newly introduced",
			oldMCS:   multiclusterservice.NewMultiClusterService(),
			newMCS:   multiclusterservice.NewMultiClusterService(multiclusterservice.WithServiceSpec(reloadSpec)),
			warnings: admission.Warnings{"Failed to validate reload for provided MultiClusterService"},
			err:      "reload is not supported",
		},
		{
			// A MultiClusterService created before this validation shipped must stay
			// editable: an update unrelated to reload must not be rejected just
			// because the (pre-existing) reload is unsupported.
			name: "should not fail when reload was already true and the update only adds a label",
			oldMCS: multiclusterservice.NewMultiClusterService(
				multiclusterservice.WithServiceSpec(reloadSpec),
			),
			newMCS: func() *kcmv1.MultiClusterService {
				mcs := multiclusterservice.NewMultiClusterService(
					multiclusterservice.WithServiceSpec(reloadSpec),
				)
				mcs.Labels = map[string]string{"foo": "bar"}
				return mcs
			}(),
		},
		{
			// The controller itself updates the object as part of tearing it down
			// (e.g. to remove its finalizer); that update must not be rejected
			// because of reload set before this validation shipped.
			name: "should not fail when reload is true and the object is being deleted",
			oldMCS: func() *kcmv1.MultiClusterService {
				mcs := multiclusterservice.NewMultiClusterService(
					multiclusterservice.WithServiceSpec(reloadSpec),
				)
				now := metav1.Now()
				mcs.DeletionTimestamp = &now
				mcs.Finalizers = []string{"some.finalizer/kcm"}
				return mcs
			}(),
			newMCS: func() *kcmv1.MultiClusterService {
				mcs := multiclusterservice.NewMultiClusterService(
					multiclusterservice.WithServiceSpec(reloadSpec),
				)
				now := metav1.Now()
				mcs.DeletionTimestamp = &now
				return mcs
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			validator := &MultiClusterServiceValidator{}
			warn, err := validator.ValidateUpdate(t.Context(), tt.oldMCS, tt.newMCS)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}
			g.Expect(warn).To(Equal(tt.warnings))
		})
	}
}
