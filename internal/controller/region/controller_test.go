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

package region

import (
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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	systemNamespace = "kcm-system"
	releaseName     = "test-release-name"

	registryCertSecretName = "registry-cert"
	imagePullSecretName    = "image-pull-secret"

	kubeconfigSecretKey = "value"

	coreKCMTemplateName           = "test-template-kcm"
	coreKCMRegionalTemplateName   = "test-template-kcm-regional"
	coreCAPITemplateName          = "test-template-capi"
	awsProviderTemplateName       = "test-template-aws"
	openstackProviderTemplateName = "test-template-openstack"
	azureProviderTemplateName     = "test-template-azure"
)

var secretsToCopy = map[string]map[string][]byte{
	registryCertSecretName: {
		"tls.crt": []byte("test-cert"),
	},
	imagePullSecretName: {
		".dockerconfigjson": []byte("test"),
	},
}

type testCase struct {
	// regionName is the name of the Region being tested.
	regionName types.NamespacedName
	// enabledProviders defines the list of enabled providers;
	// key is the provider name, value is the corresponding ProviderTemplate name.
	enabledProviders map[string]string
	// clusterDeploymentRef marks that the Region being tested should contain
	// a reference to an existing ClusterDeployment object to be onboarded
	// as a regional cluster.
	clusterDeploymentRef *kcmv1.ClusterDeploymentRef
}

var providers = map[string]string{
	"aws":       awsProviderTemplateName,
	"openstack": openstackProviderTemplateName,
	"azure":     azureProviderTemplateName,
}

var tests = []testCase{
	{
		regionName: types.NamespacedName{Name: "rgn1"},
		enabledProviders: map[string]string{
			"aws":       awsProviderTemplateName,
			"openstack": openstackProviderTemplateName,
		},
	},
	{
		regionName: types.NamespacedName{Name: "rgn2"},
		enabledProviders: map[string]string{
			"azure": azureProviderTemplateName,
		},
		clusterDeploymentRef: &kcmv1.ClusterDeploymentRef{
			Namespace: "cld-ns",
			Name:      "cld-name",
		},
	},
}

var _ = Describe("Region controller", Ordered, func() {
	var supportedProviders []kcmv1.NamedProviderTemplate

	for name, tpl := range providers {
		supportedProviders = append(supportedProviders, kcmv1.NamedProviderTemplate{
			Name:                 name,
			CoreProviderTemplate: kcmv1.CoreProviderTemplate{Template: tpl},
		})
	}

	BeforeAll(func() {
		By("Creating the system namespace")
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: systemNamespace,
			},
		}
		Expect(mgmtClient.Create(ctx, ns)).To(Succeed())

		By("Creating the Release object")
		release := &kcmv1.Release{
			ObjectMeta: metav1.ObjectMeta{
				Name: releaseName,
			},
			Spec: kcmv1.ReleaseSpec{
				Version:   "test-version",
				KCM:       kcmv1.CoreProviderTemplate{Template: coreKCMTemplateName},
				Regional:  kcmv1.CoreProviderTemplate{Template: coreKCMRegionalTemplateName},
				CAPI:      kcmv1.CoreProviderTemplate{Template: coreCAPITemplateName},
				Providers: supportedProviders,
			},
		}
		Expect(mgmtClient.Create(ctx, release)).To(Succeed())

		By("Creating the Management object")
		mgmt := &kcmv1.Management{
			ObjectMeta: metav1.ObjectMeta{
				Name: kcmv1.ManagementName,
			},
			Spec: kcmv1.ManagementSpec{
				Release: releaseName,
				ComponentsCommonSpec: kcmv1.ComponentsCommonSpec{
					Core: &kcmv1.Core{
						KCM:  kcmv1.Component{},
						CAPI: kcmv1.Component{},
					},
				},
			},
		}
		Expect(mgmtClient.Create(ctx, mgmt)).To(Succeed())

		By("Creating required secrets in the system namespace")
		for secretName, secretData := range secretsToCopy {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: systemNamespace,
				},
				Data: secretData,
			}
			Expect(mgmtClient.Create(ctx, secret)).To(Succeed())
		}

		By("Creating ProviderTemplate objects and marking them as valid")
		for _, ptName := range []string{
			coreKCMTemplateName,
			coreKCMRegionalTemplateName,
			coreCAPITemplateName,
			awsProviderTemplateName,
			openstackProviderTemplateName,
			azureProviderTemplateName,
		} {
			pt := &kcmv1.ProviderTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ptName,
					Namespace: systemNamespace,
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
			Expect(mgmtClient.Create(ctx, pt)).To(Succeed())

			err := mgmtClient.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: ptName}, pt)
			Expect(err).NotTo(HaveOccurred())

			pt.Status.Valid = true
			pt.Status.ChartRef = &helmcontrollerv2.CrossNamespaceSourceReference{
				Kind:      "HelmChart",
				Name:      ptName,
				Namespace: systemNamespace,
			}
			Expect(mgmtClient.Status().Update(ctx, pt)).To(Succeed())
		}
	})

	Context("When reconciling Region resources", func() {
		for _, testCfg := range tests {
			t := testCfg

			var (
				kubeconfigSecretName      = "kubeconfig-secret"
				kubeconfigSecretNamespace = systemNamespace
			)

			if t.clusterDeploymentRef != nil {
				kubeconfigSecretName = t.clusterDeploymentRef.Name + "-kubeconfig"
				kubeconfigSecretNamespace = t.clusterDeploymentRef.Namespace
			}

			kubeconfigSecretRef := &fluxmeta.SecretKeyReference{
				Name: kubeconfigSecretName,
				Key:  kubeconfigSecretKey,
			}
			kubeconfigSecretNamespacedName := types.NamespacedName{
				Namespace: kubeconfigSecretNamespace,
				Name:      kubeconfigSecretName,
			}

			BeforeEach(func() {
				By("Ensuring the kubeconfig namespace exists")
				ns := &corev1.Namespace{}
				err := mgmtClient.Get(ctx, types.NamespacedName{Name: kubeconfigSecretNamespace}, ns)
				if apierrors.IsNotFound(err) {
					ns = &corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name: kubeconfigSecretNamespace,
						},
					}
					Expect(mgmtClient.Create(ctx, ns)).To(Succeed())
				} else {
					Expect(err).NotTo(HaveOccurred())
				}

				By("Ensuring the kubeconfig Secret exists")
				kubeconfigSecret := &corev1.Secret{}
				err = mgmtClient.Get(ctx, kubeconfigSecretNamespacedName, kubeconfigSecret)
				if apierrors.IsNotFound(err) {
					secret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: kubeconfigSecretNamespace,
							Name:      kubeconfigSecretName,
						},
						Data: map[string][]byte{
							kubeconfigSecretKey: rgnKubeconfig,
						},
					}
					Expect(mgmtClient.Create(ctx, secret)).To(Succeed())
				} else {
					Expect(err).NotTo(HaveOccurred())
				}

				if t.clusterDeploymentRef != nil {
					By("Ensuring the ClusterDeployment object exists")
					cld := &kcmv1.ClusterDeployment{}
					err := mgmtClient.Get(ctx, types.NamespacedName{
						Namespace: t.clusterDeploymentRef.Namespace,
						Name:      t.clusterDeploymentRef.Name,
					}, cld)
					if apierrors.IsNotFound(err) {
						cld = &kcmv1.ClusterDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: t.clusterDeploymentRef.Namespace,
								Name:      t.clusterDeploymentRef.Name,
							},
							Spec: kcmv1.ClusterDeploymentSpec{
								Template: "test-template",
							},
						}
						Expect(mgmtClient.Create(ctx, cld)).To(Succeed())
					} else {
						Expect(err).NotTo(HaveOccurred())
					}
				}
			})

			It("reconciles Region "+t.regionName.Name, func() {
				By("Creating the Region custom resource if it does not exist")
				rgn := &kcmv1.Region{}
				err := mgmtClient.Get(ctx, t.regionName, rgn)
				if apierrors.IsNotFound(err) {
					componentsSpec := kcmv1.ComponentsCommonSpec{
						Core: &kcmv1.Core{
							KCM:  kcmv1.Component{},
							CAPI: kcmv1.Component{},
						},
					}
					for name := range t.enabledProviders {
						componentsSpec.Providers = append(componentsSpec.Providers, kcmv1.Provider{Name: name})
					}

					rgn = &kcmv1.Region{
						ObjectMeta: metav1.ObjectMeta{
							Name: t.regionName.Name,
						},
						Spec: kcmv1.RegionSpec{
							ComponentsCommonSpec: componentsSpec,
						},
					}

					if t.clusterDeploymentRef != nil {
						rgn.Spec.ClusterDeployment = t.clusterDeploymentRef
					} else {
						rgn.Spec.KubeConfig = kubeconfigSecretRef
					}

					rgn.GetObjectKind().SetGroupVersionKind(kcmv1.GroupVersion.WithKind(kcmv1.RegionKind))

					Expect(mgmtClient.Create(ctx, rgn)).To(Succeed())
				} else {
					Expect(err).NotTo(HaveOccurred())
				}

				reconciler := newTestReconciler()
				testRegionReconciliation(reconciler, t)
			})

			It("cleans up Region "+t.regionName.Name, func() {
				rgn := &kcmv1.Region{}
				err := mgmtClient.Get(ctx, t.regionName, rgn)
				if err == nil {
					Expect(mgmtClient.Delete(ctx, rgn)).To(Succeed())
				} else if !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred())
				}

				reconciler := newTestReconciler()
				testRegionCleanup(reconciler, t)
			})
		}
	})
})

func newTestReconciler() *Reconciler {
	return &Reconciler{
		MgmtClient:                    mgmtClient,
		SystemNamespace:               systemNamespace,
		RegistryCertSecretName:        registryCertSecretName,
		ImagePullSecretName:           imagePullSecretName,
		skipCertManagerInstalledCheck: true,
	}
}

func testRegionReconciliation(reconciler *Reconciler, t testCase) {
	By("Reconciling the Region: should add finalizer")
	_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.regionName})
	Expect(err).NotTo(HaveOccurred())

	rgn := &kcmv1.Region{}
	err = mgmtClient.Get(ctx, t.regionName, rgn)
	Expect(err).NotTo(HaveOccurred())
	Expect(rgn.Finalizers).To(ContainElement(kcmv1.RegionFinalizer))

	By("Reconciling the Region: should add KCM component label")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.regionName})
	Expect(err).NotTo(HaveOccurred())

	err = mgmtClient.Get(ctx, t.regionName, rgn)
	Expect(err).NotTo(HaveOccurred())
	Expect(rgn.Labels[kcmv1.GenericComponentNameLabel]).To(Equal(kcmv1.GenericComponentLabelValueKCM))

	By("Reconciling the Region: initial setup")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.regionName})
	Expect(err).NotTo(HaveOccurred())

	if t.clusterDeploymentRef != nil {
		By("Reconciling the Region: should copy the regional kubeconfig Secret to the system namespace")
		copiedSecret := &corev1.Secret{}
		secretName := t.clusterDeploymentRef.Namespace + "." + t.clusterDeploymentRef.Name + "-kubeconfig"
		err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: secretName}, copiedSecret)
		Expect(err).NotTo(HaveOccurred())
		Expect(copiedSecret.Data[kubeconfigSecretKey]).To(Equal(rgnKubeconfig))
	}

	By("Reconciling the Region: should copy the required Secrets to the regional cluster")
	for secretName, secretData := range secretsToCopy {
		secret := &corev1.Secret{}
		err = rgnClient.Get(ctx, types.NamespacedName{Namespace: systemNamespace, Name: secretName}, secret)
		Expect(err).NotTo(HaveOccurred())
		Expect(secret.Data).To(Equal(secretData))
		Expect(secret.Labels).To(HaveKeyWithValue(kcmv1.KCMRegionLabelKey, t.regionName.Name))
	}

	By("Reconciling the Region: should create kcm-regional HelmRelease")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.regionName})
	Expect(err).NotTo(HaveOccurred())
	markHelmReleaseReady(rgn.Name, kcmv1.CoreKCMRegionalName)

	By("Reconciling the Region: should create CAPI HelmRelease")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.regionName})
	Expect(err).NotTo(HaveOccurred())
	markHelmReleaseReady(rgn.Name, kcmv1.CoreCAPIName)

	By("Reconciling the Region: should set partial status with core components ready")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.regionName})
	Expect(err).NotTo(HaveOccurred())

	err = mgmtClient.Get(ctx, t.regionName, rgn)
	Expect(err).NotTo(HaveOccurred())

	partialComponentsStatus := map[string]kcmv1.ComponentStatus{
		kcmv1.CoreKCMRegionalName: {
			Template: coreKCMRegionalTemplateName,
			Success:  true,
		},
		kcmv1.CoreCAPIName: {
			Template: coreCAPITemplateName,
			Success:  true,
		},
	}
	for _, provider := range rgn.Spec.Providers {
		partialComponentsStatus[provider.Name] = kcmv1.ComponentStatus{
			Template: t.enabledProviders[provider.Name],
			Error:    fmt.Sprintf("HelmRelease %s/%s-%s Ready condition is not updated yet", systemNamespace, rgn.Name, provider.Name),
		}
	}

	Expect(rgn.Status.ComponentsCommonStatus.Components).To(Equal(partialComponentsStatus))

	By("Reconciling the Region: should create providers' HelmReleases")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.regionName})
	Expect(err).NotTo(HaveOccurred())

	for providerName := range t.enabledProviders {
		markHelmReleaseReady(rgn.Name, providerName)
	}

	By("Reconciling the Region: should have correct status and mark Region as Ready")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.regionName})
	Expect(err).NotTo(HaveOccurred())

	err = mgmtClient.Get(ctx, t.regionName, rgn)
	Expect(err).NotTo(HaveOccurred())

	expectedComponentsStatus := map[string]kcmv1.ComponentStatus{
		kcmv1.CoreKCMRegionalName: {
			Template: coreKCMRegionalTemplateName,
			Success:  true,
		},
		kcmv1.CoreCAPIName: {
			Template: coreCAPITemplateName,
			Success:  true,
		},
	}

	for _, provider := range rgn.Spec.Providers {
		expectedComponentsStatus[provider.Name] = kcmv1.ComponentStatus{
			Template: t.enabledProviders[provider.Name],
			Success:  true,
		}
	}

	Expect(rgn.Status.ComponentsCommonStatus.Components).To(Equal(expectedComponentsStatus))
}

func testRegionCleanup(reconciler *Reconciler, t testCase) {
	By("Reconciling the Region cleanup: should remove provider HelmReleases first")
	_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.regionName})
	Expect(err).NotTo(HaveOccurred())

	rgn := &kcmv1.Region{}
	rgnName := t.regionName.Name
	err = mgmtClient.Get(ctx, t.regionName, rgn)
	Expect(err).NotTo(HaveOccurred())

	for providerName := range t.enabledProviders {
		hr := &helmcontrollerv2.HelmRelease{}
		err := mgmtClient.Get(ctx, types.NamespacedName{
			Namespace: systemNamespace,
			Name:      rgnName + "-" + providerName,
		}, hr)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}

	By("Reconciling the Region cleanup: should remove CAPI HelmRelease")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.regionName})
	Expect(err).NotTo(HaveOccurred())

	hr := &helmcontrollerv2.HelmRelease{}
	err = mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: systemNamespace,
		Name:      rgnName + "-" + coreCAPITemplateName,
	}, hr)
	Expect(apierrors.IsNotFound(err)).To(BeTrue())

	By("Reconciling the Region cleanup: should remove kcm-regional HelmRelease")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.regionName})
	Expect(err).NotTo(HaveOccurred())

	err = mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: systemNamespace,
		Name:      rgnName + "-" + coreKCMRegionalTemplateName,
	}, hr)
	Expect(apierrors.IsNotFound(err)).To(BeTrue())

	By("Reconciling the Region cleanup: should remove all Secrets managed by the Region")
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: t.regionName})
	Expect(err).NotTo(HaveOccurred())

	for secretName := range secretsToCopy {
		By(fmt.Sprintf("Ensuring Secret is removed from regional cluster: %s/%s", systemNamespace, secretName))
		secret := &corev1.Secret{}
		err = rgnClient.Get(ctx, types.NamespacedName{
			Namespace: systemNamespace,
			Name:      secretName,
		}, secret)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}

	By("Ensuring the Region object is removed")
	err = mgmtClient.Get(ctx, t.regionName, rgn)
	Expect(apierrors.IsNotFound(err)).To(BeTrue())
}

func markHelmReleaseReady(rgnName, componentName string) {
	hr := &helmcontrollerv2.HelmRelease{}
	err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: systemNamespace,
		Name:      rgnName + "-" + componentName,
	}, hr)
	Expect(err).NotTo(HaveOccurred())

	const (
		helmReleaseConfigDigest           = "sha256:some_digest"
		helmReleaseSnapshotDeployedStatus = "deployed"
	)

	hr.Status.History = helmcontrollerv2.Snapshots{
		{
			Name:          componentName,
			FirstDeployed: metav1.Now(),
			LastDeployed:  metav1.Now(),
			Status:        helmReleaseSnapshotDeployedStatus,
			ConfigDigest:  helmReleaseConfigDigest,
		},
	}

	apimeta.SetStatusCondition(&hr.Status.Conditions, metav1.Condition{
		Type:               fluxmeta.ReadyCondition,
		Status:             metav1.ConditionTrue,
		Reason:             fluxmeta.SucceededReason,
		ObservedGeneration: hr.Generation,
	})

	hr.Status.ObservedGeneration = hr.Generation
	hr.Status.LastAttemptedConfigDigest = helmReleaseConfigDigest

	Expect(mgmtClient.Status().Update(ctx, hr)).To(Succeed())
}
