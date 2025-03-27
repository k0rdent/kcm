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
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/K0rdent/kcm/api/v1alpha1"
	tc "github.com/K0rdent/kcm/test/objects/templatechain"
	"github.com/K0rdent/kcm/test/scheme"
)

func TestClusterTemplateChainValidateCreate(t *testing.T) {
	ctx := t.Context()

	upgradeFromTemplateName := "template-1-0-1"
	upgradeToTemplateName := "template-1-0-2"
	supportedTemplates := []v1alpha1.SupportedTemplate{
		{
			Name: upgradeFromTemplateName,
			AvailableUpgrades: []v1alpha1.AvailableUpgrade{
				{
					Name: upgradeToTemplateName,
				},
			},
		},
	}

	tests := []struct {
		name            string
		chain           *v1alpha1.ClusterTemplateChain
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name:  "should fail if spec is invalid: incorrect supported templates",
			chain: tc.NewClusterTemplateChain(tc.WithName("test"), tc.WithSupportedTemplates(supportedTemplates)),
			warnings: admission.Warnings{
				"template template-1-0-2 is allowed for upgrade but is not present in the list of '.spec.supportedTemplates'",
			},
			err: "the template chain spec is invalid",
		},
		{
			name:  "should succeed",
			chain: tc.NewClusterTemplateChain(tc.WithName("test"), tc.WithSupportedTemplates(append(supportedTemplates, v1alpha1.SupportedTemplate{Name: upgradeToTemplateName}))),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			validator := &ClusterTemplateChainValidator{Client: c}
			warn, err := validator.ValidateCreate(ctx, tt.chain)
			if tt.err != "" {
				g.Expect(err).To(MatchError(tt.err))
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

func TestServiceTemplateChainValidateCreate(t *testing.T) {
	ctx := t.Context()

	serviceChain := tc.NewServiceTemplateChain(tc.WithNamespace("test"), tc.WithName("myapp-chain"),
		tc.WithSupportedTemplates([]v1alpha1.SupportedTemplate{
			{
				Name: "myapp-v1",
				AvailableUpgrades: []v1alpha1.AvailableUpgrade{
					{Name: "myapp-v2"},
					{Name: "myapp-v2.1"},
					{Name: "myapp-v2.2"},
				},
			},
			{
				Name: "myapp-v2",
				AvailableUpgrades: []v1alpha1.AvailableUpgrade{
					{Name: "myapp-v2.1"},
					{Name: "myapp-v2.2"},
					{Name: "myapp-v3"},
				},
			},
			{
				Name: "myapp-v2.1",
				AvailableUpgrades: []v1alpha1.AvailableUpgrade{
					{Name: "myapp-v2.2"},
					{Name: "myapp-v3"},
				},
			},
			{
				Name: "myapp-v2.2",
				AvailableUpgrades: []v1alpha1.AvailableUpgrade{
					{Name: "myapp-v3"},
				},
			},
			{
				Name: "myapp-v3",
			},
		}),
	)

	tests := []struct {
		title        string
		chain        *v1alpha1.ServiceTemplateChain
		existingObjs []runtime.Object
		warnings     admission.Warnings
		err          string
	}{
		{
			title: "should succeed",
			chain: serviceChain,
		},
		{
			title: "should fail if a ServiceTemplate exists and is allowed for update but is supported in the chain",
			chain: func() *v1alpha1.ServiceTemplateChain {
				tmpls := []v1alpha1.SupportedTemplate{}
				for _, s := range serviceChain.Spec.SupportedTemplates {
					// remove myapp-v3 from supportedTemplates
					if s.Name == "myapp-v3" {
						continue
					}
					tmpls = append(tmpls, s)
				}

				return tc.NewServiceTemplateChain(
					tc.WithNamespace(serviceChain.Namespace),
					tc.WithName(serviceChain.Name),
					tc.WithSupportedTemplates(tmpls))
			}(),
			warnings: admission.Warnings{
				"template myapp-v3 is allowed for upgrade but is not present in the list of '.spec.supportedTemplates'",
			},
			err: errInvalidTemplateChainSpec.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjs...).Build()
			validator := ServiceTemplateChainValidator{Client: c}
			warn, err := validator.ValidateCreate(ctx, tt.chain)
			if tt.err != "" {
				g.Expect(err).To(MatchError(tt.err))
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
