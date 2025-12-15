// Copyright 2025
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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
	"github.com/K0rdent/kcm/test/objects/clusterdeployment"
	"github.com/K0rdent/kcm/test/objects/credential"
	"github.com/K0rdent/kcm/test/objects/management"
	"github.com/K0rdent/kcm/test/objects/region"
	"github.com/K0rdent/kcm/test/objects/release"
	"github.com/K0rdent/kcm/test/objects/template"
	"github.com/K0rdent/kcm/test/scheme"
)

func TestRegionValidateCreate(t *testing.T) {
	g := NewWithT(t)

	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create}})

	const (
		kubeconfigSecretName  = "kubeconfig-secret"
		kubeconfigSecretKey   = "value"
		clusterDeploymentName = "test1"

		systemNamespace = "kcm-system"
	)

	kubeConfigSecretData := map[string][]byte{
		kubeconfigSecretKey: []byte("Zm9vYmFyCg=="),
	}

	tests := []struct {
		name            string
		rgn             *kcmv1.Region
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name:            "kubeconfig secret does not exist in the system namespace, should fail",
			rgn:             region.New(region.WithKubeConfigSecretReference(kubeconfigSecretName, kubeconfigSecretKey)),
			existingObjects: []runtime.Object{},
			err:             fmt.Sprintf("failed to get Secret %s/%s: secrets %q not found", systemNamespace, kubeconfigSecretName, kubeconfigSecretName),
		},
		{
			name: "kubeconfig secret with the same name exists in the non system namespace, should fail",
			rgn:  region.New(region.WithKubeConfigSecretReference(kubeconfigSecretName, kubeconfigSecretKey)),
			existingObjects: []runtime.Object{&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      kubeconfigSecretName,
				},
				Data: kubeConfigSecretData,
			}},
			err: fmt.Sprintf("failed to get Secret %s/%s: secrets %q not found", systemNamespace, kubeconfigSecretName, kubeconfigSecretName),
		},
		{
			name: "kubeconfig secret exists but the data is invalid, should fail",
			rgn:  region.New(region.WithKubeConfigSecretReference(kubeconfigSecretName, kubeconfigSecretKey)),
			existingObjects: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: systemNamespace,
						Name:      kubeconfigSecretName,
					},
					Data: map[string][]byte{
						"wrongKey": []byte("Zm9vYmFyCg=="),
					},
				},
			},
			err: fmt.Sprintf("kubeConfig Secret does not have %s key defined", kubeconfigSecretKey),
		},
		{
			name: "kubeconfig secret exists and the key is not empty, should succeed",
			rgn:  region.New(region.WithKubeConfigSecretReference(kubeconfigSecretName, kubeconfigSecretKey)),
			existingObjects: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: systemNamespace,
						Name:      kubeconfigSecretName,
					},
					Data: kubeConfigSecretData,
				},
			},
		},
		{
			name: "referenced cluster deployment does not exist in the defined namespace, should fail",
			rgn:  region.New(region.WithClusterDeploymentReference(systemNamespace, clusterDeploymentName)),
			existingObjects: []runtime.Object{
				&kcmv1.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "test",
						Name:      clusterDeploymentName,
					},
				},
			},
			err: fmt.Sprintf("failed to get ClusterDeployment %s/%s: clusterdeployments.k0rdent.mirantis.com %q not found", systemNamespace, clusterDeploymentName, clusterDeploymentName),
		},
		{
			name: "referenced cluster deployment exists, should succeed",
			rgn:  region.New(region.WithClusterDeploymentReference(systemNamespace, clusterDeploymentName)),
			existingObjects: []runtime.Object{
				&kcmv1.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: systemNamespace,
						Name:      clusterDeploymentName,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			validator := &RegionValidator{Client: c, SystemNamespace: systemNamespace}

			warn, err := validator.ValidateCreate(ctx, tt.rgn)
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

func TestRegionValidateUpdate(t *testing.T) {
	g := NewWithT(t)

	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update}})

	const (
		systemNamespace = "kcm-system"

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

	validStatus := kcmv1.TemplateValidationStatus{Valid: true}

	componentAwsDefaultTpl := kcmv1.Provider{
		Name: "cluster-api-provider-aws",
		Component: kcmv1.Component{
			Template: awsProviderTemplateName,
		},
	}

	componentK0smotronDefaultTpl := kcmv1.Provider{
		Name: "k0smotron",
		Component: kcmv1.Component{
			Template: k0smotronTemplateName,
		},
	}

	tests := []struct {
		name            string
		oldRgn          *kcmv1.Region
		rgn             *kcmv1.Region
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name:   "management does not exist, should fail",
			oldRgn: region.New(),
			rgn:    region.New(region.WithProviders(componentAwsDefaultTpl)),
			existingObjects: []runtime.Object{
				release.New(),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName), template.WithProvidersStatus(infraAWSProvider)),
			},
			err: fmt.Sprintf("failed to get Management: managements.k0rdent.mirantis.com %q not found", kcmv1.ManagementName),
		},
		{
			name:   "no providers removed, no cluster deployments in this Region, should succeed",
			oldRgn: region.New(),
			rgn:    region.New(region.WithProviders(componentAwsDefaultTpl)),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName), template.WithProvidersStatus(infraAWSProvider)),
				clusterdeployment.NewClusterDeployment(),
			},
		},
		{
			name:   "no cluster templates for the removed provider, should succeed",
			oldRgn: region.New(region.WithProviders(componentAwsDefaultTpl)),
			rgn:    region.New(region.WithProviders()),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName), template.WithProvidersStatus(infraAWSProvider)),
			},
		},
		{
			name:   "cluster template from removed provider exists but no managed clusters in this Region, should succeed",
			oldRgn: region.New(region.WithProviders(componentAwsDefaultTpl)),
			rgn:    region.New(region.WithProviders()),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName), template.WithProvidersStatus(infraAWSProvider)),
				template.NewClusterTemplate(template.WithProvidersStatus(infraAWSProvider)),
				clusterdeployment.NewClusterDeployment(),
			},
		},
		{
			name:   "managed cluster in this Region uses the removed provider, should fail",
			oldRgn: region.New(region.WithProviders(componentAwsDefaultTpl)),
			rgn:    region.New(region.WithProviders()),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName), template.WithProvidersStatus(infraAWSProvider)),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
				template.NewClusterTemplate(template.WithProvidersStatus(infraAWSProvider)),
				credential.NewCredential(credential.WithName("test"), credential.WithRegion(region.DefaultName)),
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithClusterTemplate(template.DefaultName),
					clusterdeployment.WithCredential("test"),
					clusterdeployment.WithRegion(region.DefaultName),
				),
			},
			warnings: admission.Warnings{"Some of the providers cannot be removed"},
			err:      fmt.Sprintf(`Region "%s" is invalid: spec.providers: Forbidden: provider %s is required by at least one ClusterDeployment and cannot be removed from the Region %s`, region.DefaultName, infraAWSProvider, region.DefaultName),
		},
		{
			name:   "managed cluster in this Region does not use the removed provider, should succeed",
			oldRgn: region.New(region.WithProviders(componentAwsDefaultTpl)),
			rgn:    region.New(region.WithProviders()),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName), template.WithProvidersStatus(infraAWSProvider)),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
				template.NewClusterTemplate(template.WithProvidersStatus(infraOtherProvider)),
				credential.NewCredential(credential.WithName("test"), credential.WithRegion(region.DefaultName)),
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithClusterTemplate(template.DefaultName),
					clusterdeployment.WithCredential("test"),
					clusterdeployment.WithRegion(region.DefaultName),
				),
			},
		},
		{
			name:   "managed cluster from another region uses the removed provider, should succeed",
			oldRgn: region.New(region.WithProviders(componentAwsDefaultTpl)),
			rgn:    region.New(region.WithProviders()),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName), template.WithProvidersStatus(infraAWSProvider)),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
				template.NewClusterTemplate(template.WithProvidersStatus(infraAWSProvider)),
				credential.NewCredential(credential.WithName("test"), credential.WithRegion("region2")),
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithClusterTemplate(template.DefaultName),
					clusterdeployment.WithCredential("test"),
					clusterdeployment.WithRegion("region2"),
				),
			},
		},
		{
			name:   "no capi providertemplate, should fail",
			oldRgn: region.New(),
			rgn:    region.New(),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
			},
			err: fmt.Sprintf(`the Region %s is invalid: failed to get ProviderTemplate %s: providertemplates.k0rdent.mirantis.com "%s" not found`, region.DefaultName, release.DefaultCAPITemplateName, release.DefaultCAPITemplateName),
		},
		{
			name:   "capi providertemplate without capi version set, should succeed",
			oldRgn: region.New(),
			rgn:    region.New(),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
				template.NewProviderTemplate(template.WithName(release.DefaultCAPITemplateName)),
			},
		},
		{
			name:   "capi providertemplate is not valid, should fail",
			oldRgn: region.New(),
			rgn: region.New(
				region.WithCoreComponents(&kcmv1.Core{
					CAPI: kcmv1.Component{
						Template: "new-capi-template",
					},
				}),
			),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
				template.NewProviderTemplate(
					template.WithName("new-capi-template"),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
				),
			},
			err: fmt.Sprintf("the Region %s is invalid: not valid ProviderTemplate new-capi-template: %s", region.DefaultName, validationutil.ErrProviderIsNotReady),
		},
		{
			name:   "default capi providertemplate in invalid but it has not changed, should succeed",
			oldRgn: region.New(),
			rgn: region.New(
				region.WithProviders(componentAwsDefaultTpl),
			),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
				template.NewProviderTemplate(
					template.WithName(release.DefaultCAPITemplateName),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
				),
				template.NewProviderTemplate(
					template.WithName(awsProviderTemplateName),
					template.WithValidationStatus(validStatus),
				),
			},
		},
		{
			name: "custom capi providertemplate in invalid but it has not changed, should succeed",
			oldRgn: region.New(
				region.WithCoreComponents(&kcmv1.Core{
					CAPI: kcmv1.Component{
						Template: "custom-capi-template",
					},
				}),
			),
			rgn: region.New(
				region.WithCoreComponents(&kcmv1.Core{
					CAPI: kcmv1.Component{
						Template: "custom-capi-template",
					},
				}),
				region.WithProviders(componentAwsDefaultTpl),
			),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
				template.NewProviderTemplate(
					template.WithName("custom-capi-template"),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
				),
				template.NewProviderTemplate(
					template.WithName(awsProviderTemplateName),
					template.WithValidationStatus(validStatus),
				),
			},
		},
		{
			name:   "no providertemplates that declared in Region spec.providers, should fail",
			oldRgn: region.New(),
			rgn:    region.New(region.WithProviders(componentAwsDefaultTpl)),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
				template.NewProviderTemplate(
					template.WithName(release.DefaultCAPITemplateName),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
					template.WithValidationStatus(validStatus),
				),
			},
			err: fmt.Sprintf(`the Region %s is invalid: failed to get ProviderTemplate %s: providertemplates.k0rdent.mirantis.com "%s" not found`, region.DefaultName, awsProviderTemplateName, awsProviderTemplateName),
		},
		{
			name:   "providertemplates without specified capi contracts, should succeed",
			oldRgn: region.New(),
			rgn:    region.New(region.WithProviders(componentAwsDefaultTpl)),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
				template.NewProviderTemplate(
					template.WithName(release.DefaultCAPITemplateName),
					template.WithProviderStatusCAPIContracts(capiVersion, ""),
					template.WithValidationStatus(validStatus),
				),
				template.NewProviderTemplate(template.WithName(awsProviderTemplateName)),
			},
		},
		{
			name:   "providertemplates is not ready, should fail",
			oldRgn: region.New(),
			rgn:    region.New(region.WithProviders(componentAwsDefaultTpl)),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
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
			err: fmt.Sprintf("the Region %s is invalid: not valid ProviderTemplate %s: %s", region.DefaultName, awsProviderTemplateName, validationutil.ErrProviderIsNotReady),
		},
		{
			name:   "providertemplates do not match capi contracts, should fail",
			oldRgn: region.New(),
			rgn:    region.New(region.WithProviders(componentAwsDefaultTpl)),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
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
			warnings: admission.Warnings{"The Region object has incompatible CAPI contract versions in ProviderTemplates"},
			err:      fmt.Sprintf("the Region %s is invalid: core CAPI contract versions does not support %s version in the ProviderTemplate %s", region.DefaultName, capiVersionOther, awsProviderTemplateName),
		},
		{
			name:   "providertemplates match capi contracts, should succeed",
			oldRgn: region.New(),
			rgn:    region.New(region.WithProviders(componentAwsDefaultTpl)),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
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
			name:   "missing provider versions that are required by the cluster deployment in this Region, should fail",
			oldRgn: region.New(),
			rgn:    region.New(region.WithProviders(componentAwsDefaultTpl, componentK0smotronDefaultTpl)),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
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
				credential.NewCredential(credential.WithName("test")),
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithClusterTemplate(awsClusterTemplateName),
					clusterdeployment.WithCredential("test"),
					clusterdeployment.WithRegion(region.DefaultName),
				),
			},
			warnings: []string{"The Region object has incompatible CAPI contract versions in ProviderTemplates"},
			err: fmt.Sprintf("the Region %s is invalid: "+
				"missing contract version v1beta4 for %s provider that is required by one or more ClusterDeployment, "+
				"missing contract version v1beta2 for %s provider that is required by one or more ClusterDeployment", region.DefaultName, infraAWSProvider, bootstrapK0smotronProvider),
		},
		{
			name: "missing provider versions that are required by the cluster deployment in this Region, but the templates were not changed, should succeed",
			oldRgn: region.New(
				region.WithProviders(componentAwsDefaultTpl, componentK0smotronDefaultTpl),
			),
			rgn: region.New(
				region.WithLabels(map[string]string{"foo": "bar"}),
				region.WithProviders(componentAwsDefaultTpl, componentK0smotronDefaultTpl),
			),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
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
		},
		{
			name:   "missing provider versions that are required by the cluster deployment not in this Region, should succeed",
			oldRgn: region.New(),
			rgn:    region.New(region.WithProviders(componentAwsDefaultTpl, componentK0smotronDefaultTpl)),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
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
				credential.NewCredential(credential.WithName("test")),
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithClusterTemplate(awsClusterTemplateName),
					clusterdeployment.WithCredential("test"),
					clusterdeployment.WithRegion("region2"),
				),
			},
		},
		{
			name:   "the cluster deployment uses the provider but its contract version is exposed, should succeed",
			oldRgn: region.New(),
			rgn:    region.New(region.WithProviders(componentAwsDefaultTpl)),
			existingObjects: []runtime.Object{
				release.New(),
				management.NewManagement(),
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
				credential.NewCredential(credential.WithName("test")),
				clusterdeployment.NewClusterDeployment(
					clusterdeployment.WithClusterTemplate(awsClusterTemplateName),
					clusterdeployment.WithCredential("test"),
					clusterdeployment.WithRegion(region.DefaultName),
				),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&kcmv1.ClusterTemplate{}, kcmv1.ClusterTemplateProvidersIndexKey, kcmv1.ExtractProvidersFromClusterTemplate).
				WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentTemplateIndexKey, kcmv1.ExtractTemplateNameFromClusterDeployment).
				Build()
			validator := &RegionValidator{Client: c, SystemNamespace: systemNamespace}

			warnings, err := validator.ValidateUpdate(ctx, tt.oldRgn, tt.rgn)
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

func TestRegionValidateDelete(t *testing.T) {
	g := NewWithT(t)

	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Delete}})

	const (
		systemNamespace = "kcm-system"

		credentialName = "cred1"
		regionName     = "region1"
	)

	tests := []struct {
		name            string
		rgn             *kcmv1.Region
		existingObjects []runtime.Object
		err             string
		warnings        admission.Warnings
	}{
		{
			name: "should fail if ClusterDeployment objects exist in that Region",
			rgn:  region.New(region.WithName(regionName)),
			existingObjects: []runtime.Object{
				credential.NewCredential(credential.WithName(credentialName), credential.WithRegion(regionName)),
				clusterdeployment.NewClusterDeployment(clusterdeployment.WithCredential(credentialName)),
			},
			warnings: admission.Warnings{"The Region object can't be removed while any ClusterDeployment objects deployed in that Region still exist"},
			err:      "region deletion is forbidden",
		},
		{
			name: "should succeed is no ClusterDeployment objects exists in that Region",
			rgn:  region.New(region.WithName(regionName)),
			existingObjects: []runtime.Object{
				credential.NewCredential(credential.WithName(credentialName), credential.WithRegion("region2")),
				clusterdeployment.NewClusterDeployment(clusterdeployment.WithCredential(credentialName)),
			},
		},
		{
			name: "should succeed is no ClusterDeployment exists",
			rgn:  region.New(region.WithName(regionName)),
			existingObjects: []runtime.Object{
				credential.NewCredential(credential.WithName(credentialName), credential.WithRegion("region1")),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			c := fake.
				NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				WithIndex(&kcmv1.Credential{}, kcmv1.CredentialRegionIndexKey, kcmv1.ExtractCredentialRegion).
				WithIndex(&kcmv1.ClusterDeployment{}, kcmv1.ClusterDeploymentCredentialIndexKey, kcmv1.ExtractCredentialNameFromClusterDeployment).
				Build()

			validator := &RegionValidator{Client: c, SystemNamespace: systemNamespace}

			warn, err := validator.ValidateDelete(ctx, tt.rgn)
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
