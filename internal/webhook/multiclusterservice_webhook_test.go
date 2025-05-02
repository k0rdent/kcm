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
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/test/objects/multiclusterservice"
	"github.com/K0rdent/kcm/test/objects/template"
	"github.com/K0rdent/kcm/test/scheme"
)

const (
	testMCSName          = "testmcs"
	testSvcTemplate1Name = "test-servicetemplate-1"
	testSvcTemplate2Name = "test-servicetemplate-2"
	testSystemNamespace  = "test-system-namespace"
)

func TestMultiClusterServiceValidateCreate(t *testing.T) {
	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
		},
	})

	tests := []struct {
		name            string
		mcs             *kcmv1.MultiClusterService
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name: "should fail if the ServiceTemplates are not found in system namespace",
			mcs: multiclusterservice.NewMultiClusterService(
				multiclusterservice.WithName(testMCSName),
				multiclusterservice.WithServiceTemplate(testSvcTemplate1Name),
			),
			existingObjects: []runtime.Object{
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithNamespace("othernamespace"),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
			err: apierrors.NewNotFound(schema.GroupResource{Group: kcmv1.GroupVersion.Group, Resource: "servicetemplates"}, testSvcTemplate1Name).Error(),
		},
		{
			name: "should fail if the ServiceTemplates were found but are invalid",
			mcs: multiclusterservice.NewMultiClusterService(
				multiclusterservice.WithName(testMCSName),
				multiclusterservice.WithServiceTemplate(testSvcTemplate1Name),
			),
			existingObjects: []runtime.Object{
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithNamespace(testSystemNamespace),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
				),
			},
			err: fmt.Sprintf("the MultiClusterService is invalid: the ServiceTemplate %s/%s is invalid with the error: validation error example", testSystemNamespace, testSvcTemplate1Name),
		},
		{
			name: "should succeed",
			mcs: multiclusterservice.NewMultiClusterService(
				multiclusterservice.WithName(testMCSName),
				multiclusterservice.WithServiceTemplate(testSvcTemplate1Name),
			),
			existingObjects: []runtime.Object{
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithNamespace(testSystemNamespace),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
		},
		{
			name: "should succeed with multiple serviceTemplates",
			mcs: multiclusterservice.NewMultiClusterService(
				multiclusterservice.WithName(testMCSName),
				multiclusterservice.WithServiceTemplate(testSvcTemplate1Name),
				multiclusterservice.WithServiceTemplate(testSvcTemplate2Name),
			),
			existingObjects: []runtime.Object{
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithNamespace(testSystemNamespace),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate2Name),
					template.WithNamespace(testSystemNamespace),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
		},
		{
			name: "should succeed without any serviceTemplates",
			mcs: multiclusterservice.NewMultiClusterService(
				multiclusterservice.WithName(testMCSName),
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			validator := &MultiClusterServiceValidator{Client: c, SystemNamespace: testSystemNamespace}
			warn, err := validator.ValidateCreate(ctx, tt.mcs)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.err))
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

func TestMultiClusterServiceValidateUpdate(t *testing.T) {
	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
		},
	})

	oldMCS := multiclusterservice.NewMultiClusterService(
		multiclusterservice.WithName(testMCSName),
		multiclusterservice.WithServiceTemplate(testSvcTemplate1Name),
	)

	tests := []struct {
		name            string
		newMCS          *kcmv1.MultiClusterService
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name: "should fail if the ServiceTemplates are not found in system namespace",
			newMCS: multiclusterservice.NewMultiClusterService(
				multiclusterservice.WithName(testMCSName),
				multiclusterservice.WithServiceTemplate(testSvcTemplate1Name),
				multiclusterservice.WithServiceTemplate(testSvcTemplate2Name),
			),
			existingObjects: []runtime.Object{
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithNamespace(testSystemNamespace),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate2Name),
					template.WithNamespace("othernamespace"),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
			err: apierrors.NewNotFound(schema.GroupResource{Group: kcmv1.GroupVersion.Group, Resource: "servicetemplates"}, testSvcTemplate2Name).Error(),
		},
		{
			name: "should fail if the ServiceTemplates were found but are invalid",
			newMCS: multiclusterservice.NewMultiClusterService(
				multiclusterservice.WithName(testMCSName),
				multiclusterservice.WithServiceTemplate(testSvcTemplate1Name),
				multiclusterservice.WithServiceTemplate(testSvcTemplate2Name),
			),
			existingObjects: []runtime.Object{
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithNamespace(testSystemNamespace),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate2Name),
					template.WithNamespace(testSystemNamespace),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
				),
			},
			err: fmt.Sprintf("the MultiClusterService is invalid: the ServiceTemplate %s/%s is invalid with the error: validation error example", testSystemNamespace, testSvcTemplate2Name),
		},
		{
			name: "should succeed if another template is added",
			newMCS: multiclusterservice.NewMultiClusterService(
				multiclusterservice.WithName(oldMCS.Name),
				multiclusterservice.WithServiceTemplate(testSvcTemplate1Name),
				multiclusterservice.WithServiceTemplate(testSvcTemplate2Name),
			),
			existingObjects: []runtime.Object{
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithNamespace(testSystemNamespace),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate2Name),
					template.WithNamespace(testSystemNamespace),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
		},
		{
			name: "should succeed if all templates removed",
			newMCS: multiclusterservice.NewMultiClusterService(
				multiclusterservice.WithName(oldMCS.Name),
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			validator := &MultiClusterServiceValidator{Client: c, SystemNamespace: testSystemNamespace}
			warn, err := validator.ValidateUpdate(ctx, oldMCS, tt.newMCS)
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
