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
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcm "github.com/K0rdent/kcm/api/v1beta1"
)

//nolint:dupl
var _ = Describe("ServiceTemplate Controller", func() {
	var (
		reconciler      ServiceTemplateReconciler
		namespace       corev1.Namespace
		secret          corev1.Secret
		serviceTemplate kcm.ServiceTemplate
		gitRepository   sourcev1.GitRepository
		bucket          sourcev1.Bucket
		ociRepository   sourcev1.OCIRepository
	)

	Context("When reconciling ServiceTemplate", func() {
		BeforeEach(func() {
			By("creating reconciler", func() {
				reconciler = ServiceTemplateReconciler{
					TemplateReconciler: TemplateReconciler{
						Client: k8sClient,
					},
				}
			})

			By("creating namespace", func() {
				namespace = corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "servicetemplate-ns-",
					},
				}
				Expect(k8sClient.Create(ctx, &namespace)).To(Succeed())
			})

			By("defining ServiceTemplate metadata", func() {
				serviceTemplate = kcm.ServiceTemplate{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "servicetemplate-",
						Namespace:    namespace.Name,
					},
				}
			})

			By("defining Secret metadata", func() {
				secret = corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "servicetemplate-secret-",
						Namespace:    namespace.Name,
					},
				}
			})

			By("defining Bucket metadata", func() {
				bucket = sourcev1.Bucket{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "servicetemplate-bucket-",
						Namespace:    namespace.Name,
					},
				}
			})

			By("defining GitRepository metadata", func() {
				gitRepository = sourcev1.GitRepository{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "servicetemplate-gitrepo-",
						Namespace:    namespace.Name,
					},
				}
			})
		})

		AfterEach(func() {
			By("cleanup resources", func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &namespace))).To(Succeed())
			})
		})

		It("should fail to create invalid service template", func() {
			By("creating service template with empty spec", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
			By("creating service template with multiple one-of choices", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Helm:      &kcm.HelmSpec{},
					Kustomize: &kcm.SourceSpec{},
					Resources: &kcm.SourceSpec{},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
			By("creating service template with multiple one-of choices: Helm & Kustomize", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Helm:      &kcm.HelmSpec{},
					Kustomize: &kcm.SourceSpec{},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
			By("creating service template with multiple one-of choices: Helm & Resources", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Helm:      &kcm.HelmSpec{},
					Resources: &kcm.SourceSpec{},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
			By("creating service template with multiple one-of choices: Kustomize & Resources", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{},
					Resources: &kcm.SourceSpec{},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
			By("creating service template with empty path in Source spec", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
			By("creating service template without source defined in Source spec", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path:             ".",
						LocalSourceRef:   nil,
						RemoteSourceSpec: nil,
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
			By("creating service template with multiple sources defined in Source spec", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path:             ".",
						LocalSourceRef:   &kcm.LocalSourceRef{},
						RemoteSourceSpec: &kcm.RemoteSourceSpec{},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
			By("creating service template with unsupported kind in local source in Source spec", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path:           ".",
						LocalSourceRef: &kcm.LocalSourceRef{Kind: "invalid"},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
			By("creating service template with multiple remote sources in Source spec", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path: ".",
						RemoteSourceSpec: &kcm.RemoteSourceSpec{
							Git:    &kcm.EmbeddedGitRepositorySpec{},
							Bucket: &kcm.EmbeddedBucketSpec{},
							OCI:    &kcm.EmbeddedOCIRepositorySpec{},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
			By("creating service template with multiple remote sources in Source spec: Git & Bucket", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path: ".",
						RemoteSourceSpec: &kcm.RemoteSourceSpec{
							Git:    &kcm.EmbeddedGitRepositorySpec{},
							Bucket: &kcm.EmbeddedBucketSpec{},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
			By("creating service template with multiple remote sources in Source spec: Git & OCI", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path: ".",
						RemoteSourceSpec: &kcm.RemoteSourceSpec{
							Git: &kcm.EmbeddedGitRepositorySpec{},
							OCI: &kcm.EmbeddedOCIRepositorySpec{},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
			By("creating service template with multiple remote sources in Source spec: Bucket & OCI", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path: ".",
						RemoteSourceSpec: &kcm.RemoteSourceSpec{
							Bucket: &kcm.EmbeddedBucketSpec{},
							OCI:    &kcm.EmbeddedOCIRepositorySpec{},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).NotTo(Succeed())
			})
		})

		It("should set service template state to invalid if local source is not found", func() {
			By("creating service template with local source", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path:           ".",
						DeploymentType: "Remote",
						LocalSourceRef: &kcm.LocalSourceRef{
							Kind: "ConfigMap",
							Name: "absent-configmap",
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &serviceTemplate)
			})

			By("reconciling service template", func() {
				Eventually(func(g Gomega) {
					_, _ = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&serviceTemplate)})
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&serviceTemplate), &serviceTemplate)).To(Succeed())
					g.Expect(serviceTemplate.Status.Valid).To(BeFalse())
					g.Expect(serviceTemplate.Status.ValidationError).NotTo(BeEmpty())
				}, eventuallyTimeout, pollingInterval).Should(Succeed())
			})
		})

		It("should set service template state to invalid if local source is not ready", func() {
			By("creating bucket with not ready status", func() {
				bucket.Spec = sourcev1.BucketSpec{
					Provider:   "azure",
					BucketName: "not-ready-bucket",
					Endpoint:   "https://not-ready-bucket.blob.core.windows.net",
					Interval:   metav1.Duration{Duration: time.Second},
				}

				Expect(k8sClient.Create(ctx, &bucket)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &bucket)
				bucket.Status.Conditions = append(bucket.Status.Conditions, metav1.Condition{
					Type:               kcm.ReadyCondition,
					Status:             metav1.ConditionFalse,
					Reason:             "NotReady",
					LastTransitionTime: metav1.NewTime(time.Now()),
				})
				Expect(k8sClient.Status().Update(ctx, &bucket)).To(Succeed())
			})

			By("creating service template with bucket source", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path:           ".",
						DeploymentType: "Remote",
						LocalSourceRef: &kcm.LocalSourceRef{
							Kind: "Bucket",
							Name: bucket.Name,
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &serviceTemplate)
			})

			By("reconciling service template", func() {
				Eventually(func(g Gomega) {
					_, _ = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&serviceTemplate)})
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&serviceTemplate), &serviceTemplate)).To(Succeed())
					g.Expect(serviceTemplate.Status.Valid).To(BeFalse())
					g.Expect(serviceTemplate.Status.ValidationError).NotTo(BeEmpty())
				}, eventuallyTimeout, pollingInterval).Should(Succeed())
			})
		})

		It("should set service template state to valid if local source is ok: Secret", func() {
			By("creating secret", func() {
				secret.Data = map[string][]byte{
					"username": []byte("admin"),
					"password": []byte("password"),
				}
				Expect(k8sClient.Create(ctx, &secret)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &secret)
			})

			By("creating service template with secret source", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path:           ".",
						DeploymentType: "Remote",
						LocalSourceRef: &kcm.LocalSourceRef{
							Kind: "Secret",
							Name: secret.Name,
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &serviceTemplate)
			})

			By("reconciling service template", func() {
				Eventually(func(g Gomega) {
					_, _ = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&serviceTemplate)})
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&serviceTemplate), &serviceTemplate)).To(Succeed())
					g.Expect(serviceTemplate.Status.Valid).To(BeTrue())
					g.Expect(serviceTemplate.Status.ValidationError).To(BeEmpty())
				}, eventuallyTimeout, pollingInterval).Should(Succeed())
			})
		})

		It("should set service template state to valid if local source is ok: GitRepository", func() {
			By("creating git repository with ready state", func() {
				gitRepository.Spec = sourcev1.GitRepositorySpec{
					Interval: metav1.Duration{Duration: time.Second},
					URL:      "https://github.com/valid-git-repository/test.git",
				}
				Expect(k8sClient.Create(ctx, &gitRepository)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &gitRepository)
				gitRepository.Status.Conditions = append(gitRepository.Status.Conditions, metav1.Condition{
					Type:               kcm.ReadyCondition,
					Status:             metav1.ConditionTrue,
					Reason:             "Ready",
					LastTransitionTime: metav1.NewTime(time.Now()),
				})
				Expect(k8sClient.Status().Update(ctx, &gitRepository)).To(Succeed())
			})

			By("creating service template with git repository source", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path:           ".",
						DeploymentType: "Remote",
						LocalSourceRef: &kcm.LocalSourceRef{
							Kind: "GitRepository",
							Name: gitRepository.Name,
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &serviceTemplate)
			})

			By("reconciling service template", func() {
				Eventually(func(g Gomega) {
					_, _ = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&serviceTemplate)})
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&serviceTemplate), &serviceTemplate)).To(Succeed())
					g.Expect(serviceTemplate.Status.Valid).To(BeTrue())
					g.Expect(serviceTemplate.Status.ValidationError).To(BeEmpty())
				}, eventuallyTimeout, pollingInterval).Should(Succeed())
			})
		})

		It("should reconcile service template with remote source: GitRepository", func() {
			By("creating service template with remote git repository source", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path:           ".",
						DeploymentType: "Remote",
						RemoteSourceSpec: &kcm.RemoteSourceSpec{
							Git: &kcm.EmbeddedGitRepositorySpec{
								GitRepositorySpec: sourcev1.GitRepositorySpec{
									URL: "https://github.com/valid-git-repository/test.git",
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &serviceTemplate)
			})

			By("reconciling service template", func() {
				Eventually(func(g Gomega) {
					_, _ = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&serviceTemplate)})
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&serviceTemplate), &serviceTemplate)).To(Succeed())
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&serviceTemplate), &gitRepository)).To(Succeed())
					g.Expect(serviceTemplate.Status.SourceStatus).NotTo(BeNil())
					g.Expect(serviceTemplate.Status.SourceStatus.Kind).To(Equal(sourcev1.GitRepositoryKind))
					g.Expect(serviceTemplate.Status.SourceStatus.Name).To(Equal(serviceTemplate.Name))
					g.Expect(serviceTemplate.Status.SourceStatus.Namespace).To(Equal(serviceTemplate.Namespace))
					g.Expect(serviceTemplate.Status.Valid).To(BeFalse())
					g.Expect(serviceTemplate.Status.ValidationError).NotTo(BeEmpty())
				}, eventuallyTimeout, pollingInterval).Should(Succeed())
			})

			By("updating git repository with ready state", func() {
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&serviceTemplate), &gitRepository)).To(Succeed())
				gitRepository.Status.Conditions = append(gitRepository.Status.Conditions, metav1.Condition{
					Type:               kcm.ReadyCondition,
					Status:             metav1.ConditionTrue,
					Reason:             "Ready",
					LastTransitionTime: metav1.NewTime(time.Now()),
				})
				Expect(k8sClient.Status().Update(ctx, &gitRepository)).To(Succeed())
			})

			By("reconciling service template to ensure it's in valid state", func() {
				Eventually(func(g Gomega) {
					_, _ = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&serviceTemplate)})
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&serviceTemplate), &serviceTemplate)).To(Succeed())
					g.Expect(serviceTemplate.Status.Valid).To(BeTrue())
					g.Expect(serviceTemplate.Status.ValidationError).To(BeEmpty())
				}, eventuallyTimeout, pollingInterval).Should(Succeed())
			})
		})

		It("should reconcile service template with remote source: OCIRepository", func() {
			By("creating service template with remote oci repository source", func() {
				serviceTemplate.Spec = kcm.ServiceTemplateSpec{
					Kustomize: &kcm.SourceSpec{
						Path:           ".",
						DeploymentType: "Remote",
						RemoteSourceSpec: &kcm.RemoteSourceSpec{
							OCI: &kcm.EmbeddedOCIRepositorySpec{
								OCIRepositorySpec: sourcev1.OCIRepositorySpec{
									URL: "oci://ghcr.io/test/test",
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, &serviceTemplate)).To(Succeed())
				DeferCleanup(k8sClient.Delete, &serviceTemplate)
			})

			By("reconciling service template", func() {
				Eventually(func(g Gomega) {
					_, _ = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&serviceTemplate)})
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&serviceTemplate), &serviceTemplate)).To(Succeed())
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&serviceTemplate), &ociRepository)).To(Succeed())
					g.Expect(serviceTemplate.Status.SourceStatus).NotTo(BeNil())
					g.Expect(serviceTemplate.Status.SourceStatus.Kind).To(Equal(sourcev1.OCIRepositoryKind))
					g.Expect(serviceTemplate.Status.SourceStatus.Name).To(Equal(serviceTemplate.Name))
					g.Expect(serviceTemplate.Status.SourceStatus.Namespace).To(Equal(serviceTemplate.Namespace))
					g.Expect(serviceTemplate.Status.Valid).To(BeFalse())
					g.Expect(serviceTemplate.Status.ValidationError).NotTo(BeEmpty())
				}, eventuallyTimeout, pollingInterval).Should(Succeed())
			})

			By("updating oci repository with not ready state", func() {
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&serviceTemplate), &ociRepository)).To(Succeed())
				ociRepository.Status.Conditions = append(ociRepository.Status.Conditions, metav1.Condition{
					Type:               kcm.ReadyCondition,
					Status:             metav1.ConditionFalse,
					Reason:             "NotReady",
					LastTransitionTime: metav1.NewTime(time.Now()),
				})
				Expect(k8sClient.Status().Update(ctx, &ociRepository)).To(Succeed())
			})

			By("reconciling service template to ensure it's in invalid state", func() {
				Eventually(func(g Gomega) {
					_, _ = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&serviceTemplate)})
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&serviceTemplate), &serviceTemplate)).To(Succeed())
					g.Expect(serviceTemplate.Status.Valid).To(BeFalse())
					g.Expect(serviceTemplate.Status.ValidationError).NotTo(BeEmpty())
				}, eventuallyTimeout, pollingInterval).Should(Succeed())
			})
		})
	})
})
