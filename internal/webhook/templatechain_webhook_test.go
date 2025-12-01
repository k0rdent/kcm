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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	templates "github.com/K0rdent/kcm/test/objects/template"
	tc "github.com/K0rdent/kcm/test/objects/templatechain"
	"github.com/K0rdent/kcm/test/scheme"
)

func TestClusterTemplateChainValidateCreate(t *testing.T) {
	ctx := t.Context()

	const (
		upgradeFromTemplateName = "template-1-0-1"
		upgradeToTemplateName   = "template-1-0-2"
		testNamespace           = templates.DefaultNamespace
		systemNamespace         = "test-system"
		testChainName           = "test"
	)

	supportedTemplates := []kcmv1.SupportedTemplate{
		{
			Name: upgradeFromTemplateName,
			AvailableUpgrades: []kcmv1.AvailableUpgrade{
				{
					Name: upgradeToTemplateName,
				},
			},
		},
	}

	tests := []struct {
		name            string
		chain           *kcmv1.ClusterTemplateChain
		existingObjects []runtime.Object
		err             error
		warnings        admission.Warnings
	}{
		{
			name:  "should fail if spec is invalid: incorrect supported templates",
			chain: tc.NewClusterTemplateChain(tc.WithName(testChainName), tc.WithSupportedTemplates(supportedTemplates)),
			warnings: admission.Warnings{
				"template template-1-0-2 is allowed for upgrade but is not present in the list of '.spec.supportedTemplates'",
			},
			err: errInvalidTemplateChainSpec,
		},
		{
			name: "fails due to absent referenced clustertemplate in unmanaged chain",
			chain: tc.NewClusterTemplateChain(
				tc.WithName(testChainName),
				tc.WithNamespace(testNamespace),
				tc.WithSupportedTemplates([]kcmv1.SupportedTemplate{{Name: upgradeFromTemplateName}}),
			),
			err: apierrors.NewInvalid(
				schema.GroupKind{Group: kcmv1.GroupVersion.Group, Kind: kcmv1.ClusterTemplateChainKind},
				testChainName,
				field.ErrorList{field.NotFound(field.NewPath("spec", "supportedTemplates").Index(0).Child("name"), upgradeFromTemplateName)},
			),
		},
		{
			name: "fails due to absent referenced clustertemplate in managed and system chain",
			chain: tc.NewClusterTemplateChain(
				tc.WithName(testChainName),
				tc.WithNamespace(systemNamespace),
				tc.ManagedByKCM(),
				tc.WithSupportedTemplates([]kcmv1.SupportedTemplate{{Name: upgradeFromTemplateName}}),
			),
			err: apierrors.NewInvalid(
				schema.GroupKind{Group: kcmv1.GroupVersion.Group, Kind: kcmv1.ClusterTemplateChainKind},
				testChainName,
				field.ErrorList{field.NotFound(field.NewPath("spec", "supportedTemplates").Index(0).Child("name"), upgradeFromTemplateName)},
			),
		},
		{
			name: "succeeds being managed and non-system with an absent clustertemplate",
			chain: tc.NewClusterTemplateChain(
				tc.WithName(testChainName),
				tc.WithNamespace(testNamespace),
				tc.ManagedByKCM(),
				tc.WithSupportedTemplates([]kcmv1.SupportedTemplate{{Name: upgradeFromTemplateName}}),
			),
		},
		{
			name:  "should succeed",
			chain: tc.NewClusterTemplateChain(tc.WithName(testChainName), tc.WithNamespace(testNamespace), tc.WithSupportedTemplates(append(supportedTemplates, kcmv1.SupportedTemplate{Name: upgradeToTemplateName}))),
			existingObjects: []runtime.Object{
				templates.NewClusterTemplate(templates.WithName(upgradeFromTemplateName), templates.WithNamespace(testNamespace)),
				templates.NewClusterTemplate(templates.WithName(upgradeToTemplateName), templates.WithNamespace(testNamespace)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			validator := &ClusterTemplateChainValidator{Client: c, SystemNamespace: systemNamespace}
			warn, err := validator.ValidateCreate(ctx, tt.chain)
			if tt.err != nil {
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

	const (
		testChainName   = "myapp-chain"
		testNamespace   = "test"
		systemNamespace = "test-system"
		tplv1           = "myapp-v1"
		tplv2           = "myapp-v2"
		tplv21          = "myapp-v2.1"
		tplv22          = "myapp-v2.2"
		tplv3           = "myapp-v3"
	)

	serviceChain := tc.NewServiceTemplateChain(tc.WithNamespace(testNamespace), tc.WithName(testChainName),
		tc.WithSupportedTemplates([]kcmv1.SupportedTemplate{
			{
				Name: tplv1,
				AvailableUpgrades: []kcmv1.AvailableUpgrade{
					{Name: "myapp-v2"},
					{Name: "myapp-v2.1"},
					{Name: "myapp-v2.2"},
				},
			},
			{
				Name: tplv2,
				AvailableUpgrades: []kcmv1.AvailableUpgrade{
					{Name: "myapp-v2.1"},
					{Name: "myapp-v2.2"},
					{Name: "myapp-v3"},
				},
			},
			{
				Name: tplv21,
				AvailableUpgrades: []kcmv1.AvailableUpgrade{
					{Name: "myapp-v2.2"},
					{Name: "myapp-v3"},
				},
			},
			{
				Name: tplv22,
				AvailableUpgrades: []kcmv1.AvailableUpgrade{
					{Name: "myapp-v3"},
				},
			},
			{
				Name: tplv3,
			},
		}),
	)

	tests := []struct {
		title        string
		chain        *kcmv1.ServiceTemplateChain
		existingObjs []runtime.Object
		warnings     admission.Warnings
		err          error
	}{
		{
			title: "should succeed",
			chain: serviceChain,
			existingObjs: []runtime.Object{
				templates.NewServiceTemplate(templates.WithNamespace(testNamespace), templates.WithName(tplv1)),
				templates.NewServiceTemplate(templates.WithNamespace(testNamespace), templates.WithName(tplv2)),
				templates.NewServiceTemplate(templates.WithNamespace(testNamespace), templates.WithName(tplv21)),
				templates.NewServiceTemplate(templates.WithNamespace(testNamespace), templates.WithName(tplv22)),
				templates.NewServiceTemplate(templates.WithNamespace(testNamespace), templates.WithName(tplv3)),
			},
		},
		{
			title: "fails due to absent referenced servicetemplate in unmanaged chain",
			chain: tc.NewServiceTemplateChain(
				tc.WithName(testChainName),
				tc.WithNamespace(testNamespace),
				tc.WithSupportedTemplates([]kcmv1.SupportedTemplate{{Name: tplv1}}),
			),
			err: apierrors.NewInvalid(
				schema.GroupKind{Group: kcmv1.GroupVersion.Group, Kind: kcmv1.ServiceTemplateChainKind},
				testChainName,
				field.ErrorList{field.NotFound(field.NewPath("spec", "supportedTemplates").Index(0).Child("name"), tplv1)},
			),
		},
		{
			title: "fails due to absent referenced servicetemplate in managed and system chain",
			chain: tc.NewServiceTemplateChain(
				tc.WithName(testChainName),
				tc.WithNamespace(systemNamespace),
				tc.ManagedByKCM(),
				tc.WithSupportedTemplates([]kcmv1.SupportedTemplate{{Name: tplv1}}),
			),
			err: apierrors.NewInvalid(
				schema.GroupKind{Group: kcmv1.GroupVersion.Group, Kind: kcmv1.ServiceTemplateChainKind},
				testChainName,
				field.ErrorList{field.NotFound(field.NewPath("spec", "supportedTemplates").Index(0).Child("name"), tplv1)},
			),
		},
		{
			title: "succeeds being managed and non-system with an absent servicetemplate",
			chain: tc.NewServiceTemplateChain(
				tc.WithName(testChainName),
				tc.WithNamespace(testNamespace),
				tc.ManagedByKCM(),
				tc.WithSupportedTemplates([]kcmv1.SupportedTemplate{{Name: tplv1}}),
			),
		},
		{
			title: "should fail if a ServiceTemplate exists and is allowed for update but is supported in the chain",
			chain: func() *kcmv1.ServiceTemplateChain {
				tmpls := []kcmv1.SupportedTemplate{}
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
			err: errInvalidTemplateChainSpec,
		},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjs...).Build()
			validator := ServiceTemplateChainValidator{Client: c, SystemNamespace: systemNamespace}
			warn, err := validator.ValidateCreate(ctx, tt.chain)
			if tt.err != nil {
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
