// Copyright 2026
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
	"encoding/json"
	"fmt"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/build"
	"github.com/K0rdent/kcm/internal/helm"
	helmutil "github.com/K0rdent/kcm/internal/util/helm"
)

const (
	testReleaseName       = "kcm-1-0-0"
	kcmBuildVersion       = "1.0.0"
	kcmTemplatesChartName = "kcm-1-0-0-tpl"

	kcmTemplatesTemplateName = "kcm-templates"

	coreKCMTemplateName         = "kcm-template"
	coreKCMRegionalTemplateName = "kcm-regional-template"
	coreCAPITemplateName        = "capi-template"
	awsProviderTemplateName     = "cluster-api-provider-aws-1-0-0"
	azureProviderTemplateName   = "cluster-api-provider-azure-1-0-1"

	regCertSecretName       = "test-reg-cert-secret"
	regCredentialSecretName = "test-reg-credential-secret"
)

var testReleaseSpec = kcmv1.ReleaseSpec{
	Version: kcmBuildVersion,
	KCM: kcmv1.CoreProviderTemplate{
		Template: coreKCMTemplateName,
	},
	Regional: kcmv1.CoreProviderTemplate{
		Template: coreKCMRegionalTemplateName,
	},
	CAPI: kcmv1.CoreProviderTemplate{
		Template: coreCAPITemplateName,
	},
	Providers: []kcmv1.NamedProviderTemplate{
		{
			Name: "aws",
			CoreProviderTemplate: kcmv1.CoreProviderTemplate{
				Template: awsProviderTemplateName,
			},
		},
		{
			Name: "azure",
			CoreProviderTemplate: kcmv1.CoreProviderTemplate{
				Template: azureProviderTemplateName,
			},
		},
	},
}

type releaseTestCase struct {
	createManagement bool
	createTemplates  bool
	createRelease    bool
	fluxEnabled      bool

	insecureRegistry              bool
	registryCredentialsSecretName string
	registryCertSecretName        string

	createPredeclaredSecretsFunc func() error
}

var _ = Describe("Release Controller", Ordered, func() {
	var reconciler *ReleaseReconciler

	BeforeAll(func() {
		// Ensure system namespace exists
		Expect(crclient.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: systemNamespace}}))).To(Succeed())
		build.Version = kcmBuildVersion
	})

	AfterAll(func() {
		build.Version = ""
	})

	AfterEach(func() {
		// Clean up secrets
		Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: regCertSecretName, Namespace: systemNamespace}}))).To(Succeed())
		Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: regCredentialSecretName, Namespace: systemNamespace}}))).To(Succeed())

		// Clean up ProviderTemplates
		for _, ptName := range []string{
			coreKCMTemplateName,
			coreKCMRegionalTemplateName,
			coreCAPITemplateName,
			awsProviderTemplateName,
			azureProviderTemplateName,
		} {
			Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, &kcmv1.ProviderTemplate{ObjectMeta: metav1.ObjectMeta{Name: ptName}}))).To(Succeed())
		}

		// Clean up HelmReleases
		helmReleases := &helmcontrollerv2.HelmReleaseList{}
		Expect(k8sClient.List(ctx, helmReleases)).To(Succeed())
		for _, hr := range helmReleases.Items {
			Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, &hr))).To(Succeed())
		}

		// Clean up HelmCharts
		helmCharts := &sourcev1.HelmChartList{}
		Expect(k8sClient.List(ctx, helmCharts)).To(Succeed())
		for _, hc := range helmCharts.Items {
			Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, &hc))).To(Succeed())
		}

		// Clean up HelmRepositories
		helmRepos := &sourcev1.HelmRepositoryList{}
		Expect(k8sClient.List(ctx, helmRepos)).To(Succeed())
		for _, helmRepo := range helmRepos.Items {
			Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, &helmRepo))).To(Succeed())
		}

		// Clean up Release
		Expect(crclient.IgnoreNotFound(k8sClient.Delete(ctx, &kcmv1.Release{ObjectMeta: metav1.ObjectMeta{Name: testReleaseName}}))).To(Succeed())
	})

	DescribeTable("Release Reconciliation",
		func(tc releaseTestCase) {
			reconciler = newTestReleaseReconciler(tc)
			testReleaseReconciliation(reconciler, tc)
		},
		Entry("Should do nothing", releaseTestCase{
			createTemplates:  false,
			createManagement: false,
			createRelease:    false,
		}),
		Entry("Should fail if no predeclared secrets exist", releaseTestCase{
			createTemplates:               true,
			createManagement:              false,
			createRelease:                 true,
			registryCertSecretName:        regCertSecretName,
			registryCredentialsSecretName: regCredentialSecretName,
		}),
		Entry("Should fail if registry credential secret does not exist", releaseTestCase{
			createTemplates:  true,
			createManagement: false,
			createRelease:    true,
			createPredeclaredSecretsFunc: func() error {
				return createTestRegistrySecret(regCertSecretName)
			},
			registryCertSecretName:        regCertSecretName,
			registryCredentialsSecretName: regCredentialSecretName,
		}),
		Entry("Should reconcile if all predefined secrets exist", releaseTestCase{
			createTemplates:  true,
			createManagement: false,
			createRelease:    true,
			createPredeclaredSecretsFunc: func() error {
				if err := createTestRegistrySecret(regCertSecretName); err != nil {
					return err
				}
				return createTestRegistrySecret(regCredentialSecretName)
			},
			registryCertSecretName:        regCertSecretName,
			registryCredentialsSecretName: regCredentialSecretName,
		}),
	)
})

func createTestRegistrySecret(name string) error {
	return k8sClient.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: systemNamespace,
		},
		Data: map[string][]byte{
			"foo": []byte("bar"),
		},
	})
}

func newTestReleaseReconciler(tc releaseTestCase) *ReleaseReconciler {
	defaultRegistryConfig := initializeDefaultTestRegistryConfig(tc)

	return &ReleaseReconciler{
		Client:                k8sClient,
		SystemNamespace:       systemNamespace,
		KCMTemplatesChartName: kcmTemplatesTemplateName,

		DefaultRegistryConfig: defaultRegistryConfig,

		DefaultHelmTimeout: 20 * time.Minute,

		CreateRelease:    tc.createRelease,
		CreateManagement: tc.createManagement,
		CreateTemplates:  tc.createTemplates,
		FluxEnabled:      tc.fluxEnabled,
	}
}

func initializeDefaultTestRegistryConfig(tc releaseTestCase) helm.DefaultRegistryConfig {
	const templatesRepoURL = "https://example.com/helm-charts"

	repoType, err := helmutil.DetermineDefaultRepositoryType(templatesRepoURL)
	Expect(err).NotTo(HaveOccurred())
	conf := helm.DefaultRegistryConfig{
		URL:      templatesRepoURL,
		RepoType: repoType,
	}
	if tc.registryCredentialsSecretName != "" {
		conf.CredentialsSecretName = tc.registryCredentialsSecretName
	}
	if tc.registryCertSecretName != "" {
		conf.CertSecretName = tc.registryCertSecretName
	}
	if tc.insecureRegistry {
		conf.Insecure = tc.insecureRegistry
	}
	return conf
}

func testReleaseReconciliation(reconciler *ReleaseReconciler, tc releaseTestCase) {
	if tc.createPredeclaredSecretsFunc != nil {
		Expect(tc.createPredeclaredSecretsFunc()).NotTo(HaveOccurred())
	}

	By("Reconciliation when no Release exists, but the request name is provided, should ignore since the object must be deleted")
	_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testReleaseName}})
	Expect(err).NotTo(HaveOccurred())

	By("Initial reconciliation, no Release exists")
	result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{}})
	// when `continueTest` is set to false, we are in the condition when we should not proceed
	// with the fake Release creation and further Release object reconciliation (for example, when
	// templates creation wasn't requested or predeclared secrets are missing)
	continueTest := testReleaseInitialReconciliation(reconciler, tc, err)
	if !continueTest {
		return
	}

	By("KCM Templates HelmRelease is not yet ready, should requeue")
	Expect(result).To(Equal(ctrl.Result{RequeueAfter: 10 * time.Second}))

	By("Marking KCM Templates HelmRelease as ready to continue reconciliation")
	hr := &helmcontrollerv2.HelmRelease{}
	err = k8sClient.Get(ctx, types.NamespacedName{Name: kcmTemplatesChartName, Namespace: systemNamespace}, hr)
	Expect(err).NotTo(HaveOccurred())

	apimeta.SetStatusCondition(&hr.Status.Conditions, metav1.Condition{
		Type:               fluxmeta.ReadyCondition,
		Status:             metav1.ConditionTrue,
		Reason:             fluxmeta.SucceededReason,
		ObservedGeneration: hr.Generation,
	})
	Expect(k8sClient.Status().Update(ctx, hr)).To(Succeed())

	By("Creating Release object")
	testRelease := &kcmv1.Release{
		ObjectMeta: metav1.ObjectMeta{
			Name: testReleaseName,
		},
		Spec: testReleaseSpec,
	}
	Expect(k8sClient.Create(ctx, testRelease)).To(Succeed())

	By("First Release reconciliation, should add KCM components label on the Release object")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testReleaseName}})
	Expect(err).NotTo(HaveOccurred())

	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testReleaseName}, testRelease)).To(Succeed())
	Expect(testRelease.Labels).To(Equal(map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM}))

	By("Creating some ProviderTemplates and mark them as ready")
	createTestProviderTemplatesForRelease(testRelease, []string{coreKCMTemplateName, coreCAPITemplateName})

	By("Next reconciliation, some templates are ready, should proceed")
	// Use the cached client because field indexers require it
	reconciler.Client = mgrClient
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testReleaseName}})
	testReleaseNextReconciliation(reconciler, tc, err)

	Expect(err).To(MatchError(fmt.Sprintf("missing or invalid templates: %s, %s, %s", coreKCMRegionalTemplateName, awsProviderTemplateName, azureProviderTemplateName)))

	By("Creating the rest of ProviderTemplates and mark them as ready")
	createTestProviderTemplatesForRelease(testRelease, []string{coreKCMRegionalTemplateName, awsProviderTemplateName, azureProviderTemplateName})
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testReleaseName}})
	testReleaseNextReconciliation(reconciler, tc, err)

	Expect(err).NotTo(HaveOccurred())
}

// testReleaseInitialReconciliation validates the initial reconciliation logic when the Release object is not yet created
func testReleaseInitialReconciliation(reconciler *ReleaseReconciler, tc releaseTestCase, reconcileErr error) (continueTest bool) {
	if !tc.createRelease && !tc.createManagement && !tc.createTemplates {
		By("Should do nothing when no management, release or template creation is requested")
		Expect(reconcileErr).NotTo(HaveOccurred())
		return false
	}
	if !tc.createRelease && !tc.createTemplates {
		By("Should not create default HelmRepository")

		Expect(reconcileErr).NotTo(HaveOccurred())
		validateHelmRepositoryIsNotCreated()
	}

	if tc.registryCertSecretName != "" || tc.registryCredentialsSecretName != "" {
		By("Should fail if any of predefined secrets do not exist")

		var predeclaredSecrets, missingSecrets []string
		if tc.registryCertSecretName != "" {
			predeclaredSecrets = append(predeclaredSecrets, tc.registryCertSecretName)
			err := k8sClient.Get(ctx, types.NamespacedName{Name: tc.registryCertSecretName, Namespace: systemNamespace}, &corev1.Secret{})
			Expect(crclient.IgnoreNotFound(err)).NotTo(HaveOccurred())
			if apierrors.IsNotFound(err) {
				missingSecrets = append(missingSecrets, tc.registryCertSecretName)
			}
		}
		if tc.registryCredentialsSecretName != "" {
			predeclaredSecrets = append(predeclaredSecrets, tc.registryCredentialsSecretName)
			err := k8sClient.Get(ctx, types.NamespacedName{Name: tc.registryCredentialsSecretName, Namespace: systemNamespace}, &corev1.Secret{})
			Expect(crclient.IgnoreNotFound(err)).NotTo(HaveOccurred())
			if apierrors.IsNotFound(err) {
				missingSecrets = append(missingSecrets, tc.registryCredentialsSecretName)
			}
		}

		if len(missingSecrets) > 0 {
			Expect(reconcileErr).To(HaveOccurred())
			expectedErr := fmt.Sprintf("some of the predeclared Secrets (%v) are missing (%v) in the %s namespace", predeclaredSecrets, missingSecrets, systemNamespace)
			Expect(reconcileErr).To(MatchError(ContainSubstring(expectedErr)))

			// when secrets are missing, the reconciliation should not proceed to create any resources, so we return here
			return false
		}
	}

	if tc.createRelease || tc.createTemplates {
		By("Should create default HelmRepository on initial installation")
		helmRepo := &sourcev1.HelmRepository{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: kcmv1.DefaultRepoName, Namespace: systemNamespace}, helmRepo)
		Expect(err).NotTo(HaveOccurred())
		Expect(helmRepo.Labels).To(Equal(map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue}))

		expectedHelmRepoSpec := reconciler.DefaultRegistryConfig.HelmRepositorySpec()
		Expect(helmRepo.Spec.URL).To(Equal(expectedHelmRepoSpec.URL))
		Expect(helmRepo.Spec.SecretRef).To(Equal(expectedHelmRepoSpec.SecretRef))
		if tc.registryCertSecretName != "" {
			Expect(helmRepo.Spec.CertSecretRef).NotTo(BeNil())
		} else {
			Expect(helmRepo.Spec.CertSecretRef).To(BeNil())
		}
		Expect(helmRepo.Spec.Insecure).To(Equal(tc.insecureRegistry))
	}

	if tc.createTemplates {
		validateKCMTemplatesComponents(reconciler.DefaultHelmTimeout)
	}

	if tc.createManagement {
		By("Should ensure management if requested")

		mgmt := &kcmv1.Management{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: kcmv1.ManagementName}, mgmt)).To(Succeed())
		return false
	}
	return true
}

// testReleaseNextReconciliation validates the reconciliation logic for a Release object
func testReleaseNextReconciliation(reconciler *ReleaseReconciler, tc releaseTestCase, reconcileErr error) {
	if !tc.createRelease && !tc.createTemplates {
		By("Should not create default HelmRepository")

		Expect(reconcileErr).NotTo(HaveOccurred())
		validateHelmRepositoryIsNotCreated()
	}

	testRelease := &kcmv1.Release{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testReleaseName}, testRelease)).To(Succeed())
	var templatesCreatedCondMessage string

	if tc.createTemplates {
		validateKCMTemplatesComponents(reconciler.DefaultHelmTimeout)
		templatesCreatedCondMessage = "All templates have been created"
	} else {
		By("Should set TemplatesCreatedCondition to True since templates creation is disabled")
		templatesCreatedCondMessage = "Templates creation is disabled"
	}

	By("Should set TemplatesCreatedCondition")
	validateStatusConditionExistsAndEqual(testRelease.Status.Conditions,
		kcmv1.TemplatesCreatedCondition,
		metav1.ConditionTrue,
		kcmv1.SucceededReason,
		templatesCreatedCondMessage)
}

// validateHelmRepositoryIsNotCreated asserts that the default HelmRepository does not exist
func validateHelmRepositoryIsNotCreated() {
	By("Should not create default HelmRepository")
	helmRepo := &sourcev1.HelmRepository{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: kcmv1.DefaultRepoName, Namespace: systemNamespace}, helmRepo)
	Expect(err).To(HaveOccurred())
	Expect(apierrors.IsNotFound(err)).To(BeTrue())
}

// validateKCMTemplatesComponents asserts that the KCM Templates HelmChart and HelmRelease are created and configured correctly
func validateKCMTemplatesComponents(defaultHelmTimeout time.Duration) {
	By("Should create or update KCM Templates HelmChart")
	helmChart := &sourcev1.HelmChart{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: kcmTemplatesChartName, Namespace: systemNamespace}, helmChart)
	Expect(err).NotTo(HaveOccurred())
	Expect(helmChart.Labels).To(Equal(map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue}))
	Expect(helmChart.Spec.Chart).To(Equal(kcmTemplatesTemplateName))
	Expect(helmChart.Spec.Version).To(Equal(kcmBuildVersion))
	Expect(helmChart.Spec.SourceRef).To(Equal(kcmv1.DefaultSourceRef))
	Expect(helmChart.Spec.Interval).To(Equal(metav1.Duration{Duration: helm.DefaultReconcileInterval}))

	By("Should create or update KCM Templates HelmRelease")
	hr := &helmcontrollerv2.HelmRelease{}
	err = k8sClient.Get(ctx, types.NamespacedName{Name: kcmTemplatesChartName, Namespace: systemNamespace}, hr)
	Expect(err).NotTo(HaveOccurred())
	Expect(hr.Labels).To(Equal(map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue}))
	Expect(hr.Spec.ChartRef.Kind).To(Equal(sourcev1.HelmChartKind))
	Expect(hr.Spec.ChartRef.Name).To(Equal(kcmTemplatesChartName))
	Expect(hr.Spec.ChartRef.Namespace).To(Equal(systemNamespace))
	Expect(hr.Spec.Timeout).To(Equal(&metav1.Duration{Duration: defaultHelmTimeout}))

	var (
		val       = hr.Spec.Values
		valuesMap map[string]any
	)
	Expect(json.Unmarshal(val.Raw, &valuesMap)).To(Succeed())
	Expect(valuesMap).To(Equal(map[string]any{"createRelease": true}))
}

// createTestProviderTemplatesForRelease creates and marks ProviderTemplates as valid for the given Release
func createTestProviderTemplatesForRelease(release *kcmv1.Release, providerTemplateNames []string) {
	By("Creating ProviderTemplate objects and marking them as valid")
	for _, ptName := range providerTemplateNames {
		pt := &kcmv1.ProviderTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ptName,
				Namespace: systemNamespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: kcmv1.GroupVersion.String(),
						Kind:       kcmv1.ReleaseKind,
						Name:       testReleaseName,
						UID:        release.UID,
					},
				},
			},
			Spec: kcmv1.ProviderTemplateSpec{
				Helm: kcmv1.HelmSpec{
					ChartSpec: &sourcev1.HelmChartSpec{
						Chart:    ptName,
						Version:  "1.0.0",
						Interval: metav1.Duration{Duration: 10 * time.Minute},
						SourceRef: sourcev1.LocalHelmChartSourceReference{
							Kind: "HelmRepository",
							Name: "kcm-templates",
						},
					},
				},
			},
		}
		Expect(mgrClient.Create(ctx, pt)).To(Succeed())

		pt.Status.Valid = true
		pt.Status.ObservedGeneration = pt.Generation
		Expect(mgrClient.Status().Update(ctx, pt)).To(Succeed())

		// Wait until the cache observes the created object
		Eventually(func() error {
			err := mgrClient.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: ptName}, pt)
			if err != nil {
				return err
			}
			if !pt.Status.Valid || pt.Status.ObservedGeneration != pt.Generation {
				return fmt.Errorf("ProviderTemplate %s is not yet valid or observed generation is not updated", ptName)
			}
			return nil
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())
	}
}
