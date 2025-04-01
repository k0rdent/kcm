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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/test/objects/clusterdeployment"
	"github.com/K0rdent/kcm/test/objects/management"
	"github.com/K0rdent/kcm/test/objects/multiclusterservice"
	"github.com/K0rdent/kcm/test/objects/release"
	"github.com/K0rdent/kcm/test/objects/template"
	tc "github.com/K0rdent/kcm/test/objects/templatechain"
	"github.com/K0rdent/kcm/test/scheme"
)

func TestProviderTemplateValidateDelete(t *testing.T) {
	ctx := t.Context()

	const (
		templateName = "mytemplate"
	)
	tmpl := template.NewProviderTemplate(template.WithName(templateName))

	releaseName := "kcm-0-0-3"

	tests := []struct {
		title           string
		template        *v1alpha1.ProviderTemplate
		existingObjects []runtime.Object
		warnings        admission.Warnings
		err             string
	}{
		{
			title:    "should fail if the core ProviderTemplate defined in the Management spec is removed",
			template: tmpl,
			existingObjects: []runtime.Object{
				management.NewManagement(management.WithRelease(releaseName), management.WithCoreComponents(&v1alpha1.Core{
					KCM: v1alpha1.Component{
						Template: templateName,
					},
				})),
				release.New(release.WithName(releaseName)),
			},
			warnings: admission.Warnings{fmt.Sprintf("The ProviderTemplate %s cannot be removed while it is used in the Management spec", templateName)},
			err:      errTemplateDeletionForbidden.Error(),
		},
		{
			title: "should fail if the template is part of one of the existing Releases",
			template: template.NewProviderTemplate(
				template.WithName(templateName),
				template.WithOwnerReference([]metav1.OwnerReference{
					{
						APIVersion: v1alpha1.GroupVersion.String(),
						Kind:       v1alpha1.ReleaseKind,
						Name:       "kcm-0-0-3",
					},
					{
						APIVersion: v1alpha1.GroupVersion.String(),
						Kind:       v1alpha1.ReleaseKind,
						Name:       "kcm-0-0-4",
					},
				}),
			),
			existingObjects: []runtime.Object{
				management.NewManagement(management.WithRelease(releaseName)),
				release.New(release.WithName("kcm-0-0-3")),
				release.New(release.WithName("kcm-0-0-4")),
			},
			warnings: admission.Warnings{fmt.Sprintf("The ProviderTemplate %s cannot be removed while it is part of existing Releases: kcm-0-0-3, kcm-0-0-4", templateName)},
			err:      errTemplateDeletionForbidden.Error(),
		},
		{
			title:    "should succeed if the provider is not enabled in Management spec and is not a part of one of the existing Release",
			template: tmpl,
			existingObjects: []runtime.Object{
				management.NewManagement(
					management.WithRelease(releaseName),
					management.WithCoreComponents(&v1alpha1.Core{}),
					management.WithProviders(v1alpha1.Provider{
						Name: "cluster-api-provider-aws",
						Component: v1alpha1.Component{
							Template: "cluster-api-provider-aws-0-0-2",
						},
					},
					)),
				release.New(release.WithName(releaseName)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.
				NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&v1alpha1.ClusterDeployment{}, v1alpha1.ClusterDeploymentServiceTemplatesIndexKey, v1alpha1.ExtractServiceTemplateNamesFromClusterDeployment).
				Build()

			validator := &ProviderTemplateValidator{
				TemplateValidator{
					Client:          c,
					SystemNamespace: testSystemNamespace,
					templateKind:    v1alpha1.ProviderTemplateKind,
				},
			}

			warn, err := validator.ValidateDelete(ctx, tt.template)
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

func TestClusterTemplateValidateDelete(t *testing.T) {
	ctx := t.Context()

	const (
		templateName      = "mytemplate"
		templateNamespace = "mynamespace"
	)

	tpl := template.NewClusterTemplate(template.WithName(templateName), template.WithNamespace(templateNamespace))

	tests := []struct {
		title           string
		template        *v1alpha1.ClusterTemplate
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			title:    "should fail if ClusterDeployment object referencing the template exists in the same namespace",
			template: tpl,
			existingObjects: []runtime.Object{clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithNamespace(templateNamespace),
				clusterdeployment.WithClusterTemplate(tpl.Name),
			)},
			warnings: admission.Warnings{"The ClusterTemplate object can't be removed if ClusterDeployment objects referencing it still exist"},
			err:      "template deletion is forbidden",
		},
		{
			title: "should fail if the template is owned by one or more ClusterTemplateChains",
			template: template.NewClusterTemplate(
				template.WithName(templateName),
				template.WithNamespace(templateNamespace),
				template.WithOwnerReference([]metav1.OwnerReference{
					{
						APIVersion: v1alpha1.GroupVersion.String(),
						Kind:       v1alpha1.ClusterTemplateChainKind,
						Name:       "test-chain",
					},
				}),
			),
			existingObjects: []runtime.Object{
				tc.NewClusterTemplateChain(
					tc.WithName("test-chain"),
					tc.WithNamespace(templateNamespace),
					tc.WithSupportedTemplates(
						[]v1alpha1.SupportedTemplate{
							{
								Name: templateName,
							},
						}),
				),
			},
			warnings: admission.Warnings{"The ClusterTemplate object can't be removed if it is managed by ClusterTemplateChain: test-chain"},
			err:      "template deletion is forbidden",
		},
		{
			title: "should succeed if the template is owned by one or more ClusterTemplateChains that were already removed",
			template: template.NewClusterTemplate(
				template.WithName(templateName),
				template.WithNamespace(templateNamespace),
				template.WithOwnerReference([]metav1.OwnerReference{
					{
						APIVersion: v1alpha1.GroupVersion.String(),
						Kind:       v1alpha1.ClusterTemplateChainKind,
						Name:       "test-chain",
					},
				}),
			),
		},
		{
			title:    "should succeed if some ClusterDeployment from another namespace references the template with the same name",
			template: tpl,
			existingObjects: []runtime.Object{clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithNamespace("new"),
				clusterdeployment.WithClusterTemplate(templateName),
			)},
		},
		{
			title:           "should succeed because no ClusterDeployment or ClusterTemplateChain references the template",
			template:        tpl,
			existingObjects: []runtime.Object{clusterdeployment.NewClusterDeployment()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&v1alpha1.ClusterDeployment{}, v1alpha1.ClusterDeploymentTemplateIndexKey, v1alpha1.ExtractTemplateNameFromClusterDeployment).
				Build()
			validator := &ClusterTemplateValidator{
				TemplateValidator: TemplateValidator{
					Client:            c,
					SystemNamespace:   testSystemNamespace,
					templateKind:      v1alpha1.ClusterTemplateKind,
					templateChainKind: v1alpha1.ClusterTemplateChainKind,
				},
			}

			warn, err := validator.ValidateDelete(ctx, tt.template)
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

func TestServiceTemplateValidateDelete(t *testing.T) {
	ctx := t.Context()

	const (
		templateName      = "mytemplate"
		templateNamespace = "mynamespace"
	)
	tmpl := template.NewServiceTemplate(template.WithNamespace(templateNamespace), template.WithName(templateName))

	tests := []struct {
		title           string
		template        *v1alpha1.ServiceTemplate
		existingObjects []runtime.Object
		warnings        admission.Warnings
		err             string
	}{
		{
			title:    "should fail if ClusterDeployment exists in same namespace",
			template: tmpl,
			existingObjects: []runtime.Object{
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithNamespace(templateNamespace),
					clusterdeployment.WithServiceTemplate(templateName),
				),
			},
			warnings: admission.Warnings{"The ServiceTemplate object can't be removed if ClusterDeployment objects referencing it still exist"},
			err:      errTemplateDeletionForbidden.Error(),
		},
		{
			title: "should fail if the template is owned by one or more ServiceTemplateChains",
			template: template.NewServiceTemplate(
				template.WithName(templateName),
				template.WithNamespace(templateNamespace),
				template.WithOwnerReference([]metav1.OwnerReference{
					{
						APIVersion: v1alpha1.GroupVersion.String(),
						Kind:       v1alpha1.ServiceTemplateChainKind,
						Name:       "test-chain",
					},
				}),
			),
			existingObjects: []runtime.Object{
				tc.NewServiceTemplateChain(
					tc.WithName("test-chain"),
					tc.WithNamespace(templateNamespace),
					tc.WithSupportedTemplates(
						[]v1alpha1.SupportedTemplate{
							{
								Name: templateName,
							},
						}),
				),
			},
			warnings: admission.Warnings{"The ServiceTemplate object can't be removed if it is managed by ServiceTemplateChain: test-chain"},
			err:      "template deletion is forbidden",
		},
		{
			title: "should succeed if the template is owned by one or more ServiceTemplateChains that were already removed",
			template: template.NewServiceTemplate(
				template.WithName(templateName),
				template.WithNamespace(templateNamespace),
				template.WithOwnerReference([]metav1.OwnerReference{
					{
						APIVersion: v1alpha1.GroupVersion.String(),
						Kind:       v1alpha1.ServiceTemplateChainKind,
						Name:       "test-chain",
					},
				}),
			),
		},
		{
			title:    "should succeed if ClusterDeployment referencing ServiceTemplate is another namespace",
			template: tmpl,
			existingObjects: []runtime.Object{
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithNamespace("someothernamespace"),
					clusterdeployment.WithServiceTemplate(tmpl.Name),
				),
			},
		},
		{
			title:           "should succeed because no cluster references the template",
			template:        tmpl,
			existingObjects: []runtime.Object{clusterdeployment.NewClusterDeployment()},
		},
		{
			title:    "should fail if a MultiClusterService is referencing serviceTemplate in system namespace",
			template: template.NewServiceTemplate(template.WithNamespace(testSystemNamespace), template.WithName(templateName)),
			existingObjects: []runtime.Object{
				multiclusterservice.NewMultiClusterService(
					multiclusterservice.WithName("mymulticlusterservice"),
					multiclusterservice.WithServiceTemplate(templateName),
				),
			},
			warnings: admission.Warnings{"The mytemplate ServiceTemplate object can't be removed if MultiClusterService objects [mymulticlusterservice] referencing it still exist"},
			err:      errTemplateDeletionForbidden.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.
				NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&v1alpha1.ClusterDeployment{}, v1alpha1.ClusterDeploymentServiceTemplatesIndexKey, v1alpha1.ExtractServiceTemplateNamesFromClusterDeployment).
				WithIndex(&v1alpha1.MultiClusterService{}, v1alpha1.MultiClusterServiceTemplatesIndexKey, v1alpha1.ExtractServiceTemplateNamesFromMultiClusterService).
				Build()

			validator := &ServiceTemplateValidator{
				TemplateValidator{
					Client:            c,
					SystemNamespace:   testSystemNamespace,
					templateKind:      v1alpha1.ServiceTemplateKind,
					templateChainKind: v1alpha1.ServiceTemplateChainKind,
				},
			}

			warn, err := validator.ValidateDelete(ctx, tt.template)
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
