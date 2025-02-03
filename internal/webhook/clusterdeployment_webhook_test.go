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
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	sveltosv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/test/objects/clusterdeployment"
	"github.com/K0rdent/kcm/test/objects/credential"
	"github.com/K0rdent/kcm/test/objects/management"
	"github.com/K0rdent/kcm/test/objects/template"
	"github.com/K0rdent/kcm/test/scheme"
)

var (
	testTemplateName   = "template-test"
	testCredentialName = "cred-test"
	newTemplateName    = "new-template-name"

	testNamespace = "test"

	mgmt = management.NewManagement(
		management.WithAvailableProviders(v1alpha1.Providers{
			"infrastructure-aws",
			"control-plane-k0smotron",
			"bootstrap-k0smotron",
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
)

func TestClusterDeploymentValidateCreate(t *testing.T) {
	g := NewWithT(t)

	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
		},
	})

	tests := []struct {
		name              string
		ClusterDeployment *v1alpha1.ClusterDeployment
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
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithNamespace(testNamespace),
				),
			},
			err: fmt.Sprintf("the ClusterDeployment is invalid: clustertemplates.k0rdent.mirantis.com \"%s\" not found", testTemplateName),
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
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithNamespace("othernamespace"),
				),
			},
			err: fmt.Sprintf("the ClusterDeployment is invalid: servicetemplates.k0rdent.mirantis.com \"%s\" not found", testSvcTemplate1Name),
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
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
				),
			},
			err: "the ClusterDeployment is invalid: the template is not valid: validation error example",
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
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
				),
			},
			err: "the ClusterDeployment is invalid: the template is not valid: validation error example",
		},
		{
			name: "should fail if TemplateResourceRefs are referring to resource in another namespace",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithServiceTemplate(testSvcTemplate1Name),
				clusterdeployment.WithServiceSpec(v1alpha1.ServiceSpec{
					Services: []v1alpha1.Service{
						{Template: testSvcTemplate1Name},
					},
					TemplateResourceRefs: []sveltosv1beta1.TemplateResourceRef{
						{Resource: corev1.ObjectReference{APIVersion: "v1", Kind: "ConfigMap", Name: "test-configmap", Namespace: "othernamespace"}},
						{Resource: corev1.ObjectReference{APIVersion: "v1", Kind: "Secret", Name: "test-secret", Namespace: "othernamespace"}},
					},
				}),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
			},
			err: "the ClusterDeployment is invalid: ConfigMap \"test-configmap\" is in namespace othernamespace, cannot refer to a resource in a namespace other than default in .spec.serviceSpec.templateResourceRefs\nSecret \"test-secret\" is in namespace othernamespace, cannot refer to a resource in a namespace other than default in .spec.serviceSpec.templateResourceRefs",
		},
		{
			name: "should fail if ValuesFrom are referring to resource in another namespace",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithServiceSpec(v1alpha1.ServiceSpec{
					Services: []v1alpha1.Service{
						{
							Template: testSvcTemplate1Name,
							ValuesFrom: []sveltosv1beta1.ValueFrom{
								{Kind: "ConfigMap", Name: "test-configmap", Namespace: "othernamespace"},
								{Kind: "Secret", Name: "test-secret", Namespace: "othernamespace"},
							},
						},
					},
				}),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
			},
			err: "the ClusterDeployment is invalid: ConfigMap \"test-configmap\" is in namespace othernamespace, cannot refer to a resource in a namespace other than default in .spec.serviceSpec.services[].valuesFrom\nSecret \"test-secret\" is in namespace othernamespace, cannot refer to a resource in a namespace other than default in .spec.serviceSpec.services[].valuesFrom",
		},
		{
			name: "should succeed",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithCredential(testCredentialName),
				clusterdeployment.WithServiceSpec(v1alpha1.ServiceSpec{
					TemplateResourceRefs: []sveltosv1beta1.TemplateResourceRef{
						// Should not fail if namespace is empty
						{Resource: corev1.ObjectReference{APIVersion: "v1", Kind: "ConfigMap", Name: "test-configmap"}},
						{Resource: corev1.ObjectReference{APIVersion: "v1", Kind: "Secret", Name: "test-secret"}},
					},
					Services: []v1alpha1.Service{
						{
							Template: testSvcTemplate1Name,
							ValuesFrom: []sveltosv1beta1.ValueFrom{
								// Should not fail if namespace is empty
								{Kind: "ConfigMap", Name: "test-configmap"},
								{Kind: "Secret", Name: "test-secret"},
							},
						},
					},
				}),
			),
			existingObjects: []runtime.Object{
				mgmt,
				cred,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
				template.NewServiceTemplate(
					template.WithName(testSvcTemplate1Name),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
			},
		},
		{
			name: "cluster template k8s version does not satisfy service template constraints",
			ClusterDeployment: clusterdeployment.NewClusterDeployment(
				clusterdeployment.WithClusterTemplate(testTemplateName),
				clusterdeployment.WithServiceTemplate(testTemplateName),
			),
			existingObjects: []runtime.Object{
				cred,
				management.NewManagement(management.WithAvailableProviders(v1alpha1.Providers{
					"infrastructure-aws",
					"control-plane-k0smotron",
					"bootstrap-k0smotron",
				})),
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
					template.WithClusterStatusK8sVersion("v1.30.0"),
				),
				template.NewServiceTemplate(
					template.WithName(testTemplateName),
					template.WithServiceK8sConstraint("<1.30"),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
			},
			err:      fmt.Sprintf(`failed to validate k8s compatibility: k8s version v1.30.0 of the ClusterDeployment default/%s does not satisfy constrained version <1.30 from the ServiceTemplate default/%s`, clusterdeployment.DefaultName, testTemplateName),
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
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
			},
			err: "the ClusterDeployment is invalid: credentials.k0rdent.mirantis.com \"\" not found",
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
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
			},
			err: "the ClusterDeployment is invalid: credential is not Ready",
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
					management.WithAvailableProviders(v1alpha1.Providers{
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					}),
				),
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
			},
			err: "the ClusterDeployment is invalid: wrong kind of the ClusterIdentity \"SomeOtherDummyClusterStaticIdentity\" for provider \"infrastructure-aws\"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			validator := &ClusterDeploymentValidator{Client: c}
			warn, err := validator.ValidateCreate(ctx, tt.ClusterDeployment)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				if err.Error() != tt.err {
					t.Fatalf("expected error '%s', got error: %s", tt.err, err.Error())
				}
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
	)

	g := NewWithT(t)

	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
		},
	})

	tests := []struct {
		name                 string
		oldClusterDeployment *v1alpha1.ClusterDeployment
		newClusterDeployment *v1alpha1.ClusterDeployment
		existingObjects      []runtime.Object
		err                  string
		warnings             admission.Warnings
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
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
				),
			},
			err: "the ClusterDeployment is invalid: the template is not valid: validation error example",
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
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
				template.NewClusterTemplate(
					template.WithName(upgradeTargetTemplateName),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
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
				mgmt, cred,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
					template.WithProvidersStatus(
						"infrastructure-aws",
						"control-plane-k0smotron",
						"bootstrap-k0smotron",
					),
				),
				template.NewClusterTemplate(
					template.WithName(newTemplateName),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
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
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{
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
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{
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
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
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
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{
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
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
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
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{
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
					template.WithNamespace("othernamespace"),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
				),
			},
			err: fmt.Sprintf("the ClusterDeployment is invalid: servicetemplates.k0rdent.mirantis.com \"%s\" not found", testSvcTemplate1Name),
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
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{
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
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
				),
			},
			err: "the ClusterDeployment is invalid: the template is not valid: validation error example",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			validator := &ClusterDeploymentValidator{Client: c}
			warn, err := validator.ValidateUpdate(ctx, tt.oldClusterDeployment, tt.newClusterDeployment)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				if err.Error() != tt.err {
					t.Fatalf("expected error '%s', got error: %s", tt.err, err.Error())
				}
			} else {
				g.Expect(err).To(Succeed())
			}

			g.Expect(warn).To(Equal(tt.warnings))
		})
	}
}

func TestClusterDeploymentDefault(t *testing.T) {
	g := NewWithT(t)

	ctx := context.Background()

	clusterDeploymentConfig := `{"foo":"bar"}`

	tests := []struct {
		name            string
		input           *v1alpha1.ClusterDeployment
		output          *v1alpha1.ClusterDeployment
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
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{
						Valid:           false,
						ValidationError: "validation error example",
					}),
				),
			},
			err: "template is invalid: the template is not valid: validation error example",
		},
		{
			name:   "should not set defaults: config in template status is unset",
			input:  clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(testTemplateName)),
			output: clusterdeployment.NewClusterDeployment(clusterdeployment.WithClusterTemplate(testTemplateName)),
			existingObjects: []runtime.Object{
				mgmt,
				template.NewClusterTemplate(
					template.WithName(testTemplateName),
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
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
					template.WithValidationStatus(v1alpha1.TemplateValidationStatus{Valid: true}),
					template.WithConfigStatus(clusterDeploymentConfig),
				),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjects...).Build()
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
