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

package webhook

import (
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/utils"
	am "github.com/K0rdent/kcm/test/objects/accessmanagement"
	"github.com/K0rdent/kcm/test/objects/management"
	"github.com/K0rdent/kcm/test/scheme"
)

func TestAccessManagementValidateCreate(t *testing.T) {
	g := NewWithT(t)

	ctx := t.Context()

	tests := []struct {
		name            string
		am              *v1alpha1.AccessManagement
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name:            "should fail if the AccessManagement object already exists",
			am:              am.NewAccessManagement(am.WithName("new")),
			existingObjects: []runtime.Object{am.NewAccessManagement(am.WithName(v1alpha1.AccessManagementName))},
			err:             "AccessManagement object already exists",
		},
		{
			name: "should succeed",
			am:   am.NewAccessManagement(am.WithName("new")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&v1alpha1.ClusterDeployment{}, v1alpha1.ClusterDeploymentTemplateIndexKey, v1alpha1.ExtractTemplateNameFromClusterDeployment).
				Build()
			validator := &AccessManagementValidator{Client: c, SystemNamespace: utils.DefaultSystemNamespace}
			warn, err := validator.ValidateCreate(ctx, tt.am)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				if err.Error() != tt.err {
					t.Fatalf("expected error '%s', got error: %s", tt.err, err.Error())
				}
			} else {
				g.Expect(err).To(Succeed())
			}
			if len(tt.warnings) > 0 {
				g.Expect(warn).To(Equal(tt.warnings))
			} else {
				g.Expect(warn).To(BeEmpty())
			}
		})
	}
}

func TestAccessManagementValidateDelete(t *testing.T) {
	g := NewWithT(t)

	ctx := t.Context()

	amName := "test"

	tests := []struct {
		name            string
		am              *v1alpha1.AccessManagement
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name:            "should fail if Management object exists and was not deleted",
			am:              am.NewAccessManagement(am.WithName(amName)),
			existingObjects: []runtime.Object{management.NewManagement()},
			err:             "AccessManagement deletion is forbidden",
		},
		{
			name: "should succeed if Management object is not found",
			am:   am.NewAccessManagement(am.WithName(amName)),
		},
		{
			name:            "should succeed if Management object was deleted",
			am:              am.NewAccessManagement(am.WithName(amName)),
			existingObjects: []runtime.Object{management.NewManagement(management.WithDeletionTimestamp(metav1.Now()))},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&v1alpha1.ClusterDeployment{}, v1alpha1.ClusterDeploymentTemplateIndexKey, v1alpha1.ExtractTemplateNameFromClusterDeployment).
				Build()
			validator := &AccessManagementValidator{Client: c, SystemNamespace: utils.DefaultSystemNamespace}
			warn, err := validator.ValidateDelete(ctx, tt.am)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				if err.Error() != tt.err {
					t.Fatalf("expected error '%s', got error: %s", tt.err, err.Error())
				}
			} else {
				g.Expect(err).To(Succeed())
			}
			if len(tt.warnings) > 0 {
				g.Expect(warn).To(Equal(tt.warnings))
			} else {
				g.Expect(warn).To(BeEmpty())
			}
		})
	}
}
