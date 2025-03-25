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
	"github.com/K0rdent/kcm/test/objects/clusterdeployment"
	"github.com/K0rdent/kcm/test/objects/management"
	"github.com/K0rdent/kcm/test/objects/release"
	"github.com/K0rdent/kcm/test/objects/template"
	"github.com/K0rdent/kcm/test/scheme"
)

func TestManagementValidateCreate(t *testing.T) {
	g := NewWithT(t)

	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create}})

	tests := []struct {
		name            string
		management      *v1alpha1.Management
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name: "release is not ready, should fail",
			management: management.NewManagement(
				management.WithRelease(release.DefaultName),
			),
			existingObjects: []runtime.Object{
				release.New(
					release.WithName(release.DefaultName),
					release.WithReadyStatus(false),
				),
			},
			err: fmt.Sprintf(`Management "%s" is invalid: spec.release: Forbidden: release "%s" status is not ready`, management.DefaultName, release.DefaultName),
		},
		{
			name: "should succeed",
			management: management.NewManagement(
				management.WithRelease(release.DefaultName),
			),
			existingObjects: []runtime.Object{
				release.New(
					release.WithName(release.DefaultName),
				),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			validator := &ManagementValidator{Client: c}

			warn, err := validator.ValidateCreate(ctx, tt.management)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err).To(MatchError(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}

			g.Expect(warn).To(Equal(tt.warnings))
		})
	}
}

func TestManagementValidateUpdate(t *testing.T) {
	g := NewWithT(t)

	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update}})

	const (
		someContractVersion = "v1alpha4_v1beta1"
		capiVersion         = "v1beta1"
		capiVersionOther    = "v1alpha3"

		infraAWSProvider   = "infrastructure-aws"
		infraOtherProvider = "infrastructure-other-provider"

		bootstrapK0smotronProvider = "bootstrap-k0sproject-k0smotron"
		k0smotronTemplateName      = "k0smotron-0-0-7"

		awsProviderTemplateName = "cluster-api-provider-aws-0-0-4"
		awsClusterTemplateName  = "aws-standalone-cp-0-0-5"
	)

	validStatus := v1alpha1.TemplateValidationStatus{Valid: true}

	componentAwsDefaultTpl := v1alpha1.Provider{
		Name: "cluster-api-provider-aws",
		Component: v1alpha1.Component{
			Template: awsProviderTemplateName,
		},
	}

	componentK0smotronDefaultTpl := v1alpha1.Provider{
		Name: "k0smotron",
		Component: v1alpha1.Component{
			Template: k0smotronTemplateName,
		},
	}

	tests := []struct {
		name            string
		oldMgmt         *v1alpha1.Management
		management      *v1alpha1.Management
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name:    "no providers removed, no cluster deployments, should succeed",
			oldMgmt: management.NewManagement(management.WithProviders(componentAwsDefaultTpl)),
			management: management.NewManagement(
				management.WithProviders(componentAwsDefaultTpl),
				management.WithRelease(release.DefaultName),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName), template.WithProvidersStatus(infraAWSProvider)),
			},
		},
		{
			name: "release does not exist, should fail",
			oldMgmt: management.NewManagement(
				management.WithProviders(componentAwsDefaultTpl),
				management.WithRelease("previous-release"),
			),
			management: management.NewManagement(
				management.WithProviders(),
				management.WithRelease("new-release"),
			),
			err: fmt.Sprintf(`Management "%s" is invalid: spec.release: Forbidden: releases.k0rdent.mirantis.com "new-release" not found`, management.DefaultName),
		},
		{
			name: "removed provider does not have related providertemplate, should pass",
			oldMgmt: management.NewManagement(
				management.WithProviders(componentAwsDefaultTpl),
			),
			management: management.NewManagement(
				management.WithProviders(),
				management.WithRelease(release.DefaultName),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
			},
		},
		{
			name: "no cluster templates, should succeed",
			oldMgmt: management.NewManagement(
				management.WithProviders(componentAwsDefaultTpl),
			),
			management: management.NewManagement(
				management.WithProviders(),
				management.WithRelease(release.DefaultName),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName), template.WithProvidersStatus(infraAWSProvider)),
			},
		},
		{
			name: "cluster template from removed provider exists but no managed clusters, should succeed",
			oldMgmt: management.NewManagement(
				management.WithProviders(componentAwsDefaultTpl),
			),
			management: management.NewManagement(
				management.WithProviders(),
				management.WithRelease(release.DefaultName),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName), template.WithProvidersStatus(infraAWSProvider)),
				template.NewClusterTemplate(template.WithProvidersStatus(infraAWSProvider)),
			},
		},
		{
			name: "managed cluster uses the removed provider, should fail",
			oldMgmt: management.NewManagement(
				management.WithProviders(componentAwsDefaultTpl),
			),
			management: management.NewManagement(
				management.WithProviders(),
				management.WithRelease(release.DefaultName),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName), template.WithProvidersStatus(infraAWSProvider)),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
				template.NewClusterTemplate(template.WithProvidersStatus(infraAWSProvider)),
				clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(template.DefaultName)),
			},
			warnings: admission.Warnings{"Some of the providers cannot be removed"},
			err:      fmt.Sprintf(`Management "%s" is invalid: spec.providers: Forbidden: provider %s is required by at least one ClusterDeployment and cannot be removed from the Management %s`, management.DefaultName, infraAWSProvider, management.DefaultName),
		},
		{
			name: "managed cluster does not use the removed provider, should succeed",
			oldMgmt: management.NewManagement(
				management.WithProviders(componentAwsDefaultTpl),
			),
			management: management.NewManagement(
				management.WithProviders(),
				management.WithRelease(release.DefaultName),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName), template.WithProvidersStatus(infraAWSProvider)),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
				template.NewClusterTemplate(template.WithProvidersStatus(infraOtherProvider)),
				clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(template.DefaultName)),
			},
		},
		{
			name:            "no capi providertemplate, should fail",
			oldMgmt:         management.NewManagement(),
			management:      management.NewManagement(management.WithRelease(release.DefaultName)),
			existingObjects: []runtime.Object{release.New()},
			err:             fmt.Sprintf(`the Management is invalid: failed to get ProviderTemplate %s: providertemplates.k0rdent.mirantis.com "%s" not found`, release.DefaultCAPITemplateName, release.DefaultCAPITemplateName),
		},
		{
			name:       "capi providertemplate without capi version set, should succeed",
			oldMgmt:    management.NewManagement(),
			management: management.NewManagement(management.WithRelease(release.DefaultName)),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
			},
		},
		{
			name:       "capi providertemplate is not valid, should fail",
			oldMgmt:    management.NewManagement(),
			management: management.NewManagement(management.WithRelease(release.DefaultName)),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(
					template.WithName(release.DefaultCAPITemplateName),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
				),
			},
			err: "the Management is invalid: not valid ProviderTemplate " + release.DefaultCAPITemplateName,
		},
		{
			name:    "no providertemplates that declared in mgmt spec.providers, should fail",
			oldMgmt: management.NewManagement(),
			management: management.NewManagement(
				management.WithRelease(release.DefaultName),
				management.WithProviders(componentAwsDefaultTpl),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(
					template.WithName(release.DefaultCAPITemplateName),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
					template.WithValidationStatus(validStatus),
				),
			},
			err: fmt.Sprintf(`the Management is invalid: failed to get ProviderTemplate %s: providertemplates.k0rdent.mirantis.com "%s" not found`, awsProviderTemplateName, awsProviderTemplateName),
		},
		{
			name:    "providertemplates without specified capi contracts, should succeed",
			oldMgmt: management.NewManagement(),
			management: management.NewManagement(
				management.WithRelease(release.DefaultName),
				management.WithProviders(componentAwsDefaultTpl),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(
					template.WithName(release.DefaultCAPITemplateName),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
					template.WithValidationStatus(validStatus),
				),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName)),
			},
		},
		{
			name:    "providertemplates is not ready, should fail",
			oldMgmt: management.NewManagement(),
			management: management.NewManagement(
				management.WithRelease(release.DefaultName),
				management.WithProviders(componentAwsDefaultTpl),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(
					template.WithName(release.DefaultCAPITemplateName),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
					template.WithValidationStatus(validStatus),
				),
				template.NewProviderTemplate(
					template.WithName(awsProviderTemplateName),
					template.WithProviderStatusCAPIContracts(capiVersionOther, someContractVersion),
				),
			},
			err: "the Management is invalid: not valid ProviderTemplate " + awsProviderTemplateName,
		},
		{
			name:    "providertemplates do not match capi contracts, should fail",
			oldMgmt: management.NewManagement(),
			management: management.NewManagement(
				management.WithRelease(release.DefaultName),
				management.WithProviders(componentAwsDefaultTpl),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(
					template.WithName(release.DefaultCAPITemplateName),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
					template.WithValidationStatus(validStatus),
				),
				template.NewProviderTemplate(
					template.WithName(awsProviderTemplateName),
					template.WithProviderStatusCAPIContracts(capiVersionOther, someContractVersion),
					template.WithValidationStatus(validStatus),
				),
			},
			warnings: admission.Warnings{"The Management object has incompatible CAPI contract versions in ProviderTemplates"},
			err:      fmt.Sprintf("the Management is invalid: core CAPI contract versions does not support %s version in the ProviderTemplate %s", capiVersionOther, awsProviderTemplateName),
		},
		{
			name:    "providertemplates match capi contracts, should succeed",
			oldMgmt: management.NewManagement(),
			management: management.NewManagement(
				management.WithRelease(release.DefaultName),
				management.WithProviders(componentAwsDefaultTpl),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(
					template.WithName(release.DefaultCAPITemplateName),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
					template.WithValidationStatus(validStatus),
				),
				template.NewProviderTemplate(
					template.WithName(awsProviderTemplateName),
					template.WithProviderStatusCAPIContracts(capiVersion, someContractVersion),
					template.WithValidationStatus(validStatus),
				),
			},
		},
		{
			name:    "missing provider versions that are required by the cluster deployment, should fail",
			oldMgmt: management.NewManagement(),
			management: management.NewManagement(
				management.WithRelease(release.DefaultName),
				management.WithProviders(componentAwsDefaultTpl, componentK0smotronDefaultTpl),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(
					template.WithName(release.DefaultCAPITemplateName),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
					template.WithValidationStatus(validStatus),
				),
				template.NewProviderTemplate(
					template.WithName(componentAwsDefaultTpl.Template),
					template.WithProvidersStatus(infraAWSProvider),
					template.WithProviderStatusCAPIContracts(capiVersion, "v1alpha4_v1beta1"),
					template.WithValidationStatus(validStatus),
				),
				template.NewProviderTemplate(
					template.WithName(componentK0smotronDefaultTpl.Template),
					template.WithProvidersStatus(bootstrapK0smotronProvider),
					template.WithProviderStatusCAPIContracts(capiVersion, "v1beta1"),
					template.WithValidationStatus(validStatus),
				),
				template.NewClusterTemplate(
					template.WithName(awsClusterTemplateName),
					template.WithProvidersStatus(infraAWSProvider, bootstrapK0smotronProvider),
					template.WithClusterStatusProviderContracts(map[string]string{
						infraAWSProvider:           "v1beta4",
						bootstrapK0smotronProvider: "v1beta2",
						infraOtherProvider:         "v1beta3",
					}),
				),
				clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(awsClusterTemplateName)),
			},
			warnings: []string{"The Management object has incompatible CAPI contract versions in ProviderTemplates"},
			err: fmt.Sprintf("the Management is invalid: "+
				"missing contract version v1beta4 for %s provider that is required by one or more ClusterDeployment, "+
				"missing contract version v1beta2 for %s provider that is required by one or more ClusterDeployment", infraAWSProvider, bootstrapK0smotronProvider),
		},
		{
			name:    "the cluster deployment uses the provider but its contract version is exposed, should succeed",
			oldMgmt: management.NewManagement(),
			management: management.NewManagement(
				management.WithRelease(release.DefaultName),
				management.WithProviders(componentAwsDefaultTpl),
			),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(
					template.WithName(release.DefaultCAPITemplateName),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
					template.WithValidationStatus(validStatus),
				),
				template.NewProviderTemplate(
					template.WithName(componentAwsDefaultTpl.Template),
					template.WithProvidersStatus(infraAWSProvider),
					template.WithProviderStatusCAPIContracts(capiVersion, "v1alpha4_v1beta1"),
					template.WithValidationStatus(validStatus),
				),
				template.NewClusterTemplate(
					template.WithName(awsClusterTemplateName),
					template.WithProvidersStatus(infraAWSProvider, bootstrapK0smotronProvider),
					template.WithClusterStatusProviderContracts(map[string]string{
						infraAWSProvider: "v1alpha4",
					}),
				),
				clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(awsClusterTemplateName)),
			},
		},
		{
			name: "release is not ready, should fail",
			oldMgmt: management.NewManagement(
				management.WithRelease("old-release"),
			),
			management: management.NewManagement(
				management.WithRelease(release.DefaultName),
			),
			existingObjects: []runtime.Object{
				release.New(
					release.WithReadyStatus(false),
				),
			},
			err: fmt.Sprintf(`Management "%s" is invalid: spec.release: Forbidden: release "%s" status is not ready`, management.DefaultName, release.DefaultName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&v1alpha1.ClusterTemplate{}, v1alpha1.ClusterTemplateProvidersIndexKey, v1alpha1.ExtractProvidersFromClusterTemplate).
				WithIndex(&v1alpha1.ClusterDeployment{}, v1alpha1.ClusterDeploymentTemplateIndexKey, v1alpha1.ExtractTemplateNameFromClusterDeployment).
				Build()
			validator := &ManagementValidator{Client: c}

			warnings, err := validator.ValidateUpdate(ctx, tt.oldMgmt, tt.management)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err).To(MatchError(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}

			g.Expect(warnings).To(Equal(tt.warnings))
		})
	}
}

func TestManagementValidateDelete(t *testing.T) {
	g := NewWithT(t)

	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Delete}})

	tests := []struct {
		name            string
		management      *v1alpha1.Management
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name:            "should fail if ClusterDeployment objects exist",
			management:      management.NewManagement(),
			existingObjects: []runtime.Object{clusterdeployment.NewClusterDeployment()},
			warnings:        admission.Warnings{"The Management object can't be removed if ClusterDeployment objects still exist"},
			err:             "management deletion is forbidden",
		},
		{
			name:       "should succeed",
			management: management.NewManagement(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			validator := &ManagementValidator{Client: c}

			warn, err := validator.ValidateDelete(ctx, tt.management)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err).To(MatchError(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}

			g.Expect(warn).To(Equal(tt.warnings))
		})
	}
}
