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

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
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
		template        *kcmv1.ProviderTemplate
		existingObjects []runtime.Object
		warnings        admission.Warnings
		err             string
	}{
		{
			title:    "should fail if the core ProviderTemplate defined in the Management spec is removed",
			template: tmpl,
			existingObjects: []runtime.Object{
				management.NewManagement(management.WithRelease(releaseName), management.WithCoreComponents(&kcmv1.Core{
					KCM: kcmv1.Component{
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
						APIVersion: kcmv1.GroupVersion.String(),
						Kind:       kcmv1.ReleaseKind,
						Name:       "kcm-0-0-3",
					},
					{
						APIVersion: kcmv1.GroupVersion.String(),
						Kind:       kcmv1.ReleaseKind,
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
					management.WithCoreComponents(&kcmv1.Core{}),
					management.WithProviders(kcmv1.Provider{
						Name:     "cluster-api-provider-aws",
						Template: "cluster-api-provider-aws-0-0-2",
					},
					),
				),
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
				WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentServiceTemplatesIndexKey, kcmv1.ExtractServiceTemplateNamesFromClusterDeployment).
				Build()

			validator := &ProviderTemplateValidator{
				TemplateValidator{
					Client:          c,
					SystemNamespace: testSystemNamespace,
					templateKind:    kcmv1.ProviderTemplateKind,
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

	// systemTpl owns the HelmChart testSystemNamespace/mytemplate, which distributed copies reference
	systemTpl := template.NewClusterTemplate(
		template.WithName(templateName),
		template.WithNamespace(testSystemNamespace),
		template.WithHelmSpec(kcmv1.HelmSpec{ChartSpec: &sourcev1.HelmChartSpec{Chart: templateName, Version: "1.0.0"}}),
	)
	newCopy := func(namespace, name, chartName string) *kcmv1.ClusterTemplate {
		return template.NewClusterTemplate(
			template.WithName(name),
			template.WithNamespace(namespace),
			template.WithHelmSpec(kcmv1.HelmSpec{ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind:      sourcev1.HelmChartKind,
				Name:      chartName,
				Namespace: testSystemNamespace,
			}}),
		)
	}
	newClusterDeployment := func(namespace, templateName string) *kcmv1.ClusterDeployment {
		return clusterdeployment.NewClusterDeployment(
			clusterdeployment.WithNamespace(namespace),
			clusterdeployment.WithClusterTemplate(templateName),
		)
	}

	tests := []struct {
		title           string
		template        *kcmv1.ClusterTemplate
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
						APIVersion: kcmv1.GroupVersion.String(),
						Kind:       kcmv1.ClusterTemplateChainKind,
						Name:       "test-chain",
					},
				}),
			),
			existingObjects: []runtime.Object{
				tc.NewClusterTemplateChain(
					tc.WithName("test-chain"),
					tc.WithNamespace(templateNamespace),
					tc.WithSupportedTemplates(
						[]kcmv1.SupportedTemplate{
							{
								Name: templateName,
							},
						},
					),
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
						APIVersion: kcmv1.GroupVersion.String(),
						Kind:       kcmv1.ClusterTemplateChainKind,
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
		{
			title:    "should fail if a distributed copy references the system template's HelmChart",
			template: systemTpl,
			existingObjects: []runtime.Object{
				systemTpl,
				newCopy("team-a", templateName, templateName),
			},
			warnings: admission.Warnings{"The ClusterTemplate object can't be removed while its distributed copies reference its HelmChart"},
			err:      "template deletion is forbidden",
		},
		{
			title:    "should fail if several distributed copies reference the system template's HelmChart",
			template: systemTpl,
			existingObjects: []runtime.Object{
				systemTpl,
				newCopy("team-a", templateName, templateName),
				newCopy("team-b", templateName, templateName),
			},
			warnings: admission.Warnings{"The ClusterTemplate object can't be removed while its distributed copies reference its HelmChart"},
			err:      "template deletion is forbidden",
		},
		{
			title:    "should succeed if ClusterTemplates in other namespaces reference another HelmChart",
			template: systemTpl,
			existingObjects: []runtime.Object{
				systemTpl,
				newCopy("team-a", templateName, "another-chart"),
				newClusterDeployment("team-a", templateName),
			},
		},
		{
			title: "should succeed if the system template references a HelmChart it does not own",
			template: template.NewClusterTemplate(
				template.WithName(templateName),
				template.WithNamespace(testSystemNamespace),
				template.WithHelmSpec(kcmv1.HelmSpec{ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
					Kind:      sourcev1.HelmChartKind,
					Name:      templateName,
					Namespace: testSystemNamespace,
				}}),
			),
			existingObjects: []runtime.Object{
				newCopy("team-a", templateName, templateName),
				newClusterDeployment("team-a", templateName),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment).
				WithIndex(&kcmv1.ClusterTemplate{}, kcmv1.ClusterTemplateChartRefIndexKey, kcmv1.ExtractChartRefFromClusterTemplate).
				Build()
			validator := &ClusterTemplateValidator{
				Client:            c,
				SystemNamespace:   testSystemNamespace,
				templateKind:      kcmv1.ClusterTemplateKind,
				templateChainKind: kcmv1.ClusterTemplateChainKind,
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
		template        *kcmv1.ServiceTemplate
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
						APIVersion: kcmv1.GroupVersion.String(),
						Kind:       kcmv1.ServiceTemplateChainKind,
						Name:       "test-chain",
					},
				}),
			),
			existingObjects: []runtime.Object{
				tc.NewServiceTemplateChain(
					tc.WithName("test-chain"),
					tc.WithNamespace(templateNamespace),
					tc.WithSupportedTemplates(
						[]kcmv1.SupportedTemplate{
							{
								Name: templateName,
							},
						},
					),
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
						APIVersion: kcmv1.GroupVersion.String(),
						Kind:       kcmv1.ServiceTemplateChainKind,
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
		{
			title:    "should fail if a NamespacedMultiClusterService in the same namespace is referencing the serviceTemplate",
			template: tmpl,
			existingObjects: []runtime.Object{
				multiclusterservice.NewNamespacedMultiClusterService(
					multiclusterservice.WithNamespacedName("mynamespacedmulticlusterservice"),
					multiclusterservice.WithNamespacedNamespace(templateNamespace),
					multiclusterservice.WithNamespacedServiceTemplate(templateName),
				),
			},
			warnings: admission.Warnings{"The mytemplate ServiceTemplate object can't be removed if NamespacedMultiClusterService objects [mynamespacedmulticlusterservice] referencing it still exist"},
			err:      errTemplateDeletionForbidden.Error(),
		},
		{
			// Unlike a MultiClusterService, a NamespacedMultiClusterService resolves its
			// ServiceTemplates against its own namespace, so one in the system namespace
			// blocks a template there just like in any other namespace.
			title:    "should fail if a NamespacedMultiClusterService is referencing the serviceTemplate in the system namespace",
			template: template.NewServiceTemplate(template.WithNamespace(testSystemNamespace), template.WithName(templateName)),
			existingObjects: []runtime.Object{
				multiclusterservice.NewNamespacedMultiClusterService(
					multiclusterservice.WithNamespacedName("mynamespacedmulticlusterservice"),
					multiclusterservice.WithNamespacedNamespace(testSystemNamespace),
					multiclusterservice.WithNamespacedServiceTemplate(templateName),
				),
			},
			warnings: admission.Warnings{"The mytemplate ServiceTemplate object can't be removed if NamespacedMultiClusterService objects [mynamespacedmulticlusterservice] referencing it still exist"},
			err:      errTemplateDeletionForbidden.Error(),
		},
		{
			title:    "should succeed if the NamespacedMultiClusterService referencing the serviceTemplate is in another namespace",
			template: tmpl,
			existingObjects: []runtime.Object{
				multiclusterservice.NewNamespacedMultiClusterService(
					multiclusterservice.WithNamespacedName("mynamespacedmulticlusterservice"),
					multiclusterservice.WithNamespacedNamespace("someothernamespace"),
					multiclusterservice.WithNamespacedServiceTemplate(templateName),
				),
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
				WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentServiceTemplatesIndexKey, kcmv1.ExtractServiceTemplateNamesFromClusterDeployment).
				WithIndex(&kcmv1.MultiClusterService{}, kcmv1.MultiClusterServiceTemplatesIndexKey, kcmv1.ExtractServiceTemplateNamesFromMultiClusterService).
				WithIndex(&kcmv1.NamespacedMultiClusterService{}, kcmv1.NamespacedMultiClusterServiceTemplatesIndexKey, kcmv1.ExtractServiceTemplateNamesFromNamespacedMultiClusterService).
				Build()

			validator := &ServiceTemplateValidator{
				TemplateValidator{
					Client:            c,
					SystemNamespace:   testSystemNamespace,
					templateKind:      kcmv1.ServiceTemplateKind,
					templateChainKind: kcmv1.ServiceTemplateChainKind,
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
