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
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/test/objects/management"
	"github.com/K0rdent/kcm/test/objects/release"
	"github.com/K0rdent/kcm/test/scheme"
)

func TestReleaseValidateDelete(t *testing.T) {
	g := NewWithT(t)

	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Delete}})

	tests := []struct {
		name            string
		release         *v1alpha1.Release
		existingObjects []runtime.Object
		err             string
	}{
		{
			name:            "should fail if > 1 Management",
			release:         release.New(),
			existingObjects: []runtime.Object{management.NewManagement(), management.NewManagement(management.WithName("second"))},
			err:             "expected 1 Management object, got 2",
		},
		{
			name:            "should fail if Release is in use",
			release:         release.New(),
			existingObjects: []runtime.Object{management.NewManagement(management.WithRelease(release.DefaultName))},
			err:             fmt.Sprintf("release %s is still in use", release.DefaultName),
		},
		{
			name: "should fail if some providers are in use",
			release: release.New(release.WithProviders(
				v1alpha1.NamedProviderTemplate{CoreProviderTemplate: v1alpha1.CoreProviderTemplate{Template: "template-in-use-1"}},
				v1alpha1.NamedProviderTemplate{CoreProviderTemplate: v1alpha1.CoreProviderTemplate{Template: "template-in-use-2"}},
				v1alpha1.NamedProviderTemplate{CoreProviderTemplate: v1alpha1.CoreProviderTemplate{Template: "template-not-in-use"}}),
				release.WithCAPITemplateName("template-capi-in-use"),
				release.WithKCMTemplateName("template-kcm-in-use"),
			),
			existingObjects: []runtime.Object{management.NewManagement(
				management.WithRelease("some-release"),
				management.WithProviders(
					v1alpha1.Provider{Component: v1alpha1.Component{Template: "template-in-use-1"}},
					v1alpha1.Provider{Component: v1alpha1.Component{Template: "template-in-use-2"}},
				),
				management.WithCoreComponents(&v1alpha1.Core{
					KCM:  v1alpha1.Component{Template: "template-kcm-in-use"},
					CAPI: v1alpha1.Component{Template: "template-capi-in-use"},
				}),
			)},
			err: "the following ProviderTemplates associated with the Release are still in use: template-capi-in-use, template-kcm-in-use, template-in-use-1, template-in-use-2",
		},
		{
			name: "should succeed",
			release: release.New(release.WithProviders(
				v1alpha1.NamedProviderTemplate{CoreProviderTemplate: v1alpha1.CoreProviderTemplate{Template: "template-not-in-use"}},
			)),
			existingObjects: []runtime.Object{management.NewManagement(
				management.WithRelease("some-release"),
				management.WithProviders(
					v1alpha1.Provider{Component: v1alpha1.Component{Template: "template-in-use"}},
				),
			)},
		},
		{
			name:    "should succeed if Management doesn't exist",
			release: release.New(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			validator := &ReleaseValidator{Client: c}

			_, err := validator.ValidateDelete(ctx, tt.release)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err).To(MatchError(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}
		})
	}
}
