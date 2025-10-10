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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/test/objects/clusterdeployment"
	"github.com/K0rdent/kcm/test/objects/credential"
	"github.com/K0rdent/kcm/test/objects/management"
	"github.com/K0rdent/kcm/test/objects/providerinterface"
	"github.com/K0rdent/kcm/test/objects/region"
	"github.com/K0rdent/kcm/test/objects/template"
	"github.com/K0rdent/kcm/test/scheme"
)

const (
	testSvcTemplate1Name = "test-servicetemplate-1"
	testSystemNamespace  = "test-system-namespace"
)

var (
	testTemplateName   = "template-test"
	testCredentialName = "cred-test"
	newTemplateName    = "new-template-name"

	testRegionName = "test-rgn"

	testNamespace = "test"

	mgmt = management.NewManagement(
		management.WithAvailableProviders(kcmv1.Providers{
			"infrastructure-aws",
			"control-plane-k0smotron",
			"bootstrap-k0smotron",
		}),
		management.WithComponentsStatus(map[string]kcmv1.ComponentStatus{
			"cluster-api-provider-aws": {
				ExposedProviders: []string{"infrastructure-aws"},
			},
		}),
	)

	rgn = region.New(
		region.WithName(testRegionName),
		region.WithAvailableProviders(kcmv1.Providers{
			"infrastructure-openstack",
			"control-plane-k0smotron",
			"bootstrap-k0smotron",
		}),
		region.WithComponentsStatus(map[string]kcmv1.ComponentStatus{
			"cluster-api-provider-openstack": {
				ExposedProviders: []string{"infrastructure-openstack", "control-plane-k0smotron", "bootstrap-k0smotron"},
			},
		}),
	)

	cred = credential.NewCredential(
		credential.WithName(testCredentialName),
		credential.WithReady(true),
		credential.WithIdentityRef(
			&corev1.ObjectReference{
				Kind: "AWSClusterStaticIdentity",
				Name: "awsclid",
			}),
	)

	providerInterface = providerinterface.NewAWSProviderInterface()
)

func TestClusterDeploymentValidateCreate(t *testing.T) {
	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
		},
	})

	const (
		otherNamespace    = "othernamespace"
		testSecretName    = "test-secret"
		testConfigMapName = "test-configmap"
	)

	tests := []struct {
		name              string
		ClusterDeployment *kcmv1.ClusterDeployment
		existingObjects   []runtime.Object
		err               string
		warnings          admission.Warnings
	}{
		{
			name:              "should fail if the template is unset",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(),
			err:               "the ClusterDeployment is invalid: clustertemplates.k0rdent.mirantis.com \"\" not found",
		},
		{
			name: "should fail if the ClusterTemplate is not found in the ClusterDeployment's namespace",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithNamespace(testNamespace),
				),
			},
			err: apierrors.NewNotFound(schema.GroupResource{Group: kcmv1.GroupVersion.Group, Resource: "clustertemplates"}, testTemplateName).Error(),
		},
		{
			name: "should fail if parent Region object is not found",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
			),
			existingObjects: []runtime.Object{
				mgmt,
				credential.NewCredential(
					credential.WithName(testCredentialName),
					credential.WithReady(true),
					credential.WithIdentityRef(
						&corev1.ObjectReference{
							Kind: "AWSClusterStaticIdentity",
							Name: "awsclid",
						}),
					credential.WithRegion(rgn.Name),
				),
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
			warnings: admission.Warnings{"Failed to validate required providers"},
			err:      apierrors.NewNotFound(schema.GroupResource{Group: kcmv1.GroupVersion.Group, Resource: "regions"}, testRegionName).Error(),
		},
		{
			name: "should fail if ClusterTemplate required providers are not exposed by the Region",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
			),
			existingObjects: []runtime.Object{
				mgmt,
				rgn,
				credential.NewCredential(
					credential.WithName(testCredentialName),
					credential.WithReady(true),
					credential.WithIdentityRef(
						&corev1.ObjectReference{
							Kind: "AWSClusterStaticIdentity",
							Name: "awsclid",
						}),
					credential.WithRegion(rgn.Name),
				),
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
			warnings: admission.Warnings{"Failed to validate required providers"},
			err:      fmt.Sprintf("failed to validate required providers: incompatible providers in Region %s: one or more required providers are not deployed yet: [infrastructure-aws]", testRegionName),
		},
		{
			name: "should succeed: all required providers are exposed by management",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
		},
		{
			name: "should fail if the ServiceTemplates are not found in same namespace",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithServiceTemplate(testSvcTemplate1Name),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithNamespace(otherNamespace),
				),
			},
			err: apierrors.NewNotFound(schema.GroupResource{Group: kcmv1.GroupVersion.Group, Resource: "servicetemplates"}, testSvcTemplate1Name).Error(),
		},
		{
			name: "should fail if the cluster template was found but is invalid (some validation error)",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
				),
			},
			err: fmt.Sprintf("the ClusterDeployment is invalid: the ClusterTemplate %s/%s is invalid with the error: validation error example", metav1.NamespaceDefault, testTemplateName),
		},
		{
			name: "should fail if the service templates were found but are invalid (some validation error)",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithServiceTemplate(testSvcTemplate1Name),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
				),
			},
			err: fmt.Sprintf("the ClusterDeployment is invalid: some services have invalid templates\nthe ServiceTemplate %s/%s is invalid with the error: validation error example", metav1.NamespaceDefault, testSvcTemplate1Name),
		},
		{
			name: "should succeed",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithServiceSpec(kcmv1.ServiceSpec{
					Services: []kcmv1.Service{
						{
							Template: testSvcTemplate1Name,
							ValuesFrom: []kcmv1.ValuesFrom{
								// Should not fail if namespace is empty
								{Kind: "ConfigMap", Name: testConfigMapName},
								{Kind: "Secret", Name: testSecretName},
							},
						},
					},
				}),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
		},
		{
			name: "cluster template k8s version does not satisfy service template constraints",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithServiceTemplate(testTemplateName),
			),
			existingObjects: []runtime.Object{
				cred,
				management.NewManagement(management.WithAvailableProviders(kcmv1.Providers{
					"infrastructure-aws",
					"control-plane-k0smotron",
					"bootstrap-k0smotron",
				})),
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
					template.WithClusterStatusK8sVersion("v1.30.0"),
				),
				template.NewServiceTemplate(
					template.WithName(testTemplateName),
					template.WithServiceK8sConstraint("<1.30"),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
			err:      fmt.Sprintf(`failed to validate k8s compatibility: k8s version v1.30.0 of the ClusterTemplate %s/%s does not satisfy k8s constraint <1.30 from the ServiceTemplate %s/%s referred in the ClusterDeployment %s/%s`, metav1.NamespaceDefault, testTemplateName, metav1.NamespaceDefault, testTemplateName, metav1.NamespaceDefault, clusterdeployment.DefaultName),
			warnings: admission.Warnings{"Failed to validate k8s version compatibility with ServiceTemplates"},
		},
		{
			name:              "should fail if the credential is unset",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(testTemplateName)),
			existingObjects: []runtime.Object{
				mgmt,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
			err:      fmt.Sprintf("failed to validate required providers: failed to get %s/%s Credential: credentials.k0rdent.mirantis.com \"\" not found", metav1.NamespaceDefault, ""),
			warnings: admission.Warnings{"Failed to validate required providers"},
		},
		{
			name: "should fail if credential is not Ready",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
			),
			existingObjects: []runtime.Object{
				mgmt,
				credential.NewCredential(
					credential.WithName(testCredentialName),
					credential.WithReady(false),
					credential.WithIdentityRef(
						&corev1.ObjectReference{
							Kind: "AWSClusterStaticIdentity",
							Name: "awsclid",
						}),
				),
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
			err: fmt.Sprintf("the ClusterDeployment is invalid: the Credential %s/%s is not Ready", metav1.NamespaceDefault, testCredentialName),
		},
		{
			name: "should fail if credential and template providers doesn't match",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
			),
			existingObjects: []runtime.Object{
				credential.NewCredential(
					credential.WithName(testCredentialName),
					credential.WithReady(true),
					credential.WithIdentityRef(
						&corev1.ObjectReference{
							Kind: "SomeOtherDummyClusterStaticIdentity",
							Name: "otherdummyclid",
						}),
				),
				management.NewManagement(
					management.WithAvailableProviders(kcmv1.Providers{
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					}),
					management.WithComponentsStatus(map[string]kcmv1.ComponentStatus{
						"cluster-api-provider-aws": {
							ExposedProviders: []string{"infrastructure-aws"},
						},
					}),
				),
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
				providerInterface,
			},
			err: fmt.Sprintf("the ClusterDeployment is invalid: provider %s does not support ClusterIdentity Kind %s from the Credential %s/%s", "infrastructure-aws", "SomeOtherDummyClusterStaticIdentity", metav1.NamespaceDefault, testCredentialName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				Build()
			validator := &ClusterDeploymentValidator{Client: c}
			warn, err := validator.ValidateCreate(ctx, tt.ClusterDeployment)
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

func TestClusterDeploymentValidateUpdate(t *testing.T) {
	const (
		upgradeTargetTemplateName  = "upgrade-target-template"
		unmanagedByKCMTemplateName = "unmanaged-template"

		otherNamespace = "othernamespace"
	)

	ctx := admission.NewContextWithRequest(t.Context(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
		},
	})

	tests := []struct {
		name                      string
		oldClusterDeployment      *kcmv1.ClusterDeployment
		newClusterDeployment      *kcmv1.ClusterDeployment
		existingObjects           []runtime.Object
		skipUpgradePathValidation bool
		err                       string
		warnings                  admission.Warnings
	}{
		{
			name: "update spec.template: should fail if the new cluster template was found but is invalid (some validation error)",
			oldClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithAvailableUpgrades([]string{newTemplateName}),
			),
			newClusterDeployment: clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(newTemplateName)),
			existingObjects: []runtime.Object{
				mgmt,
				template.NewClusterTemplate(
					template.WithName(newTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
				),
			},
			err: fmt.Sprintf("the ClusterDeployment is invalid: the ClusterTemplate %s/%s is invalid with the error: validation error example", metav1.NamespaceDefault, newTemplateName),
		},
		{
			name: "update spec.template: should fail if the template is not in the list of available",
			oldClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithAvailableUpgrades([]string{}),
			),
			newClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(upgradeTargetTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
			),
			existingObjects: []runtime.Object{
				mgmt, cred,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
				template.NewClusterTemplate(
					template.WithName(upgradeTargetTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
			},
			warnings: admission.Warnings{fmt.Sprintf("Cluster can't be upgraded from %s to %s. This upgrade sequence is not allowed", testTemplateName, upgradeTargetTemplateName)},
			err:      "cluster upgrade is forbidden",
		},
		{
			name: "update spec.template: should succeed if the template is in the list of available",
			oldClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithAvailableUpgrades([]string{newTemplateName}),
			),
			newClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(newTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
			),
			existingObjects: []runtime.Object{
				mgmt, cred, providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
				template.NewClusterTemplate(
					template.WithName(newTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
			},
		},
		{
			name:                      "update spec.template: should succeed if upgrade sequence validation is skipped",
			skipUpgradePathValidation: true,
			oldClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
			),
			newClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(newTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
			),
			existingObjects: []runtime.Object{
				mgmt, cred, providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
				template.NewClusterTemplate(
					template.WithName(newTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
			},
		},
		{
			name: "should succeed if spec.template is not changed",
			oldClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithConfig(`{"foo":"bar"}`),
				clusterdeployment.WithCredential(testCredentialName),
			),
			newClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithConfig(`{"a":"b"}`),
				clusterdeployment.WithCredential(testCredentialName),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
			},
		},
		{
			name: "should succeed if serviceTemplates are added",
			oldClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithConfig(`{"foo":"bar"}`),
				clusterdeployment.WithCredential(testCredentialName),
			),
			newClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithConfig(`{"a":"b"}`),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithServiceTemplate(testSvcTemplate1Name),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
		},
		{
			name: "should succeed if serviceTemplates are removed",
			oldClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithConfig(`{"foo":"bar"}`),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithServiceTemplate(testSvcTemplate1Name),
			),
			newClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithConfig(`{"a":"b"}`),
				clusterdeployment.WithCredential(testCredentialName),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
		},
		{
			name: "should fail if serviceTemplates are not in the same namespace",
			oldClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithConfig(`{"foo":"bar"}`),
				clusterdeployment.WithCredential(testCredentialName),
			),
			newClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithConfig(`{"a":"b"}`),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithServiceTemplate(testSvcTemplate1Name),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithNamespace(otherNamespace),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
			err: apierrors.NewNotFound(schema.GroupResource{Group: kcmv1.GroupVersion.Group, Resource: "servicetemplates"}, testSvcTemplate1Name).Error(),
		},
		{
			name: "should fail if the ServiceTemplates were found but are invalid",
			oldClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithConfig(`{"foo":"bar"}`),
				clusterdeployment.WithCredential(testCredentialName),
			),
			newClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithConfig(`{"a":"b"}`),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithServiceTemplate(testSvcTemplate1Name),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				providerInterface,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
				),
			},
			err: fmt.Sprintf("the ClusterDeployment is invalid: some services have invalid templates\nthe ServiceTemplate %s/%s is invalid with the error: validation error example", metav1.NamespaceDefault, testSvcTemplate1Name),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				Build()
			validator := &ClusterDeploymentValidator{Client: c, ValidateClusterUpgradePath: !tt.skipUpgradePathValidation}
			warn, err := validator.ValidateUpdate(ctx, tt.oldClusterDeployment, tt.newClusterDeployment)
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

func TestClusterDeploymentDelete(t *testing.T) {
	g := NewWithT(t)

	ctx := t.Context()

	const (
		cldName      = "test1"
		cldNamespace = "test"
	)

	tests := []struct {
		name            string
		cld             *kcmv1.ClusterDeployment
		existingObjects []runtime.Object
		err             string
	}{
		{
			name: "can't delete ClusterDeployment referenced by Region",
			cld:  clusterdeployment.NewClusterDeployment(clusterdeployment.WithName(cldName), clusterdeployment.WithNamespace(cldNamespace)),
			existingObjects: []runtime.Object{
				region.New(region.WithClusterDeploymentReference(cldNamespace, cldName)),
			},
			err: fmt.Sprintf("ClusterDeployment cannot be deleted: referenced by Region %q", region.DefaultName),
		},
		{
			name: "should succeed",
			cld:  clusterdeployment.NewClusterDeployment(clusterdeployment.WithName(cldName), clusterdeployment.WithNamespace(cldNamespace)),
			existingObjects: []runtime.Object{
				region.New(region.WithClusterDeploymentReference("kcm-system", "test2")),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				Build()
			validator := &ClusterDeploymentValidator{Client: c}
			_, err := validator.ValidateDelete(ctx, tt.cld)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				if err.Error() != tt.err {
					t.Fatalf("expected error '%s', got error: %s", tt.err, err.Error())
				}
			} else {
				g.Expect(err).To(Succeed())
			}
		})
	}
}

func TestClusterDeploymentDefault(t *testing.T) {
	g := NewWithT(t)

	ctx := t.Context()

	clusterDeploymentConfig := `{"foo":"bar"}`

	tests := []struct {
		name            string
		input           *kcmv1.ClusterDeployment
		output          *kcmv1.ClusterDeployment
		existingObjects []runtime.Object
		err             string
	}{
		{
			name:   "should not set defaults if the config is provided",
			input:  clusterdeployment.NewClusterDeployment(clusterdeployment.WithConfig(clusterDeploymentConfig)),
			output: clusterdeployment.NewClusterDeployment(clusterdeployment.WithConfig(clusterDeploymentConfig)),
		},
		{
			name:   "should not set defaults: template is invalid",
			input:  clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(testTemplateName)),
			output: clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(testTemplateName)),
			existingObjects: []runtime.Object{
				mgmt,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
				),
			},
			err: fmt.Sprintf("the ClusterTemplate %s/%s is invalid with the error: validation error example", metav1.NamespaceDefault, testTemplateName),
		},
		{
			name:   "should not set defaults: config in template status is unset",
			input:  clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(testTemplateName)),
			output: clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(testTemplateName)),
			existingObjects: []runtime.Object{
				mgmt,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
				),
			},
		},
		{
			name:  "should set defaults",
			input: clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(testTemplateName)),
			output: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithConfig(clusterDeploymentConfig),
				clusterdeployment.WithDryRun(true),
			),
			existingObjects: []runtime.Object{
				mgmt,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(kcmv1.TemplateValidationStatus{Valid: true}),
					template.WithConfigStatus(clusterDeploymentConfig),
				),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithRuntimeObjects(tt.existingObjects...).
				Build()
			validator := &ClusterDeploymentValidator{Client: c}
			err := validator.Default(ctx, tt.input)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				if err.Error() != tt.err {
					t.Fatalf("expected error '%s', got error: %s", tt.err, err.Error())
				}
			} else {
				g.Expect(err).To(Succeed())
			}
			g.Expect(tt.input).To(Equal(tt.output))
		})
	}
}
