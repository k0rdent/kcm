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

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	fluxconditions "github.com/fluxcd/pkg/runtime/conditions"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	conditionsutil "github.com/K0rdent/kcm/internal/util/conditions"
	testscheme "github.com/K0rdent/kcm/test/scheme"
)

const (
	kubernetesVersion = "v1.35.1"
	testFinalizer     = "kcm.cluster.k0sproject.io/test-finalizer"

	// Global certificate secret names
	k0sURLCertSecretName   = "test-k0s-url-cert"
	registryCertSecretName = "test-registry-cert"

	// Region configuration
	regionName               = "test-region-cld"
	regionalKubeconfigSecret = "test-regional-kubeconfig"
	regionalKubeconfigKey    = "value"

	// ClusterAuthentication configuration
	clAuthCASecretName = "test-cluster-auth-ca-cert"
	clAuthCASecretKey  = "data"

	// DataSource configuration
	dataSourceName                = "test-data-source"
	dataSourceAuthSecretName      = "test-data-source-auth"
	dataSourceSecretUsernameKey   = "username"
	dataSourceSecretUsernameValue = "user1"
	dataSourceSecretPasswordKey   = "password"
	dataSourceSecretPasswordValue = "123456"
	dataSourceCASecretName        = "test-data-source-ca-cert"
	dataSourceCASecretKey         = "data"

	// ClusterDataSource status references
	cdsCASecretName             = "test-cds-ca-secret"
	cdsKineDataSourceSecretName = "test-kine-datasource-secret"
)

var (
	clusterTemplate = &kcmv1.ClusterTemplate{}

	k0sURLCertSecretData   = map[string][]byte{"data": []byte("test-k0s-url-cert-data")}
	registryCertSecretData = map[string][]byte{"data": []byte("test-registry-cert-data")}

	authConfiguration = &kcmv1.ClusterAuthenticationSpec{
		AuthenticationConfiguration: &kcmv1.AuthenticationConfiguration{
			JWT: []apiserverv1.JWTAuthenticator{
				{
					Issuer: apiserverv1.Issuer{
						URL:       "https://issuer.example.com",
						Audiences: []string{"example-audience"},
					},
				},
			},
		},
	}
	anonAuthConfiguration = &kcmv1.ClusterAuthenticationSpec{
		AuthenticationConfiguration: &kcmv1.AuthenticationConfiguration{
			JWT: []apiserverv1.JWTAuthenticator{
				{
					Issuer: apiserverv1.Issuer{
						URL:       "https://issuer.example.com",
						Audiences: []string{"example-audience"},
					},
				},
			},
			Anonymous: &apiserverv1.AnonymousAuthConfig{
				Enabled: true,
			},
		},
	}
	invalidAuthConfiguration = &kcmv1.ClusterAuthenticationSpec{
		AuthenticationConfiguration: &kcmv1.AuthenticationConfiguration{
			JWT: []apiserverv1.JWTAuthenticator{
				{
					Issuer: apiserverv1.Issuer{
						URL: "123",
					},
				},
			},
		},
	}
	clAuthCASecretData = []byte("test-cluster-auth-ca-cert-data")

	dataSourceCASecretData = []byte("test-data-source-ca-cert-data")
	dataSourceEndpoints    = []string{"postgres-db1.example.com:5432"}
)

// customAuditPolicy returns a custom audit policy with specific rules for use in tests.
func customAuditPolicy() *kcmv1.ClusterAuditPolicySpec {
	return &kcmv1.ClusterAuditPolicySpec{
		Policy: kcmv1.Policy{
			Rules: []auditv1.PolicyRule{
				{
					Level: auditv1.LevelMetadata,
					Resources: []auditv1.GroupResources{
						{
							Group:     "",
							Resources: []string{"secrets", "configmaps"},
						},
					},
				},
				{
					Level: auditv1.LevelRequestResponse,
					Verbs: []string{"create", "update", "delete"},
				},
				{
					Level: auditv1.LevelNone,
					Users: []string{"system:kube-proxy"},
				},
			},
		},
	}
}

type fakeHelmActor struct{}

func (*fakeHelmActor) DownloadChartFromArtifact(_ context.Context, _ *fluxmeta.Artifact) (*chart.Chart, error) {
	return &chart.Chart{
		Metadata: &chart.Metadata{
			APIVersion: "v2",
			Version:    "0.1.0",
			Name:       "test-cluster-chart",
		},
	}, nil
}

func (*fakeHelmActor) InitializeConfiguration(_ *kcmv1.ClusterDeployment, _ action.DebugLog) (*action.Configuration, error) {
	return &action.Configuration{}, nil
}

func (*fakeHelmActor) EnsureReleaseWithValues(_ context.Context, _ *action.Configuration, _ *chart.Chart, _ *kcmv1.ClusterDeployment) error {
	return nil
}

// cldTestCase holds parameters for a single ClusterDeployment reconciliation test.
type cldTestCase struct {
	// Global reconciler settings
	globalRegistry         string
	globalK0sURL           string
	k0sURLCertSecretName   string
	registryCertSecretName string
	isDisabledValidationWH bool

	// ClusterDeployment spec overrides
	region      string
	dryRun      bool
	authConfig  *kcmv1.ClusterAuthenticationSpec
	auditPolicy *kcmv1.ClusterAuditPolicySpec
	dataSource  string
	config      map[string]any
}

// hasGlobalValues reports whether any global override is configured.
func (tc *cldTestCase) hasGlobalValues() bool {
	return tc.globalK0sURL != "" || tc.globalRegistry != "" ||
		tc.k0sURLCertSecretName != "" || tc.registryCertSecretName != ""
}

// ensureCredential creates an AWS Credential with ready status.
func (tc *cldTestCase) ensureCredential(namespace string) *kcmv1.Credential {
	GinkgoHelper()

	cred := &kcmv1.Credential{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-credential-aws-",
			Namespace:    namespace,
		},
		Spec: kcmv1.CredentialSpec{
			IdentityRef: &corev1.ObjectReference{
				APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
				Kind:       "AWSClusterStaticIdentity",
				Name:       "foo",
			},
		},
	}
	if tc.region != "" {
		cred.Spec.Region = tc.region
	}

	Expect(k8sClient.Create(ctx, cred)).To(Succeed())

	cred.Status = kcmv1.CredentialStatus{Ready: true}
	Expect(k8sClient.Status().Update(ctx, cred)).To(Succeed())

	Eventually(func(g Gomega) {
		g.Expect(mgrClient.Get(ctx, crclient.ObjectKeyFromObject(cred), cred)).To(Succeed())
		g.Expect(cred.Status.Ready).To(BeTrue())
	}).Should(Succeed())

	return cred
}

// ensureClusterAuthentication creates an ClusterAuthentication object.
func (tc *cldTestCase) ensureClusterAuthentication(namespace string) *kcmv1.ClusterAuthentication {
	GinkgoHelper()

	clAuth := &kcmv1.ClusterAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-cl-auth",
			Namespace:    namespace,
		},
		Spec: *tc.authConfig,
	}

	clAuth.Spec.CASecret = &kcmv1.SecretKeyReference{
		SecretReference: corev1.SecretReference{
			Namespace: namespace,
			Name:      clAuthCASecretName,
		},
		Key: clAuthCASecretKey,
	}

	Expect(k8sClient.Create(ctx, clAuth)).To(Succeed())
	Eventually(func(g Gomega) {
		g.Expect(mgrClient.Get(ctx, crclient.ObjectKeyFromObject(clAuth), clAuth)).To(Succeed())
	}).Should(Succeed())

	return clAuth
}

// ensureClusterAuditPolicy creates a ClusterAuditPolicy object with the given policy.
func (tc *cldTestCase) ensureClusterAuditPolicy(namespace string) *kcmv1.ClusterAuditPolicy {
	GinkgoHelper()

	clAuditPolicy := &kcmv1.ClusterAuditPolicy{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-cl-audit-policy-",
			Namespace:    namespace,
		},
		Spec: *tc.auditPolicy,
	}

	Expect(k8sClient.Create(ctx, clAuditPolicy)).To(Succeed())
	Eventually(func(g Gomega) {
		g.Expect(mgrClient.Get(ctx, crclient.ObjectKeyFromObject(clAuditPolicy), clAuditPolicy)).To(Succeed())
	}).Should(Succeed())

	return clAuditPolicy
}

// ensureClusterDeployment creates a ClusterDeployment with the given spec overrides.
// Returns the created ClusterDeployment.
func (tc *cldTestCase) ensureClusterDeployment(namespace, clusterTemplateName, credentialName, clAuthName, auditPolicyName string) *kcmv1.ClusterDeployment {
	GinkgoHelper()

	cld := &kcmv1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-cluster-deployment-",
			Namespace:    namespace,
		},
		Spec: kcmv1.ClusterDeploymentSpec{
			Template:    clusterTemplateName,
			Credential:  credentialName,
			ClusterAuth: clAuthName,
			DryRun:      tc.dryRun,
		},
	}
	if auditPolicyName != "" {
		cld.Spec.AuditPolicy = auditPolicyName
	}
	if tc.dataSource != "" {
		cld.Spec.DataSource = tc.dataSource
	}
	if tc.config != nil {
		Expect(cld.SetHelmValues(tc.config)).NotTo(HaveOccurred())
	}

	Expect(k8sClient.Create(ctx, cld)).To(Succeed())
	Eventually(func(g Gomega) {
		g.Expect(mgrClient.Get(ctx, crclient.ObjectKeyFromObject(cld), cld)).To(Succeed())
	}).Should(Succeed())

	return cld
}

func (tc *cldTestCase) newTestClusterDeploymentReconciler() *ClusterDeploymentReconciler {
	return &ClusterDeploymentReconciler{
		MgmtClient:             mgrClient,
		helmActor:              &fakeHelmActor{},
		SystemNamespace:        systemNamespace,
		GlobalRegistry:         tc.globalRegistry,
		GlobalK0sURL:           tc.globalK0sURL,
		K0sURLCertSecretName:   tc.k0sURLCertSecretName,
		RegistryCertSecretName: tc.registryCertSecretName,

		IsDisabledValidationWH: tc.isDisabledValidationWH,

		DefaultHelmTimeout: 20 * time.Minute,
		defaultRequeueTime: 10 * time.Second,
	}
}

// testClusterDeploymentReconciliation drives the full reconciliation lifecycle:
// finalizer, labels, region pause/unpause, cert secrets, template/credential validation,
// HelmRelease creation, CAPI Cluster status reflection, and final ready condition.
func (tc *cldTestCase) testClusterDeploymentReconciliation(reconciler *ClusterDeploymentReconciler, cldName types.NamespacedName) {
	GinkgoHelper()

	var (
		err    error
		result ctrl.Result
		cld    = &kcmv1.ClusterDeployment{}
	)

	By("First reconciliation, should add finalizer", func() {
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
			g.Expect(cld.Finalizers).To(ContainElement(kcmv1.ClusterDeploymentFinalizer))
		}).Should(Succeed())
	})

	By("Should add KCM component label", func() {
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
			g.Expect(cld.Labels).To(HaveKeyWithValue(kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM))
		}).Should(Succeed())
	})

	if tc.region != "" {
		region := &kcmv1.Region{}
		By("Should not reconcile ClusterDeployment if region is paused", func() {
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tc.region}, region)).To(Succeed())

			if region.Annotations == nil {
				region.Annotations = make(map[string]string)
			}
			region.Annotations[kcmv1.RegionPauseAnnotation] = "true"
			Expect(k8sClient.Update(ctx, region)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Name: tc.region}, region)).To(Succeed())
				g.Expect(region.Annotations[kcmv1.RegionPauseAnnotation]).To(Equal("true"))
			}).Should(Succeed())

			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
				g.Expect(cld).Should(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.PausedCondition),
						HaveField("Status", metav1.ConditionTrue),
						HaveField("Reason", kcmv1.PausedReason),
						HaveField("Message", Equal(fmt.Sprintf("Related Region %s is paused", tc.region))),
					))),
				)
			}).Should(Succeed())
		})

		By("Should reconcile ClusterDeployment when region is unpaused", func() {
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tc.region}, region)).To(Succeed())

			delete(region.Annotations, kcmv1.RegionPauseAnnotation)
			Expect(k8sClient.Update(ctx, region)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Name: regionName}, region)).To(Succeed())
				g.Expect(region.Annotations).NotTo(HaveKey(kcmv1.RegionPauseAnnotation))
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				region := &kcmv1.Region{}
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Name: regionName}, region)).To(Succeed())
				g.Expect(region.Annotations).NotTo(HaveKey(kcmv1.RegionPauseAnnotation))
			}).Should(Succeed())

			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())

			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
				g.Expect(cld).Should(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.PausedCondition),
						HaveField("Status", metav1.ConditionFalse),
						HaveField("Reason", kcmv1.NotPausedReason),
					))),
				)
			}).Should(Succeed())
		})
	}

	result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
	Expect(err).NotTo(HaveOccurred())

	if tc.k0sURLCertSecretName != "" || tc.registryCertSecretName != "" {
		By("Should copy registry and k0s URL cert secrets to the cluster namespace if specified", func() {
			if cld.Namespace == reconciler.SystemNamespace {
				return
			}
			Eventually(func(g Gomega) {
				secret := &corev1.Secret{}
				if tc.k0sURLCertSecretName != "" {
					// TODO: use regional client if region is specified
					g.Expect(mgrClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: tc.k0sURLCertSecretName}, secret)).To(Succeed())
					g.Expect(secret.Data).To(Equal(k0sURLCertSecretData))
				}
				if tc.registryCertSecretName != "" {
					g.Expect(mgrClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: tc.registryCertSecretName}, secret)).To(Succeed())
					g.Expect(secret.Data).To(Equal(registryCertSecretData))
				}
			}).Should(Succeed())
		})
	}

	// TODO: add IPAM tests

	By("Should not be ready if the template is not valid", func() {
		setClusterTemplateValidationStatus(clusterTemplate, "some cluster template error", false)

		Eventually(func(g Gomega) {
			_, reconcileErr := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
			if tc.isDisabledValidationWH {
				g.Expect(reconcileErr).NotTo(HaveOccurred())
			} else {
				g.Expect(reconcileErr).To(HaveOccurred())
			}

			g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
			g.Expect(cld).Should(
				HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", kcmv1.TemplateReadyCondition),
					HaveField("Status", metav1.ConditionFalse),
					HaveField("Reason", kcmv1.FailedReason),
					HaveField("Message", Equal(
						fmt.Sprintf(
							"ClusterTemplate %s/%s is not marked as valid: some cluster template error",
							clusterTemplate.Namespace, clusterTemplate.Name,
						),
					)),
				))),
			)
		}).Should(Succeed())

		setClusterTemplateValidationStatus(clusterTemplate, "", true)
	})

	cred := &kcmv1.Credential{}
	By("Should not be ready if the credential is not ready", func() {
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: cld.Spec.Credential}, cred)).To(Succeed())

		setCredentialReadyStatus(cred, false)

		if !tc.isDisabledValidationWH {
			By("Should set Credential ready condition to false", func() {
				Eventually(func(g Gomega) {
					_, reconcileErr := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
					g.Expect(reconcileErr).NotTo(HaveOccurred())
					g.Expect(mgrClient.Get(ctx, crclient.ObjectKeyFromObject(cld), cld)).To(Succeed())
					g.Expect(cld).Should(
						HaveField("Status.Conditions", ContainElement(SatisfyAll(
							HaveField("Type", kcmv1.CredentialReadyCondition),
							HaveField("Status", metav1.ConditionFalse),
							HaveField("Reason", kcmv1.FailedReason),
						))),
					)
				}).Should(Succeed())
			})
		}

		setCredentialReadyStatus(cred, true)
	})

	By("Reconciling ClusterDeployment", func() {
		result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
			g.Expect(cld.Status.KubernetesVersion).To(Equal(kubernetesVersion))
			g.Expect(cld).Should(
				HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", kcmv1.TemplateReadyCondition),
					HaveField("Status", metav1.ConditionTrue),
					HaveField("Reason", kcmv1.SucceededReason),
				))),
			)
			if !tc.isDisabledValidationWH {
				g.Expect(cld).Should(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.CredentialReadyCondition),
						HaveField("Status", metav1.ConditionTrue),
						HaveField("Reason", kcmv1.SucceededReason),
					))),
				)
			}
			if tc.region != "" {
				g.Expect(cld.Status.Region).To(Equal(tc.region))
			} else {
				g.Expect(cld.Status.Region).To(BeEmpty())
			}
		}).Should(Succeed())
	})

	if tc.dryRun {
		By("Expect HelmRelease to not be created in DryRun mode", func() {
			hr := &helmcontrollerv2.HelmRelease{}
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, crclient.ObjectKeyFromObject(cld), hr))).To(BeTrue())
		})
		return
	}

	if tc.authConfig != nil {
		By("Should create the secret with authentication configuration", func() {
			secretName := cld.Name + "-auth-config"

			rawAuthConfData, err := json.Marshal(tc.authConfig.GetAuthConfig())
			Expect(err).NotTo(HaveOccurred())

			var expectedAuthConfData apiserverv1.AuthenticationConfiguration
			Expect(json.Unmarshal(rawAuthConfData, &expectedAuthConfData)).NotTo(HaveOccurred())

			for i := range expectedAuthConfData.JWT {
				expectedAuthConfData.JWT[i].Issuer.CertificateAuthority = string(clAuthCASecretData)
			}
			data, err := yaml.Marshal(expectedAuthConfData)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				secret := &corev1.Secret{}
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: secretName}, secret)).To(Succeed())
				g.Expect(string(secret.Data[authConfigSecretKey])).To(MatchYAML(string(data)))
			}).Should(Succeed())
		})
	}

	if tc.auditPolicy != nil {
		By("Should create the ConfigMap with audit policy", func() {
			expectedData, err := yaml.Marshal(*tc.auditPolicy.GetPolicy())
			Expect(err).NotTo(HaveOccurred())

			cmName := cld.Name + "-audit-policy"
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: cmName}, cm)).To(Succeed())
				g.Expect(cm.Data).To(HaveKey("policy"))
				g.Expect(cm.Data["policy"]).To(MatchYAML(string(expectedData)))
			}).Should(Succeed())
		})

		By("Should set ClusterAuditPolicyReady condition to true", func() {
			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
				g.Expect(cld).Should(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.ClusterAuditPolicyReadyCondition),
						HaveField("Status", metav1.ConditionTrue),
						HaveField("Reason", kcmv1.SucceededReason),
					))),
				)
			}).Should(Succeed())
		})
	} else {
		By("Should not create the ConfigMap with audit policy", func() {
			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: cld.Name + "-audit-policy"}, &corev1.ConfigMap{})).To(Satisfy(apierrors.IsNotFound))
			}).Should(Succeed())
		})
	}

	if tc.dataSource != "" {
		By("Should create the ClusterDataSource object", func() {
			cds := &kcmv1.ClusterDataSource{}
			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: cld.Name}, cds)).To(Succeed())
				g.Expect(cds.Labels).To(HaveKeyWithValue(kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM))
				g.Expect(cds.Labels).To(HaveKeyWithValue(kcmv1.KCMManagedLabelKey, kcmv1.KCMManagedLabelValue))
				g.Expect(cds.Finalizers).To(ContainElement(kcmv1.ClusterDataSourceFinalizer))
				g.Expect(cds.OwnerReferences).To(ContainElement(SatisfyAll(
					HaveField("APIVersion", "k0rdent.mirantis.com/v1beta1"),
					HaveField("Kind", "ClusterDeployment"),
					HaveField("Name", cld.Name),
				)))
				g.Expect(cds.Spec.Schema).To(HavePrefix(strings.ReplaceAll(fmt.Sprintf("%s-%s", cld.Namespace, cld.Name), "-", "_")))
				// length should be namespace + name + 2 for the hyphen + 5 for the hash suffix
				g.Expect(cds.Spec.Schema).To(HaveLen(len(cld.Namespace) + len(cld.Name) + 7))
				g.Expect(cds.Spec.DataSource).To(Equal(cld.Spec.DataSource))

				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: cld.Name}, cld)).To(Succeed())
				g.Expect(cld).Should(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", kcmv1.ClusterDataSourceReadyCondition),
						HaveField("Status", metav1.ConditionFalse),
						HaveField("Reason", kcmv1.ProgressingReason),
						HaveField("Message", fmt.Sprintf("cross-referenced ClusterDataSource %s/%s is not yet ready", cld.Namespace, cld.Name)),
					))),
				)
			}).To(Succeed())
		})
		By("Expect the reconcile to requeue until the cluster data source is ready", func() {
			Expect(result.RequeueAfter).To(Equal(reconciler.defaultRequeueTime))
			Expect(err).NotTo(HaveOccurred())
		})
		By("Set ClusterDataSource as Ready", func() {
			cdsCASecret := ensureSecret(cld.Namespace, cdsCASecretName, map[string][]byte{})
			cdsKineDataSourceSecret := ensureSecret(cld.Namespace, cdsKineDataSourceSecretName, map[string][]byte{})
			DeferCleanup(k8sClient.Delete, cdsCASecret)
			DeferCleanup(k8sClient.Delete, cdsKineDataSourceSecret)

			cds := &kcmv1.ClusterDataSource{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: cld.Name}, cds)).To(Succeed())
			cds.Status.ObservedGeneration = cds.Generation
			cds.Status.CASecret = cdsCASecretName
			cds.Status.KineDataSourceSecret = cdsKineDataSourceSecretName
			cds.Status.Ready = true

			Expect(k8sClient.Status().Update(ctx, cds)).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: cld.Name}, cds)).To(Succeed())
				g.Expect(cds.Status.ObservedGeneration).To(Equal(cds.Generation))
				g.Expect(cds.Status.CASecret).To(Equal(cdsCASecretName))
				g.Expect(cds.Status.KineDataSourceSecret).To(Equal(cdsKineDataSourceSecretName))
				g.Expect(cds.Status.Ready).To(BeTrue())
			}).Should(Succeed())

			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
				g.Expect(cld).Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", kcmv1.ClusterDataSourceReadyCondition),
					HaveField("Status", metav1.ConditionTrue),
					HaveField("Reason", kcmv1.SucceededReason),
				))))
			}).Should(Succeed())
		})
	}

	if tc.region != "" {
		By("Expect regional kubeconfig secret has been copied to the cluster deployment namespace", func() {
			Eventually(func(g Gomega) {
				kubeconfigSecret := &corev1.Secret{}
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: regionalKubeconfigSecret}, kubeconfigSecret)).To(Succeed())
				g.Expect(kubeconfigSecret.Data[regionalKubeconfigKey]).To(Equal(buildKubeconfigFromRestConfig(cfg)))
			}).To(Succeed())
		})
	}

	By("Expect HelmRelease to be created with expected values", func() {
		var actualValues map[string]any
		expectedValues := tc.buildExpectedHelmReleaseValues(cred, cld.Name)

		Eventually(func(g Gomega) {
			hr := &helmcontrollerv2.HelmRelease{}
			g.Expect(mgrClient.Get(ctx, crclient.ObjectKeyFromObject(cld), hr)).NotTo(HaveOccurred())
			g.Expect(hr.Labels).To(HaveKeyWithValue(kcmv1.KCMManagedLabelKey, kcmv1.KCMManagedLabelValue))
			g.Expect(hr.OwnerReferences).To(ContainElement(SatisfyAll(
				HaveField("APIVersion", "k0rdent.mirantis.com/v1beta1"),
				HaveField("Kind", "ClusterDeployment"),
				HaveField("Name", cld.Name),
			)))

			err := json.Unmarshal(hr.Spec.Values.Raw, &actualValues)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(actualValues).To(Equal(expectedValues))
		}).Should(Succeed())
	})

	By("Adding testing finalizer on a HelmRelease", func() {
		hr := &helmcontrollerv2.HelmRelease{}
		Expect(k8sClient.Get(ctx, crclient.ObjectKeyFromObject(cld), hr)).NotTo(HaveOccurred())
		hr.Finalizers = append(hr.Finalizers, testFinalizer)
		Expect(k8sClient.Update(ctx, hr)).To(Succeed())

		Eventually(func(g Gomega) {
			hr := &helmcontrollerv2.HelmRelease{}
			g.Expect(mgrClient.Get(ctx, crclient.ObjectKeyFromObject(cld), hr)).NotTo(HaveOccurred())
			g.Expect(hr.Finalizers).To(ContainElement(testFinalizer))
		}).Should(Succeed())
	})

	By("Expect the ClusterDeployment status to reflect actual CAPI Cluster state", func() {
		Expect(result.RequeueAfter).To(Equal(reconciler.defaultRequeueTime))

		By("Expect ClusterDeployment to not to have CAPI Cluster summary condition when CAPI cluster is not yet created", func() {
			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
				g.Expect(cld).Should(Not(
					HaveField("Status.Conditions", ContainElement(
						HaveField("Type", kcmv1.CAPIClusterSummaryCondition),
					)),
				))
			}).Should(Succeed())
		})

		cluster := clusterapiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cld.Name,
				Namespace: cld.Namespace,
				Labels:    map[string]string{kcmv1.FluxHelmChartNameKey: cld.Name},
			},
			Spec: clusterapiv1.ClusterSpec{Paused: new(false)},
		}
		Expect(k8sClient.Create(ctx, &cluster)).To(Succeed())
		DeferCleanup(func() error {
			return crclient.IgnoreNotFound(k8sClient.Delete(ctx, &cluster))
		})

		Eventually(func(g Gomega) {
			g.Expect(mgrClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: cld.Name}, &cluster)).To(Succeed())
		}).Should(Succeed())

		By("Expect ClusterDeployment to have CAPI Cluster summary condition after the next reconcile", func() {
			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(reconciler.defaultRequeueTime))

			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
				g.Expect(cld).Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", kcmv1.CAPIClusterSummaryCondition),
					HaveField("Status", metav1.ConditionUnknown),
					HaveField("Reason", "UnknownReported"),
					HaveField("Message", SatisfyAll(
						ContainSubstring("InfrastructureReady"),
						ContainSubstring("ControlPlaneInitialized"),
						ContainSubstring("ControlPlaneAvailable"),
						ContainSubstring("WorkersAvailable"),
						ContainSubstring("RemoteConnectionProbe"),
					)),
				))))
			}).Should(Succeed())
		})

		By("Expect ClusterDeployment to have ready CAPI Cluster summary condition when CAPI Cluster is ready", func() {
			cluster.Status = clusterapiv1.ClusterStatus{
				Conditions: getCAPIClusterReadyConditions(),
			}
			Expect(k8sClient.Status().Update(ctx, &cluster)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, types.NamespacedName{Namespace: cld.Namespace, Name: cld.Name}, &cluster)).To(Succeed())
				g.Expect(cluster.Status.Conditions).To(ContainElement(SatisfyAll(
					HaveField("Type", clusterapiv1.ReadyCondition),
					HaveField("Status", metav1.ConditionTrue),
				)))
				g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
				g.Expect(cld.Status.Conditions).To(ContainElement(
					HaveField("Type", kcmv1.CAPIClusterSummaryCondition),
				))
			}).Should(Succeed())

			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
				g.Expect(cld).Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", kcmv1.CAPIClusterSummaryCondition),
					HaveField("Status", metav1.ConditionTrue),
					HaveField("Reason", "InfoReported"),
					HaveField("Message", SatisfyAll(
						ContainSubstring("InfrastructureReady"),
						ContainSubstring("ControlPlaneInitialized"),
						ContainSubstring("ControlPlaneAvailable"),
						ContainSubstring("WorkersAvailable"),
						ContainSubstring("RemoteConnectionProbe"),
					)),
				))))
			}).Should(Succeed())
		})
	})

	By("Expect ClusterDeployment to have Ready condition once HelmRelease is ready", func() {
		// Expect the reconciler to requeue until the HelmRelease is ready
		Expect(result.RequeueAfter).To(Equal(reconciler.defaultRequeueTime))

		hr := &helmcontrollerv2.HelmRelease{}
		Expect(k8sClient.Get(ctx, crclient.ObjectKeyFromObject(cld), hr)).NotTo(HaveOccurred())
		fluxconditions.Set(hr, &metav1.Condition{
			Type:   fluxmeta.ReadyCondition,
			Reason: "HelmReleaseReady",
			Status: metav1.ConditionTrue,
		})
		Expect(k8sClient.Status().Update(ctx, hr)).To(Succeed())

		Eventually(func(g Gomega) {
			hr := &helmcontrollerv2.HelmRelease{}
			g.Expect(mgrClient.Get(ctx, crclient.ObjectKeyFromObject(cld), hr)).To(Succeed())
			g.Expect(fluxconditions.IsTrue(hr, fluxmeta.ReadyCondition)).To(BeTrue())
		}).Should(Succeed())

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
			g.Expect(cld).Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
				HaveField("Type", kcmv1.ReadyCondition),
				HaveField("Status", metav1.ConditionTrue),
				HaveField("Reason", kcmv1.SucceededReason),
				HaveField("Message", "Object is ready"),
			))))
		}).Should(Succeed())
	})
}

// testClusterDeploymentCleanup drives the deletion lifecycle:
// deleting condition, ServiceSet cleanup, CAPI Cluster/HelmRelease/ClusterDataSource
// cleanup, and final finalizer removal.
func (tc *cldTestCase) testClusterDeploymentCleanup(reconciler *ClusterDeploymentReconciler, cldName types.NamespacedName) {
	GinkgoHelper()

	var (
		err    error
		result ctrl.Result
		cld    = &kcmv1.ClusterDeployment{}
	)

	if tc.isDisabledValidationWH {
		By("Should block ClusterDeployment deletion if referenced by existing region", func() {
			regionName := "test-rgn-referencing-cld"
			region := &kcmv1.Region{
				ObjectMeta: metav1.ObjectMeta{
					Name: regionName,
				},
				Spec: kcmv1.RegionSpec{
					ClusterDeployment: &kcmv1.ClusterDeploymentRef{
						Name:      cldName.Name,
						Namespace: cldName.Namespace,
					},
				},
			}
			Expect(k8sClient.Create(ctx, region)).To(Succeed())
			DeferCleanup(func() error {
				return crclient.IgnoreNotFound(k8sClient.Delete(ctx, region))
			})

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: regionName}, region)).Should(Succeed())

			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
			Expect(err).To(MatchError(fmt.Sprintf("ClusterDeployment cannot be deleted: referenced by Region %q", regionName)))

			Expect(k8sClient.Delete(ctx, region)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(apierrors.IsNotFound(mgrClient.Get(ctx, types.NamespacedName{Name: regionName}, region))).To(BeTrue())
			}).Should(Succeed())
		})
	}

	By("First deletion reconciliation should set deleting condition", func() {
		result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
			g.Expect(cld).Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
				HaveField("Type", kcmv1.DeletingCondition),
				HaveField("Status", metav1.ConditionTrue),
			))))
		}).Should(Succeed())
	})

	By("ServiceSet should be deleted if created", func() {
		Expect(result.RequeueAfter).To(Equal(reconciler.defaultRequeueTime))

		ss := kcmv1.ServiceSet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cld.Namespace,
				Name:      cld.Name,
			},
		}
		Eventually(func(g Gomega) {
			g.Expect(apierrors.IsNotFound(mgrClient.Get(ctx, crclient.ObjectKeyFromObject(cld), &ss))).To(BeTrue())
		}).Should(Succeed())
	})

	if tc.dryRun {
		By("DryRun mode: deletion should complete immediately", func() {
			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				err := mgrClient.Get(ctx, cldName, cld)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}).WithTimeout(10 * time.Second).WithPolling(250 * time.Millisecond).Should(Succeed())
		})
		return
	}

	By("Should wait for CAPI Cluster deletion", func() {
		result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
		Expect(err).NotTo(HaveOccurred())

		// Expect requeue while Cluster still exists
		Expect(result.RequeueAfter).To(Equal(reconciler.defaultRequeueTime))

		Eventually(func(g Gomega) {
			g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
			g.Expect(cld).Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
				HaveField("Type", kcmv1.DeletingCondition),
				HaveField("Status", metav1.ConditionTrue),
				HaveField("Reason", kcmv1.WaitingForClusterDeletionReason),
			))))
		}).Should(Succeed())

		cluster := &clusterapiv1.Cluster{}
		err := k8sClient.Get(ctx, cldName, cluster)
		if err == nil {
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(mgrClient.Get(ctx, cldName, cluster))
			}).Should(BeTrue())
		}

		Eventually(func() bool {
			return apierrors.IsNotFound(mgrClient.Get(ctx, cldName, &clusterapiv1.Cluster{}))
		}).Should(BeTrue())
	})

	By("Should wait for HelmRelease deletion", func() {
		result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
			g.Expect(cld).Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
				HaveField("Type", kcmv1.DeletingCondition),
				HaveField("Status", metav1.ConditionTrue),
				HaveField("Reason", kcmv1.WaitingForHelmReleaseDeletionReason),
			))))
		}).Should(Succeed())

		// Expect requeue while HelmRelease still exists
		Expect(result.RequeueAfter).To(Equal(reconciler.defaultRequeueTime))
	})

	By("Deleting the HelmRelease", func() {
		hr := &helmcontrollerv2.HelmRelease{}
		err := k8sClient.Get(ctx, cldName, hr)
		if err == nil {
			hr.Finalizers = nil
			Expect(k8sClient.Update(ctx, hr)).To(Succeed())
			Eventually(func(g Gomega) {
				err := mgrClient.Get(ctx, cldName, hr)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}).Should(Succeed())
		}

		Eventually(func() bool {
			return apierrors.IsNotFound(mgrClient.Get(ctx, cldName, &helmcontrollerv2.HelmRelease{}))
		}).Should(BeTrue())
	})

	if tc.dataSource != "" {
		By("Should wait for ClusterDataSource deletion", func() {
			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
			Expect(err).NotTo(HaveOccurred())

			Expect(result.RequeueAfter).To(Equal(reconciler.defaultRequeueTime))

			Eventually(func(g Gomega) {
				g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
				g.Expect(cld).Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", kcmv1.DeletingCondition),
					HaveField("Status", metav1.ConditionTrue),
					HaveField("Reason", kcmv1.WaitingForClusterDataSourceDeletionReason),
				))))
			}).Should(Succeed())

			cds := &kcmv1.ClusterDataSource{}
			err := k8sClient.Get(ctx, cldName, cds)
			if err == nil {
				cds.Finalizers = nil
				Expect(k8sClient.Update(ctx, cds)).To(Succeed())
				Eventually(func(g Gomega) {
					err := mgrClient.Get(ctx, cldName, cds)
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				}).Should(Succeed())
			}

			Eventually(func() bool {
				return apierrors.IsNotFound(mgrClient.Get(ctx, cldName, &kcmv1.ClusterDataSource{}))
			}).Should(BeTrue())
		})
	}

	By("Final reconciliation should remove finalizer and complete deletion", func() {
		result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: cldName})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())

		Eventually(func(g Gomega) {
			err := mgrClient.Get(ctx, cldName, cld)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}).Should(Succeed())
	})
}

// buildExpectedHelmReleaseValues constructs the expected Helm values map for a
// given test case, credential, and ClusterDeployment name.
func (tc *cldTestCase) buildExpectedHelmReleaseValues(cred *kcmv1.Credential, cldName string) map[string]any {
	GinkgoHelper()

	expected := map[string]any{
		"clusterIdentity": map[string]any{
			"namespace":  cred.Spec.IdentityRef.Namespace,
			"apiVersion": cred.Spec.IdentityRef.APIVersion,
			"kind":       cred.Spec.IdentityRef.Kind,
			"name":       cred.Spec.IdentityRef.Name,
		},
		"clusterLabels": map[string]any{
			"k0rdent.mirantis.com/component": "kcm",
		},
	}

	if tc.hasGlobalValues() {
		expected["global"] = map[string]any{
			"k0sURL":             tc.globalK0sURL,
			"registry":           tc.globalRegistry,
			"k0sURLCertSecret":   tc.k0sURLCertSecretName,
			"registryCertSecret": tc.registryCertSecretName,
		}
	}

	if tc.authConfig != nil {
		expected["auth"] = buildExpectedAuthValues(cldName, tc.authConfig)
	}

	if tc.auditPolicy != nil {
		data, err := yaml.Marshal(*tc.auditPolicy.GetPolicy())
		Expect(err).NotTo(HaveOccurred())
		hash := sha256.Sum256(data)

		expected["audit"] = map[string]any{
			"policyRef": map[string]any{
				"name": cldName + "-audit-policy",
				"key":  auditPolicyConfigKey,
				"hash": hex.EncodeToString(hash[:4]),
			},
		}
	}

	if tc.dataSource != "" {
		expected["dataSource"] = map[string]any{
			"caSecret": map[string]any{
				"key":  dataSourceCASecretKey,
				"name": cdsCASecretName,
			},
			"kineDataSourceSecretName": cdsKineDataSourceSecretName,
		}
	}

	return chartutil.CoalesceTables(expected, tc.config)
}

var _ = Describe("ClusterDeployment Controller", Ordered, func() {
	var reconciler *ClusterDeploymentReconciler

	var (
		namespace                corev1.Namespace
		pi                       kcmv1.ProviderInterface
		serviceTemplate          kcmv1.ServiceTemplate
		helmRepo                 sourcev1.HelmRepository
		clusterTemplateHelmChart sourcev1.HelmChart
		serviceTemplateHelmChart sourcev1.HelmChart
	)

	BeforeAll(func() {
		By("ensuring system namespace exists", func() {
			namespace = corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: systemNamespace,
				},
			}
			Expect(crclient.IgnoreAlreadyExists(k8sClient.Create(ctx, &namespace))).To(Succeed())
		})

		By("creating test cluster namespace", func() {
			namespace = corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-namespace-",
				},
			}
			Expect(k8sClient.Create(ctx, &namespace)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &namespace)
		})

		By("creating ProviderInterface", func() {
			pi = kcmv1.ProviderInterface{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "aws-provider-",
				},
				Spec: kcmv1.ProviderInterfaceSpec{
					ClusterGVKs: []kcmv1.GroupVersionKind{
						{
							Group:   "infrastructure.cluster.x-k8s.io",
							Version: "v1beta2",
							Kind:    "AWSCluster",
						},
						{
							Group:   "infrastructure.cluster.x-k8s.io",
							Version: "v1beta2",
							Kind:    "AWSManagedCluster",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, &pi)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &pi)
		})

		By("creating regional kubeconfig secret and Region", func() {
			kubeconfigData := buildKubeconfigFromRestConfig(cfg)
			kubeconfigSecret := ensureSecret(systemNamespace, regionalKubeconfigSecret, map[string][]byte{regionalKubeconfigKey: kubeconfigData})
			DeferCleanup(k8sClient.Delete, kubeconfigSecret)

			region := kcmv1.Region{
				ObjectMeta: metav1.ObjectMeta{
					Name: regionName,
				},
				Spec: kcmv1.RegionSpec{
					KubeConfig: &fluxmeta.SecretKeyReference{
						Name: regionalKubeconfigSecret,
						Key:  regionalKubeconfigKey,
					},
				},
			}
			Expect(k8sClient.Create(ctx, &region)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &region)
		})

		By("creating HelmRepository", func() {
			helmRepo = sourcev1.HelmRepository{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-repository-",
					Namespace:    namespace.Name,
				},
				Spec: sourcev1.HelmRepositorySpec{
					Insecure: true,
					Interval: metav1.Duration{
						Duration: 10 * time.Minute,
					},
					Provider: "generic",
					Type:     "oci",
					URL:      "oci://kcm-local-registry:5000/charts",
				},
			}
			Expect(k8sClient.Create(ctx, &helmRepo)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &helmRepo)
		})

		By("creating HelmChart resources", func() {
			clusterTemplateHelmChart = sourcev1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-cluster-template-chart-",
					Namespace:    namespace.Name,
				},
				Spec: sourcev1.HelmChartSpec{
					Chart: "test-cluster",
					Interval: metav1.Duration{
						Duration: 10 * time.Minute,
					},
					ReconcileStrategy: sourcev1.ReconcileStrategyChartVersion,
					SourceRef: sourcev1.LocalHelmChartSourceReference{
						Kind: "HelmRepository",
						Name: helmRepo.Name,
					},
					Version: "0.1.0",
				},
			}
			Expect(k8sClient.Create(ctx, &clusterTemplateHelmChart)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &clusterTemplateHelmChart)

			serviceTemplateHelmChart = sourcev1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-service-template-chart-",
					Namespace:    namespace.Name,
				},
				Spec: sourcev1.HelmChartSpec{
					Chart: "test-service",
					Interval: metav1.Duration{
						Duration: 10 * time.Minute,
					},
					ReconcileStrategy: sourcev1.ReconcileStrategyChartVersion,
					SourceRef: sourcev1.LocalHelmChartSourceReference{
						Kind: "HelmRepository",
						Name: helmRepo.Name,
					},
					Version: "0.1.0",
				},
			}
			Expect(k8sClient.Create(ctx, &serviceTemplateHelmChart)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &serviceTemplateHelmChart)
		})

		clusterTemplate = ensureClusterTemplate(namespace.Name, clusterTemplateHelmChart.Name)
		DeferCleanup(k8sClient.Delete, clusterTemplate)

		setClusterTemplateValidationStatus(clusterTemplate, "", true)

		By("creating ServiceTemplate", func() {
			serviceTemplate = kcmv1.ServiceTemplate{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-service-template-",
					Namespace:    namespace.Name,
				},
				Spec: kcmv1.ServiceTemplateSpec{
					Helm: &kcmv1.HelmSpec{
						ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
							Kind:      "HelmChart",
							Name:      serviceTemplateHelmChart.Name,
							Namespace: namespace.Name,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, &serviceTemplate)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &serviceTemplate)

			serviceTemplate.Status = kcmv1.ServiceTemplateStatus{
				TemplateStatusCommon: kcmv1.TemplateStatusCommon{
					ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
						Kind:      "HelmChart",
						Name:      serviceTemplateHelmChart.Name,
						Namespace: namespace.Name,
					},
					TemplateValidationStatus: kcmv1.TemplateValidationStatus{
						Valid: true,
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, &serviceTemplate)).To(Succeed())
		})

		By("creating CA secret for ClusterAuthentication", func() {
			clAuthCASecret := ensureSecret(namespace.Name, clAuthCASecretName, map[string][]byte{clAuthCASecretKey: clAuthCASecretData})
			DeferCleanup(k8sClient.Delete, clAuthCASecret)
		})

		By("creating DataSource and its secrets", func() {
			dsCASecret := ensureSecret(namespace.Name, dataSourceCASecretName, map[string][]byte{dataSourceCASecretKey: dataSourceCASecretData})
			dsAuthSecret := ensureSecret(namespace.Name, dataSourceAuthSecretName, map[string][]byte{
				dataSourceSecretUsernameKey: []byte(dataSourceSecretUsernameValue),
				dataSourceSecretPasswordKey: []byte(dataSourceSecretPasswordValue),
			})
			DeferCleanup(k8sClient.Delete, dsCASecret)
			DeferCleanup(k8sClient.Delete, dsAuthSecret)

			ds := kcmv1.DataSource{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.Name,
					Name:      dataSourceName,
				},
				Spec: kcmv1.DataSourceSpec{
					CertificateAuthority: newSecretRef(namespace.Name, dataSourceCASecretName, dataSourceCASecretKey),
					Auth: kcmv1.DataSourceAuth{
						Username: *newSecretRef(namespace.Name, dataSourceAuthSecretName, dataSourceSecretUsernameKey),
						Password: *newSecretRef(namespace.Name, dataSourceAuthSecretName, dataSourceSecretPasswordKey),
					},
					Type:      "postgresql",
					Endpoints: dataSourceEndpoints,
				},
			}
			Expect(k8sClient.Create(ctx, &ds)).To(Succeed())
			DeferCleanup(k8sClient.Delete, &ds)
		})

		By("creating certificate secrets in system namespace", func() {
			k0sURLCertSecret := ensureSecret(systemNamespace, k0sURLCertSecretName, k0sURLCertSecretData)
			registryCertSecret := ensureSecret(systemNamespace, registryCertSecretName, registryCertSecretData)
			DeferCleanup(k8sClient.Delete, registryCertSecret)
			DeferCleanup(k8sClient.Delete, k0sURLCertSecret)
		})
	})

	DescribeTable(
		"ClusterDeployment Reconciliation",
		func(tc cldTestCase) {
			awsCredential := tc.ensureCredential(namespace.Name)
			DeferCleanup(k8sClient.Delete, awsCredential)

			clAuthName := ""
			if tc.authConfig != nil {
				clAuth := tc.ensureClusterAuthentication(namespace.Name)
				DeferCleanup(k8sClient.Delete, clAuth)
				clAuthName = clAuth.Name
			}

			auditPolicyName := ""
			if tc.auditPolicy != nil {
				clAuditPolicy := tc.ensureClusterAuditPolicy(namespace.Name)
				DeferCleanup(k8sClient.Delete, clAuditPolicy)
				auditPolicyName = clAuditPolicy.Name
			}

			cld := tc.ensureClusterDeployment(namespace.Name, clusterTemplate.Name, awsCredential.Name, clAuthName, auditPolicyName)
			DeferCleanup(func() error { return crclient.IgnoreNotFound(k8sClient.Delete(ctx, cld)) })

			cldName := types.NamespacedName{Namespace: namespace.Name, Name: cld.Name}

			reconciler = tc.newTestClusterDeploymentReconciler()
			tc.testClusterDeploymentReconciliation(reconciler, cldName)

			deleteClusterDeployment(cldName)
			tc.testClusterDeploymentCleanup(reconciler, cldName)
		},
		Entry("ClusterDeployment is in DryRun mode", cldTestCase{
			dryRun:                 true,
			k0sURLCertSecretName:   k0sURLCertSecretName,
			registryCertSecretName: registryCertSecretName,
		}),
		Entry("ClusterDeployment with clusterAuth and dataSource and no config", cldTestCase{
			registryCertSecretName: registryCertSecretName,
			k0sURLCertSecretName:   k0sURLCertSecretName,
			authConfig:             authConfiguration,
			dataSource:             dataSourceName,
		}),
		Entry("ClusterDeployment with anonymous authentication config", cldTestCase{
			authConfig: anonAuthConfiguration,
			config: map[string]any{
				"customField1": map[string]any{
					"customField2": "customValue2",
				},
			},
		}),
		Entry("ClusterDeployment with registry, k0s URL with certs, auth, dataSource configuration and custom config", cldTestCase{
			globalK0sURL:           "https://custom-k0s-url.example.com",
			globalRegistry:         "custom-registry.example.com",
			registryCertSecretName: registryCertSecretName,
			k0sURLCertSecretName:   k0sURLCertSecretName,
			authConfig:             authConfiguration,
			dataSource:             dataSourceName,
			config: map[string]any{
				"customField1": map[string]any{
					"customField2": "customValue2",
				},
			},
		}),
		Entry("ClusterDeployment in region", cldTestCase{
			region: regionName,
		}),
		Entry("ClusterDeployment in region with registry, k0s URL with certs, auth, dataSource configuration and custom config", cldTestCase{
			region:                 regionName,
			globalK0sURL:           "https://custom-k0s-url.example.com",
			globalRegistry:         "custom-registry.example.com",
			registryCertSecretName: registryCertSecretName,
			k0sURLCertSecretName:   k0sURLCertSecretName,
			authConfig:             anonAuthConfiguration,
			dataSource:             dataSourceName,
			config: map[string]any{
				"customField1": map[string]any{
					"customField2": "customValue2",
				},
				"customField3": "customValue3",
			},
		}),
		Entry("ClusterDeployment with custom audit policy", cldTestCase{
			auditPolicy: customAuditPolicy(),
			authConfig:  authConfiguration,
			config: map[string]any{
				"customField1": "customValue1",
			},
		}),
	)

	// Regression test: setServicesCondition used to increment readyServices for
	// every ServiceState found, instead of at most once per (namespace, name).
	// When the same Service is owned by both a CD-owned ServiceSet and an
	// MCS-owned ServiceSet - which is legitimate and explicitly handled by the
	// surrounding logic - both reporting Deployed would push readyServices
	// above totalServices (the user-reported "9/5" symptom).
	//
	// We seed two ServiceSets pointing at the same cluster, each carrying the
	// same Service entry in Deployed state, then drive setServicesCondition
	// directly and assert the message is "1/1".
	It("ServicesInReadyState must not double-count services duplicated across ServiceSets", func() {
		const clusterName = "test-cd-dedup"
		serviceSetCommon := func(suffix, mcs string) *kcmv1.ServiceSet {
			return &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName + "-" + suffix,
					Namespace: namespace.Name,
				},
				Spec: kcmv1.ServiceSetSpec{
					Cluster:             clusterName,
					MultiClusterService: mcs,
				},
			}
		}
		deployedState := []kcmv1.ServiceState{
			{
				Type:                    "Helm",
				LastStateTransitionTime: &metav1.Time{Time: time.Now()},
				Namespace:               "ns-shared",
				Name:                    "svc-shared",
				Template:                "tmpl-shared",
				State:                   kcmv1.ServiceStateDeployed,
			},
		}

		By("creating a CD-owned ServiceSet reporting svc-shared as Deployed", func() {
			ssCD := serviceSetCommon("cd", "")
			Expect(k8sClient.Create(ctx, ssCD)).To(Succeed())
			DeferCleanup(func() error { return crclient.IgnoreNotFound(k8sClient.Delete(ctx, ssCD)) })
			ssCD.Status.Services = deployedState
			Expect(k8sClient.Status().Update(ctx, ssCD)).To(Succeed())
		})

		By("creating an MCS-owned ServiceSet reporting the same svc-shared as Deployed", func() {
			ssMCS := serviceSetCommon("mcs", "owning-mcs")
			Expect(k8sClient.Create(ctx, ssMCS)).To(Succeed())
			DeferCleanup(func() error { return crclient.IgnoreNotFound(k8sClient.Delete(ctx, ssMCS)) })
			ssMCS.Status.Services = deployedState
			Expect(k8sClient.Status().Update(ctx, ssMCS)).To(Succeed())
		})

		By("waiting for the cached client to observe both ServiceSets via the cluster index", func() {
			Eventually(func(g Gomega) {
				list := &kcmv1.ServiceSetList{}
				g.Expect(mgrClient.List(ctx, list, crclient.MatchingFields{kcmv1.ServiceSetClusterIndexKey: clusterName})).To(Succeed())
				g.Expect(list.Items).To(HaveLen(2))
				for _, ss := range list.Items {
					g.Expect(ss.Status.Services).To(HaveLen(1))
					g.Expect(ss.Status.Services[0].State).To(Equal(kcmv1.ServiceStateDeployed))
				}
			}).Should(Succeed())
		})

		By("running setServicesCondition and asserting ready does not exceed total", func() {
			ssCD := serviceSetCommon("cd", "")
			ssCD.Status.Services = deployedState
			cd := &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace.Name,
				},
				Spec: kcmv1.ClusterDeploymentSpec{
					ServiceSpec: kcmv1.ServiceSpec{
						Services: []kcmv1.Service{
							{Namespace: "ns-shared", Name: "svc-shared", Template: "tmpl-shared"},
						},
					},
				},
			}

			r := &ClusterDeploymentReconciler{MgmtClient: mgrClient}
			r.setServicesCondition(cd, []kcmv1.ServiceSet{*ssCD})

			Expect(cd.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", kcmv1.ServicesInReadyStateCondition),
				HaveField("Status", metav1.ConditionTrue),
				HaveField("Reason", kcmv1.SucceededReason),
				HaveField("Message", "1/1"),
			)))
		})
	})
})

// deleteClusterDeployment triggers deletion and waits for DeletionTimestamp to appear.
func deleteClusterDeployment(cldName types.NamespacedName) {
	GinkgoHelper()

	By("deleting the ClusterDeployment", func() {
		cld := &kcmv1.ClusterDeployment{}
		err := k8sClient.Get(ctx, cldName, cld)
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Delete(ctx, cld)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(mgrClient.Get(ctx, cldName, cld)).To(Succeed())
			g.Expect(cld.DeletionTimestamp).NotTo(BeNil())
		}).Should(Succeed())
	})
}

// ensureSecret creates a Secret with the given data.
func ensureSecret(namespace, name string, data map[string][]byte) *corev1.Secret {
	GinkgoHelper()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Data: data,
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	return secret
}

// newSecretRef builds a SecretKeyReference pointing to the given namespace/name/key.
func newSecretRef(namespace, name, key string) *kcmv1.SecretKeyReference {
	return &kcmv1.SecretKeyReference{
		SecretReference: corev1.SecretReference{
			Namespace: namespace,
			Name:      name,
		},
		Key: key,
	}
}

// ensureClusterTemplate creates a ClusterTemplate with the given HelmChart reference.
// Returns the created cluster template with status populated.
func ensureClusterTemplate(namespace, helmChartName string) *kcmv1.ClusterTemplate {
	GinkgoHelper()

	ct := &kcmv1.ClusterTemplate{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-cluster-template-",
			Namespace:    namespace,
		},
		Spec: kcmv1.ClusterTemplateSpec{
			Helm: kcmv1.HelmSpec{
				ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
					Kind:      "HelmChart",
					Name:      helmChartName,
					Namespace: namespace,
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, ct)).To(Succeed())

	const exposedProviderName = "test-provider-name"

	ct.Status = kcmv1.ClusterTemplateStatus{
		KubernetesVersion: kubernetesVersion,
		TemplateStatusCommon: kcmv1.TemplateStatusCommon{
			ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind:      "HelmChart",
				Name:      helmChartName,
				Namespace: namespace,
			},
		},
		Providers: kcmv1.Providers{exposedProviderName},
	}
	Expect(k8sClient.Status().Update(ctx, ct)).To(Succeed())

	Eventually(func(g Gomega) {
		g.Expect(mgrClient.Get(ctx, crclient.ObjectKeyFromObject(ct), ct)).To(Succeed())
		g.Expect(ct.Status.Providers).To(ContainElement(exposedProviderName))
		g.Expect(ct.Status.ChartRef).NotTo(BeNil())
	}).Should(Succeed())

	return ct
}

// setClusterTemplateValidationStatus updates the template's validation status
// and waits for the change to be observable.
func setClusterTemplateValidationStatus(ct *kcmv1.ClusterTemplate, errorMsg string, valid bool) {
	GinkgoHelper()

	By(fmt.Sprintf("setting ClusterTemplate validation status to valid=%t", valid), func() {
		Expect(k8sClient.Get(ctx, crclient.ObjectKeyFromObject(ct), ct)).To(Succeed())
		ct.Status.TemplateValidationStatus = kcmv1.TemplateValidationStatus{
			Valid:           valid,
			ValidationError: errorMsg,
		}
		Expect(k8sClient.Status().Update(ctx, ct)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(mgrClient.Get(ctx, crclient.ObjectKeyFromObject(ct), ct)).To(Succeed())
			g.Expect(ct.Status.Valid).To(Equal(valid))
			if errorMsg != "" {
				g.Expect(ct.Status.ValidationError).To(ContainSubstring(errorMsg))
			}
		}).Should(Succeed())
	})
}

// setCredentialReadyStatus updates the credential's ready status
// and waits for the change to be observable.
func setCredentialReadyStatus(cred *kcmv1.Credential, ready bool) {
	GinkgoHelper()

	By(fmt.Sprintf("setting Credential readiness status to %t", ready), func() {
		Expect(k8sClient.Get(ctx, crclient.ObjectKeyFromObject(cred), cred)).To(Succeed())
		cred.Status.Ready = ready
		Expect(k8sClient.Status().Update(ctx, cred)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(mgrClient.Get(ctx, crclient.ObjectKeyFromObject(cred), cred)).To(Succeed())
			g.Expect(cred.Status.Ready).To(Equal(ready))
		}).Should(Succeed())
	})
}

// buildExpectedAuthValues constructs the expected "auth" section of Helm values
// for the given auth configuration.
func buildExpectedAuthValues(cldName string, clAuthSpec *kcmv1.ClusterAuthenticationSpec) map[string]any {
	GinkgoHelper()

	authCfg := clAuthSpec.GetAuthConfig()

	authConfCopy := authCfg.DeepCopy()
	for i := range authConfCopy.JWT {
		authConfCopy.JWT[i].Issuer.CertificateAuthority = string(clAuthCASecretData)
	}
	authData, err := yaml.Marshal(authConfCopy)
	Expect(err).NotTo(HaveOccurred())

	hash := sha256.Sum256(authData)
	val := map[string]any{
		"configSecret": map[string]any{
			"hash": hex.EncodeToString(hash[:4]),
			"key":  authConfigSecretKey,
			"name": cldName + "-auth-config",
		},
	}

	if authCfg.Anonymous != nil {
		val["configWithAnon"] = true
	}

	return val
}

// getCAPIClusterReadyConditions returns a set of CAPI Cluster conditions all
// marked as True/Succeeded, simulating a fully healthy cluster.
func getCAPIClusterReadyConditions() []metav1.Condition {
	conditionTypes := []string{
		clusterapiv1.ClusterInfrastructureReadyCondition,
		clusterapiv1.ClusterControlPlaneInitializedCondition,
		clusterapiv1.ClusterControlPlaneAvailableCondition,
		clusterapiv1.ClusterControlPlaneMachinesReadyCondition,
		clusterapiv1.ClusterWorkersAvailableCondition,
		clusterapiv1.ClusterWorkerMachinesReadyCondition,
		clusterapiv1.ClusterRemoteConnectionProbeCondition,
		clusterapiv1.ReadyCondition,
	}

	conditions := make([]metav1.Condition, 0, len(conditionTypes))
	now := metav1.Now()
	for _, ct := range conditionTypes {
		conditions = append(conditions, metav1.Condition{
			Type:               ct,
			Status:             metav1.ConditionTrue,
			Reason:             "Succeeded",
			Message:            "Condition is Ready",
			LastTransitionTime: now,
		})
	}
	return conditions
}

// buildKubeconfigFromRestConfig creates kubeconfig bytes from rest.Config.
// This is used to build a "regional" kubeconfig that points back to the same envtest API server.
func buildKubeconfigFromRestConfig(restCfg *rest.Config) []byte {
	GinkgoHelper()
	const contextName = "test-regional"

	kubeconfig := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			contextName: {
				Server:                   restCfg.Host,
				CertificateAuthorityData: restCfg.CAData,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			contextName: {
				ClientCertificateData: restCfg.CertData,
				ClientKeyData:         restCfg.KeyData,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			contextName: {
				Cluster:  contextName,
				AuthInfo: contextName,
			},
		},
		CurrentContext: contextName,
	}

	data, err := clientcmd.Write(kubeconfig)
	Expect(err).NotTo(HaveOccurred())
	return data
}

func Test_updateClusterDeploymentConditions(t *testing.T) {
	const generation = 2
	type testInput struct {
		name                   string
		currentConditions      []metav1.Condition
		expectedReadyCondition metav1.Condition
	}
	tests := []testInput{
		{
			name: "No conditions exist; should add Ready condition with Unknown status.",
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionUnknown,
				Reason:             kcmv1.ProgressingReason,
				ObservedGeneration: generation,
			},
		},
		{
			name: "Some conditions exist with Unknown status; should add Ready condition with Unknown status.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.CredentialReadyCondition,
					Status:  metav1.ConditionUnknown,
					Reason:  "CredentialStatusUnknown",
					Message: "Credential status is unknown",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionUnknown,
				Reason:             kcmv1.ProgressingReason,
				Message:            "Credential status is unknown",
				ObservedGeneration: generation,
			},
		},
		{
			name: "Credential and Template are not Ready; should reflect both errors in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.CredentialReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  "SomeCredentialError",
					Message: "Some error with credentials",
				},
				{
					Type:    kcmv1.TemplateReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  "SomeTemplateError",
					Message: "Some error with cluster template",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.FailedReason,
				Message:            "Some error with credentials. Some error with cluster template",
				ObservedGeneration: generation,
			},
		},
		{
			name: "ClusterDataSource and ClusterIdentity are not Ready; should reflect both errors in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.CredentialReadyCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.SucceededReason,
					Message: "Credential is ready",
				},
				{
					Type:    kcmv1.TemplateReadyCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.SucceededReason,
					Message: "ClusterTemplate is ready",
				},
				{
					Type:    kcmv1.ClusterDataSourceReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  kcmv1.FailedReason,
					Message: "Some error with cluster data source",
				},
				{
					Type:    kcmv1.ClusterAuthenticationReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  kcmv1.FailedReason,
					Message: "Some error with cluster authentication",
				},
				{
					Type:               kcmv1.CAPIClusterSummaryCondition,
					Status:             metav1.ConditionFalse,
					Reason:             "IssuesReported",
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-5 * time.Minute)},
					Message:            "InfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.FailedReason,
				Message:            "Some error with cluster data source. Some error with cluster authentication",
				ObservedGeneration: generation,
			},
		},
		{
			name: "CAPI Cluster is provisioning for 5 minutes; should reflect Progressing reason in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.ClusterAuthenticationReadyCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.SucceededReason,
					Message: "ClusterAuthentication is ready",
				},
				{
					Type:               kcmv1.CAPIClusterSummaryCondition,
					Status:             metav1.ConditionFalse,
					Reason:             "IssuesReported",
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-5 * time.Minute)},
					Message:            "InfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.ProgressingReason,
				Message:            "InfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				ObservedGeneration: generation,
			},
		},
		{
			name: "CAPI Cluster is provisioning for more than 30 minutes; should reflect Failed reason in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:               kcmv1.CAPIClusterSummaryCondition,
					Status:             metav1.ConditionFalse,
					Reason:             "IssuesReported",
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-40 * time.Minute)},
					Message:            "InfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.FailedReason,
				Message:            "Cluster is not ready. Check the provider logs for more details.\nInfrastructureReady: OpenStackCluster status.initialization.provisioned is false",
				ObservedGeneration: generation,
			},
		},
		{
			name: "Cluster is ready, should reflect Succeeded reason in Ready condition.",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.CAPIClusterSummaryCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.SucceededReason,
					Message: "Cluster is ready",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionTrue,
				Reason:             kcmv1.SucceededReason,
				Message:            "Object is ready",
				ObservedGeneration: generation,
			},
		},
		{
			name: "Cluster is deleting, Ready condition should be equal to Deleting condition",
			currentConditions: []metav1.Condition{
				{
					Type:    kcmv1.DeletingCondition,
					Status:  metav1.ConditionTrue,
					Reason:  "IssuesReported",
					Message: "Some error with cluster deletion",
				},
				{
					Type:               kcmv1.ReadyCondition,
					Status:             metav1.ConditionUnknown,
					Reason:             kcmv1.ProgressingReason,
					Message:            "Some old Ready condition message",
					ObservedGeneration: 1,
				},
			},
			expectedReadyCondition: metav1.Condition{
				Type:               kcmv1.ReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.DeletingReason,
				Message:            "Some error with cluster deletion",
				ObservedGeneration: generation,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := conditionsutil.UpdateReadyCondition(tt.currentConditions, generation, handleClusterDeploymentFailedConditions)
			checkReadyCondition(t, result, tt.expectedReadyCondition)
		})
	}
}

// checkReadyCondition verifies if the Ready condition in the list of all conditions matches the expected Ready condition.
func checkReadyCondition(t *testing.T, conditions []metav1.Condition, expectedReadyCondition metav1.Condition) {
	t.Helper()

	for _, cond := range conditions {
		if cond.Type == kcmv1.ReadyCondition {
			if cond.Status != expectedReadyCondition.Status || cond.Reason != expectedReadyCondition.Reason ||
				cond.Message != expectedReadyCondition.Message || cond.ObservedGeneration != expectedReadyCondition.ObservedGeneration {
				printCondition := func(c metav1.Condition) string {
					return fmt.Sprintf("{Status: %s, Reason: %s, Message: %s, ObservedGeneration: %d}", c.Status, c.Reason, c.Message, c.ObservedGeneration)
				}
				t.Errorf("Ready condition does not match expected.\nGot: %s,\nWant: %s", printCondition(cond), printCondition(expectedReadyCondition))
			}
			return
		}
	}
	t.Errorf("Ready condition not found")
}

func Test_detectHelmChartNameChange(t *testing.T) {
	const (
		cdName      = "test-cd"
		cdNamespace = "default"
	)

	tests := []struct {
		name                  string
		existingHelmRelease   *helmcontrollerv2.HelmRelease
		templateChartRef      *helmcontrollerv2.CrossNamespaceSourceReference
		clientInterceptor     *interceptor.Funcs
		expectConditionSet    bool
		expectConditionStatus metav1.ConditionStatus
		preExistingCondition  bool // whether to pre-set the HelmChartNameChanged condition
		expectError           bool
	}{
		{
			name:                "no existing HelmRelease, no condition set",
			existingHelmRelease: nil,
			templateChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind: "HelmChart",
				Name: "some-chart",
			},
			expectConditionSet: false,
		},
		{
			name: "chart name unchanged, no condition set",
			existingHelmRelease: &helmcontrollerv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
				Spec: helmcontrollerv2.HelmReleaseSpec{
					ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
						Kind: "HelmChart",
						Name: "same-chart",
					},
				},
			},
			templateChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind: "HelmChart",
				Name: "same-chart",
			},
			expectConditionSet: false,
		},
		{
			name: "chart name changed, condition should be set",
			existingHelmRelease: &helmcontrollerv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
				Spec: helmcontrollerv2.HelmReleaseSpec{
					ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
						Kind: "HelmChart",
						Name: "old-chart",
					},
				},
			},
			templateChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind: "HelmChart",
				Name: "new-chart",
			},
			expectConditionSet:    true,
			expectConditionStatus: metav1.ConditionTrue,
		},
		{
			name: "existing HelmRelease has nil ChartRef, no condition set",
			existingHelmRelease: &helmcontrollerv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
				Spec: helmcontrollerv2.HelmReleaseSpec{},
			},
			templateChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind: "HelmChart",
				Name: "new-chart",
			},
			expectConditionSet: false,
		},
		{
			name:             "template has nil ChartRef, method returns early",
			templateChartRef: nil,
			existingHelmRelease: &helmcontrollerv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
				Spec: helmcontrollerv2.HelmReleaseSpec{
					ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
						Kind: "HelmChart",
						Name: "old-chart",
					},
				},
			},
			expectConditionSet: false,
		},
		{
			name: "chart name back to same, pre-existing condition should be removed",
			existingHelmRelease: &helmcontrollerv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
				Spec: helmcontrollerv2.HelmReleaseSpec{
					ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
						Kind: "HelmChart",
						Name: "same-chart",
					},
				},
			},
			templateChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind: "HelmChart",
				Name: "same-chart",
			},
			preExistingCondition: true,
			expectConditionSet:   false,
		},
		{
			name: "transient Get error should return error",
			clientInterceptor: &interceptor.Funcs{
				Get: func(_ context.Context, _ crclient.WithWatch, _ crclient.ObjectKey, obj crclient.Object, _ ...crclient.GetOption) error {
					if _, ok := obj.(*helmcontrollerv2.HelmRelease); ok {
						return errors.New("transient API server error")
					}
					return nil
				},
			},
			templateChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind: "HelmChart",
				Name: "some-chart",
			},
			expectConditionSet: false,
			expectError:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var existingObjects []runtime.Object
			if tt.existingHelmRelease != nil {
				existingObjects = append(existingObjects, tt.existingHelmRelease)
			}

			clientBuilder := fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithRuntimeObjects(existingObjects...)
			if tt.clientInterceptor != nil {
				clientBuilder = clientBuilder.WithInterceptorFuncs(*tt.clientInterceptor)
			}
			c := clientBuilder.Build()

			cd := &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
			}

			if tt.preExistingCondition {
				meta.SetStatusCondition(&cd.Status.Conditions, metav1.Condition{
					Type:    kcmv1.HelmChartNameChangedCondition,
					Status:  metav1.ConditionTrue,
					Reason:  kcmv1.HelmChartNameChangedReason,
					Message: "previously set",
				})
			}

			clusterTpl := &kcmv1.ClusterTemplate{
				Status: kcmv1.ClusterTemplateStatus{
					TemplateStatusCommon: kcmv1.TemplateStatusCommon{
						ChartRef: tt.templateChartRef,
					},
				},
			}

			r := &ClusterDeploymentReconciler{
				MgmtClient: c,
			}

			err := r.detectHelmChartNameChange(t.Context(), cd, clusterTpl)
			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			cond := meta.FindStatusCondition(cd.Status.Conditions, kcmv1.HelmChartNameChangedCondition)
			if tt.expectConditionSet {
				if cond == nil {
					t.Fatalf("expected %s condition to be set, but it was not", kcmv1.HelmChartNameChangedCondition)
				}
				if cond.Status != tt.expectConditionStatus {
					t.Errorf("expected condition status %s, got %s", tt.expectConditionStatus, cond.Status)
				}
				if cond.Reason != kcmv1.HelmChartNameChangedReason {
					t.Errorf("expected condition reason %s, got %s", kcmv1.HelmChartNameChangedReason, cond.Reason)
				}
			} else if cond != nil {
				t.Errorf("expected %s condition to not be set, but got: %+v", kcmv1.HelmChartNameChangedCondition, cond)
			}
		})
	}
}

func Test_fillClusterAuditPolicyValues(t *testing.T) {
	cdName := "test-cd"
	r := &ClusterDeploymentReconciler{}

	tests := []struct {
		name           string
		scope          *clusterScope
		values         map[string]any
		expectedValues map[string]any
	}{
		{
			name: "audit nil - policyRef removed but other audit values preserved",
			scope: &clusterScope{
				audit: nil,
				cd:    &kcmv1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{Name: cdName}},
			},
			values: map[string]any{
				"audit": map[string]any{
					"policyRef": map[string]any{"name": "old", "key": "old", "hash": "old"},
					"logPath":   "/var/log/audit",
				},
			},
			expectedValues: map[string]any{
				"audit": map[string]any{
					"logPath": "/var/log/audit",
				},
			},
		},
		{
			name: "audit.policy nil - policyRef removed but other audit values preserved",
			scope: &clusterScope{
				audit: &auditConfig{policy: nil},
				cd:    &kcmv1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{Name: cdName}},
			},
			values: map[string]any{
				"audit": map[string]any{
					"policyRef": map[string]any{"name": "old"},
					"logPath":   "-",
				},
			},
			expectedValues: map[string]any{
				"audit": map[string]any{
					"logPath": "-",
				},
			},
		},
		{
			name: "audit nil - no audit key in values, nothing happens",
			scope: &clusterScope{
				audit: nil,
				cd:    &kcmv1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{Name: cdName}},
			},
			values:         map[string]any{"foo": "bar"},
			expectedValues: map[string]any{"foo": "bar"},
		},
		{
			name: "audit with policy - policyRef is set, existing audit values preserved",
			scope: &clusterScope{
				audit: &auditConfig{policy: &auditv1.Policy{}, hash: "abc123"},
				cd:    &kcmv1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{Name: cdName}},
			},
			values: map[string]any{
				"audit": map[string]any{
					"logPath": "/var/log/audit.log",
				},
			},
			expectedValues: map[string]any{
				"audit": map[string]any{
					"logPath": "/var/log/audit.log",
					"policyRef": map[string]any{
						"name": cdName + "-audit-policy",
						"key":  "policy",
						"hash": "abc123",
					},
				},
			},
		},
		{
			name: "audit with policy - no existing audit key, audit map created",
			scope: &clusterScope{
				audit: &auditConfig{policy: &auditv1.Policy{}, hash: "def456"},
				cd:    &kcmv1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{Name: cdName}},
			},
			values: map[string]any{},
			expectedValues: map[string]any{
				"audit": map[string]any{
					"policyRef": map[string]any{
						"name": cdName + "-audit-policy",
						"key":  "policy",
						"hash": "def456",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r.fillClusterAuditPolicyValues(tt.scope, tt.values)
			if !reflect.DeepEqual(tt.values, tt.expectedValues) {
				t.Errorf("expected values %v, got %v", tt.expectedValues, tt.values)
			}
		})
	}
}

func Test_getClusterScope(t *testing.T) {
	const (
		namespace = "test-ns"
		credName  = "test-cred"
		auditName = "test-audit-policy"
		authName  = "test-auth"
		dsName    = "test-datasource"
	)

	baseCred := &kcmv1.Credential{
		ObjectMeta: metav1.ObjectMeta{
			Name:      credName,
			Namespace: namespace,
		},
		Spec: kcmv1.CredentialSpec{
			IdentityRef: &corev1.ObjectReference{
				APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
				Kind:       "AWSClusterStaticIdentity",
				Name:       "foo",
			},
		},
	}

	validAuditPolicy := &kcmv1.ClusterAuditPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      auditName,
			Namespace: namespace,
		},
		Spec: *customAuditPolicy(),
	}

	invalidAuditPolicy := &kcmv1.ClusterAuditPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      auditName,
			Namespace: namespace,
		},
		Spec: kcmv1.ClusterAuditPolicySpec{
			Policy: kcmv1.Policy{
				Rules: []auditv1.PolicyRule{
					{
						Level: "wrong",
					},
				},
			},
		},
	}

	clusterAuth := &kcmv1.ClusterAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authName,
			Namespace: namespace,
		},
		Spec: *authConfiguration,
	}

	invalidClusterAuth := &kcmv1.ClusterAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authName,
			Namespace: namespace,
		},
		Spec: *invalidAuthConfiguration,
	}

	baseDataSource := &kcmv1.DataSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dsName,
			Namespace: namespace,
		},
	}

	tests := []struct {
		name                   string
		cd                     *kcmv1.ClusterDeployment
		objects                []crclient.Object
		isDisabledValidationWH bool
		expectErrMsg           string
		expectConditionType    string
		expectConditionStatus  metav1.ConditionStatus
	}{
		{
			name: "missing credential sets CredentialReady=False and persists status",
			cd: &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cd", Namespace: namespace},
				Spec:       kcmv1.ClusterDeploymentSpec{Credential: credName},
			},
			objects:               nil,
			expectErrMsg:          fmt.Sprintf("failed to get Credential %s/%s: credentials.k0rdent.mirantis.com \"%s\" not found", namespace, credName, credName),
			expectConditionType:   kcmv1.CredentialReadyCondition,
			expectConditionStatus: metav1.ConditionFalse,
		},
		{
			name: "missing DataSource sets DataSourceReady=False and persists status",
			cd: &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cd", Namespace: namespace},
				Spec: kcmv1.ClusterDeploymentSpec{
					Credential: credName,
					DataSource: dsName,
				},
			},
			objects:               []crclient.Object{baseCred},
			expectErrMsg:          fmt.Sprintf("failed to get DataSource %s/%s: datasources.k0rdent.mirantis.com \"%s\" not found", namespace, dsName, dsName),
			expectConditionType:   kcmv1.DataSourceReadyCondition,
			expectConditionStatus: metav1.ConditionFalse,
		},
		{
			name: "missing ClusterAuthentication sets ClusterAuthenticationReady=False and persists status",
			cd: &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cd", Namespace: namespace},
				Spec: kcmv1.ClusterDeploymentSpec{
					Credential:  credName,
					ClusterAuth: authName,
				},
			},
			objects:               []crclient.Object{baseCred},
			expectErrMsg:          fmt.Sprintf("failed to get ClusterAuthentication %s/%s: clusterauthentications.k0rdent.mirantis.com \"%s\" not found", namespace, authName, authName),
			expectConditionType:   kcmv1.ClusterAuthenticationReadyCondition,
			expectConditionStatus: metav1.ConditionFalse,
		},
		{
			name: "invalid ClusterAuthentication with disabled webhook sets ClusterAuthentication=False and returns errNoRetrigger",
			cd: &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cd", Namespace: namespace},
				Spec: kcmv1.ClusterDeploymentSpec{
					Credential:  credName,
					ClusterAuth: authName,
				},
			},
			objects:                []crclient.Object{baseCred, invalidClusterAuth},
			isDisabledValidationWH: true,
			expectErrMsg:           "validation failed with webhooks disabled, will not retrigger",
			expectConditionType:    kcmv1.ClusterAuthenticationReadyCondition,
			expectConditionStatus:  metav1.ConditionFalse,
		},
		{
			name: "missing ClusterAuditPolicy sets ClusterAuditPolicyReady=False and persists status",
			cd: &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cd", Namespace: namespace},
				Spec: kcmv1.ClusterDeploymentSpec{
					Credential:  credName,
					AuditPolicy: auditName,
				},
			},
			objects:               []crclient.Object{baseCred},
			expectErrMsg:          fmt.Sprintf("failed to get ClusterAuditPolicy %s/%s: clusterauditpolicies.k0rdent.mirantis.com \"%s\" not found", namespace, auditName, auditName),
			expectConditionType:   kcmv1.ClusterAuditPolicyReadyCondition,
			expectConditionStatus: metav1.ConditionFalse,
		},
		{
			name: "invalid ClusterAuditPolicy with disabled webhook sets ClusterAuditPolicyReady=False and returns errNoRetrigger",
			cd: &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cd", Namespace: namespace},
				Spec: kcmv1.ClusterDeploymentSpec{
					Credential:  credName,
					AuditPolicy: auditName,
				},
			},
			objects:                []crclient.Object{baseCred, invalidAuditPolicy},
			isDisabledValidationWH: true,
			expectErrMsg:           "validation failed with webhooks disabled, will not retrigger",
			expectConditionType:    kcmv1.ClusterAuditPolicyReadyCondition,
			expectConditionStatus:  metav1.ConditionFalse,
		},
		{
			name: "all references exist returns scope successfully",
			cd: &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cd", Namespace: namespace},
				Spec: kcmv1.ClusterDeploymentSpec{
					Credential:  credName,
					DataSource:  dsName,
					ClusterAuth: authName,
					AuditPolicy: auditName,
				},
			},
			objects: []crclient.Object{baseCred, baseDataSource, clusterAuth, validAuditPolicy},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]crclient.Object, 0, len(tt.objects)+1)
			objects = append(objects, tt.cd.DeepCopy())
			objects = append(objects, tt.objects...)

			c := fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithObjects(objects...).
				WithStatusSubresource(&kcmv1.ClusterDeployment{}).
				Build()

			r := &ClusterDeploymentReconciler{
				MgmtClient:             c,
				IsDisabledValidationWH: tt.isDisabledValidationWH,
			}

			cd := &kcmv1.ClusterDeployment{}
			if err := c.Get(context.Background(), crclient.ObjectKeyFromObject(tt.cd), cd); err != nil {
				t.Fatalf("failed to get ClusterDeployment: %v", err)
			}

			scope, err := r.getClusterScope(ctx, cd)

			if tt.expectErrMsg != "" {
				if err.Error() != tt.expectErrMsg {
					t.Fatalf("expected error message '%s', got: '%s'", tt.expectErrMsg, err.Error())
				}

				cond := meta.FindStatusCondition(cd.Status.Conditions, tt.expectConditionType)
				if cond == nil {
					t.Fatalf("expected condition %s to be persisted, but it was not found", tt.expectConditionType)
				}
				if cond.Status != tt.expectConditionStatus {
					t.Errorf("expected condition status %s, got %s", tt.expectConditionStatus, cond.Status)
				}
				if scope != nil {
					t.Error("expected nil scope on error")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if scope == nil {
					t.Fatal("expected non-nil scope")
				}
				if scope.cd != cd {
					t.Error("scope.cd does not match input ClusterDeployment")
				}
			}
		})
	}
}

func Test_ensureAuthConfigSecret(t *testing.T) {
	const (
		cdName      = "test-cd"
		cdNamespace = "default"
		secretName  = cdName + "-auth-config"
	)

	caSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clAuthCASecretName,
			Namespace: cdNamespace,
		},
		Data: map[string][]byte{clAuthCASecretKey: clAuthCASecretData},
	}

	clAuth := &kcmv1.ClusterAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-auth",
			Namespace: cdNamespace,
		},
		Spec: *authConfiguration,
	}
	clAuth.Spec.CASecret = &kcmv1.SecretKeyReference{
		SecretReference: corev1.SecretReference{
			Namespace: cdNamespace,
			Name:      clAuthCASecretName,
		},
		Key: clAuthCASecretKey,
	}

	tests := []struct {
		name                  string
		auth                  *authConfig
		existingObjects       []crclient.Object
		preConditions         []metav1.Condition
		expectError           bool
		expectErrorContains   string
		expectSecretExists    bool
		expectConditionExists bool
	}{
		{
			name:                  "no auth config, no condition - no-op",
			auth:                  nil,
			expectSecretExists:    false,
			expectConditionExists: false,
		},
		{
			name: "no auth config, condition exists, no secret - removes condition only",
			auth: nil,
			preConditions: []metav1.Condition{
				{
					Type:   kcmv1.ClusterAuthenticationReadyCondition,
					Status: metav1.ConditionTrue,
					Reason: kcmv1.SucceededReason,
				},
			},
			expectSecretExists:    false,
			expectConditionExists: false,
		},
		{
			name: "no auth config, condition exists, secret exists - deletes secret and removes condition",
			auth: nil,
			existingObjects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secretName,
						Namespace: cdNamespace,
					},
					Data: map[string][]byte{authConfigSecretKey: []byte("old-data")},
				},
			},
			preConditions: []metav1.Condition{
				{
					Type:   kcmv1.ClusterAuthenticationReadyCondition,
					Status: metav1.ConditionTrue,
					Reason: kcmv1.SucceededReason,
				},
			},
			expectSecretExists:    false,
			expectConditionExists: false,
		},
		{
			name: "auth config present - creates secret and sets condition",
			auth: &authConfig{clAuth: clAuth},
			existingObjects: []crclient.Object{
				caSecret,
			},
			expectSecretExists:    true,
			expectConditionExists: true,
		},
		{
			name:                  "auth with nil clAuth - no-op (no condition exists)",
			auth:                  &authConfig{clAuth: nil},
			expectSecretExists:    false,
			expectConditionExists: false,
		},
		{
			name: "auth with nil AuthenticationConfiguration spec - condition exists, deletes secret",
			auth: &authConfig{
				clAuth: &kcmv1.ClusterAuthentication{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-auth",
						Namespace: cdNamespace,
					},
					Spec: kcmv1.ClusterAuthenticationSpec{
						AuthenticationConfiguration: nil,
					},
				},
			},
			preConditions: []metav1.Condition{
				{
					Type:   kcmv1.ClusterAuthenticationReadyCondition,
					Status: metav1.ConditionTrue,
					Reason: kcmv1.SucceededReason,
				},
			},
			expectSecretExists:    false,
			expectConditionExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cd := &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
			}
			if len(tt.preConditions) > 0 {
				cd.Status.Conditions = tt.preConditions
			}

			objects := make([]crclient.Object, 0, len(tt.existingObjects))
			objects = append(objects, tt.existingObjects...)

			c := fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithObjects(objects...).
				Build()

			r := &ClusterDeploymentReconciler{
				MgmtClient: c,
			}

			scope := &clusterScope{
				cd:        cd,
				auth:      tt.auth,
				rgnClient: c,
			}

			err := r.ensureAuthConfigSecret(t.Context(), scope)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.expectErrorContains != "" && !strings.Contains(err.Error(), tt.expectErrorContains) {
					t.Errorf("expected error containing %q, got: %v", tt.expectErrorContains, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check secret existence
			secret := &corev1.Secret{}
			secretErr := c.Get(t.Context(), crclient.ObjectKey{Name: secretName, Namespace: cdNamespace}, secret)
			if tt.expectSecretExists {
				if secretErr != nil {
					t.Fatalf("expected secret %s to exist, but got error: %v", secretName, secretErr)
				}
				if _, ok := secret.Data[authConfigSecretKey]; !ok {
					t.Error("expected secret to contain auth config data key")
				}
			} else {
				if !apierrors.IsNotFound(secretErr) && secretErr != nil {
					t.Fatalf("unexpected error checking secret: %v", secretErr)
				}
				if secretErr == nil {
					t.Errorf("expected secret %s to not exist, but it does", secretName)
				}
			}

			// Check condition
			cond := meta.FindStatusCondition(cd.Status.Conditions, kcmv1.ClusterAuthenticationReadyCondition)
			if tt.expectConditionExists {
				if cond == nil {
					t.Fatal("expected ClusterAuthenticationReadyCondition to exist")
				}
				if cond.Status != metav1.ConditionTrue {
					t.Errorf("expected condition status True, got %s", cond.Status)
				}
			} else if cond != nil {
				t.Errorf("expected ClusterAuthenticationReadyCondition to not exist, but found: %+v", cond)
			}
		})
	}
}

func Test_ensureAuditPolicyConfigMap(t *testing.T) {
	const (
		cdName      = "test-cd"
		cdNamespace = "default"
		cmName      = cdName + "-audit-policy"
	)

	auditPolicy := customAuditPolicy().GetPolicy()

	tests := []struct {
		name                  string
		audit                 *auditConfig
		existingObjects       []crclient.Object
		preConditions         []metav1.Condition
		expectError           bool
		expectErrorContains   string
		expectConfigMapExists bool
		expectConditionExists bool
	}{
		{
			name:                  "no audit config, no condition - no-op",
			audit:                 nil,
			expectConfigMapExists: false,
			expectConditionExists: false,
		},
		{
			name:  "no audit config, condition exists, no configmap - removes condition only",
			audit: nil,
			preConditions: []metav1.Condition{
				{
					Type:   kcmv1.ClusterAuditPolicyReadyCondition,
					Status: metav1.ConditionTrue,
					Reason: kcmv1.SucceededReason,
				},
			},
			expectConfigMapExists: false,
			expectConditionExists: false,
		},
		{
			name:  "no audit config, condition exists, configmap exists - deletes configmap and removes condition",
			audit: nil,
			existingObjects: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      cmName,
						Namespace: cdNamespace,
					},
					Data: map[string]string{auditPolicyConfigKey: "old-policy"},
				},
			},
			preConditions: []metav1.Condition{
				{
					Type:   kcmv1.ClusterAuditPolicyReadyCondition,
					Status: metav1.ConditionTrue,
					Reason: kcmv1.SucceededReason,
				},
			},
			expectConfigMapExists: false,
			expectConditionExists: false,
		},
		{
			name: "audit config present - creates configmap and sets condition",
			audit: &auditConfig{
				policy: auditPolicy,
			},
			expectConfigMapExists: true,
			expectConditionExists: true,
		},
		{
			name: "audit config with nil policy - condition exists, deletes configmap",
			audit: &auditConfig{
				policy: nil,
			},
			preConditions: []metav1.Condition{
				{
					Type:   kcmv1.ClusterAuditPolicyReadyCondition,
					Status: metav1.ConditionTrue,
					Reason: kcmv1.SucceededReason,
				},
			},
			expectConfigMapExists: false,
			expectConditionExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cd := &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cdName,
					Namespace: cdNamespace,
				},
			}
			if len(tt.preConditions) > 0 {
				cd.Status.Conditions = tt.preConditions
			}

			objects := make([]crclient.Object, 0, len(tt.existingObjects))
			objects = append(objects, tt.existingObjects...)

			c := fake.NewClientBuilder().
				WithScheme(testscheme.Scheme).
				WithObjects(objects...).
				Build()

			r := &ClusterDeploymentReconciler{
				MgmtClient: c,
			}

			scope := &clusterScope{
				cd:        cd,
				audit:     tt.audit,
				rgnClient: c,
			}

			err := r.ensureAuditPolicyConfigMap(t.Context(), scope)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.expectErrorContains != "" && !strings.Contains(err.Error(), tt.expectErrorContains) {
					t.Errorf("expected error containing %q, got: %v", tt.expectErrorContains, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check configmap existence
			cm := &corev1.ConfigMap{}
			cmErr := c.Get(t.Context(), crclient.ObjectKey{Name: cmName, Namespace: cdNamespace}, cm)
			if tt.expectConfigMapExists {
				if cmErr != nil {
					t.Fatalf("expected configmap %s to exist, but got error: %v", cmName, cmErr)
				}
				if _, ok := cm.Data[auditPolicyConfigKey]; !ok {
					t.Error("expected configmap to contain audit policy data key")
				}
			} else {
				if !apierrors.IsNotFound(cmErr) && cmErr != nil {
					t.Fatalf("unexpected error checking configmap: %v", cmErr)
				}
				if cmErr == nil {
					t.Errorf("expected configmap %s to not exist, but it does", cmName)
				}
			}

			// Check condition
			cond := meta.FindStatusCondition(cd.Status.Conditions, kcmv1.ClusterAuditPolicyReadyCondition)
			if tt.expectConditionExists {
				if cond == nil {
					t.Fatal("expected ClusterAuditPolicyReadyCondition to exist")
				}
				if cond.Status != metav1.ConditionTrue {
					t.Errorf("expected condition status True, got %s", cond.Status)
				}
			} else if cond != nil {
				t.Errorf("expected ClusterAuditPolicyReadyCondition to not exist, but found: %+v", cond)
			}

			// Verify hash is set when audit config is created
			if tt.audit != nil && tt.audit.policy != nil && tt.expectConfigMapExists {
				if scope.audit.hash == "" {
					t.Error("expected audit hash to be set on scope")
				}
			}
		})
	}
}

func Test_releaseProviderCluster(t *testing.T) {
	const (
		cdName    = "test-cd"
		cdNs      = "default"
		infraGrp  = "infrastructure.cluster.x-k8s.io"
		infraKind = "AWSCluster"
		infraCRD  = "awsclusters.infrastructure.cluster.x-k8s.io"
		otherFin  = "kcm.test/other"
	)

	newCapiCluster := func(withRef bool) *clusterapiv1.Cluster {
		c := &clusterapiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: cdName, Namespace: cdNs},
		}
		if withRef {
			c.Spec.InfrastructureRef = clusterapiv1.ContractVersionedObjectReference{
				APIGroup: infraGrp,
				Kind:     infraKind,
				Name:     cdName,
			}
		}
		return c
	}

	// newInfraCRD returns the CRD carrying the CAPI contract label so
	// external.GetObjectFromContractVersionedRef resolves the API version to v1beta2
	newInfraCRD := func() *apiextv1.CustomResourceDefinition {
		return &apiextv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: infraCRD,
				Labels: map[string]string{
					clusterapiv1.GroupVersion.Group + "/v1beta2": "v1beta2",
				},
			},
		}
	}

	newInfraCluster := func(finalizers ...string) *unstructured.Unstructured {
		u := new(unstructured.Unstructured)
		u.SetAPIVersion(infraGrp + "/v1beta2")
		u.SetKind(infraKind)
		u.SetName(cdName)
		u.SetNamespace(cdNs)
		if len(finalizers) > 0 {
			u.SetFinalizers(finalizers)
		}
		return u
	}

	newMachine := func(name string) *clusterapiv1.Machine {
		return &clusterapiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: cdNs,
				Labels:    map[string]string{clusterapiv1.ClusterNameLabel: cdName},
			},
		}
	}

	tests := []struct {
		clientInterceptor *interceptor.Funcs
		name              string
		wantErrContains   string
		objects           []crclient.Object
		wantFinalizers    []string // nil = do not assert; non-nil = expect exact set on the infra Cluster
		wantErr           bool
	}{
		{
			name: "no CAPI Cluster - noop",
		},
		{
			name:    "CAPI Cluster with empty infrastructureRef - noop",
			objects: []crclient.Object{newCapiCluster(false)},
		},
		{
			name: "infra Cluster not found - noop",
			objects: []crclient.Object{
				newCapiCluster(true),
				newInfraCRD(),
			},
		},
		{
			name:    "infra provider CRD not discoverable - noop (NoMatch)",
			objects: []crclient.Object{newCapiCluster(true)},
			clientInterceptor: &interceptor.Funcs{
				Get: func(ctx context.Context, c crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
					if pm, ok := obj.(*metav1.PartialObjectMetadata); ok &&
						pm.GetObjectKind().GroupVersionKind().Kind == "CustomResourceDefinition" {
						return &meta.NoKindMatchError{
							GroupKind: schema.GroupKind{Group: infraGrp, Kind: infraKind},
						}
					}
					return c.Get(ctx, key, obj, opts...)
				},
			},
		},
		{
			name: "machines still exist - blocking finalizer preserved",
			objects: []crclient.Object{
				newCapiCluster(true),
				newInfraCRD(),
				newInfraCluster(kcmv1.BlockingFinalizer, otherFin),
				newMachine("m1"),
			},
			wantFinalizers: []string{kcmv1.BlockingFinalizer, otherFin},
		},
		{
			name: "no machines - blocking finalizer removed, others kept",
			objects: []crclient.Object{
				newCapiCluster(true),
				newInfraCRD(),
				newInfraCluster(kcmv1.BlockingFinalizer, otherFin),
			},
			wantFinalizers: []string{otherFin},
		},
		{
			name: "no machines, no blocking finalizer - noop",
			objects: []crclient.Object{
				newCapiCluster(true),
				newInfraCRD(),
				newInfraCluster(otherFin),
			},
			wantFinalizers: []string{otherFin},
		},
		{
			name: "transient error on CAPI Cluster Get returns error",
			clientInterceptor: &interceptor.Funcs{
				Get: func(_ context.Context, _ crclient.WithWatch, _ crclient.ObjectKey, obj crclient.Object, _ ...crclient.GetOption) error {
					if _, ok := obj.(*clusterapiv1.Cluster); ok {
						return errors.New("transient API server error")
					}
					return nil
				},
			},
			wantErr:         true,
			wantErrContains: "failed to get CAPI Cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := fake.NewClientBuilder().WithScheme(testscheme.Scheme)
			if len(tt.objects) > 0 {
				b = b.WithObjects(tt.objects...)
			}
			if tt.clientInterceptor != nil {
				b = b.WithInterceptorFuncs(*tt.clientInterceptor)
			}
			c := b.Build()

			r := &ClusterDeploymentReconciler{MgmtClient: c}
			scope := &clusterScope{
				cd:        &kcmv1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{Name: cdName, Namespace: cdNs}},
				rgnClient: c,
			}

			err := r.releaseProviderCluster(t.Context(), scope)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("expected error containing %q, got %v", tt.wantErrContains, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantFinalizers == nil {
				return
			}
			got := newInfraCluster()
			if err := c.Get(t.Context(), crclient.ObjectKeyFromObject(got), got); err != nil {
				t.Fatalf("failed to Get infra Cluster: %v", err)
			}

			gotFin := slices.Sorted(slices.Values(got.GetFinalizers()))
			wantFin := slices.Sorted(slices.Values(tt.wantFinalizers))
			if !slices.Equal(gotFin, wantFin) {
				t.Errorf("finalizers mismatch: got %v, want %v", got.GetFinalizers(), tt.wantFinalizers)
			}
		})
	}
}
