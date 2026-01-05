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

package e2e

import (
	"context"
	"fmt"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	"github.com/K0rdent/kcm/test/e2e/flux"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	mcse2e "github.com/K0rdent/kcm/test/e2e/multiclusterservice"
	"github.com/K0rdent/kcm/test/e2e/templates"
)

var _ = Context("Mothership Cluster", Label("mothership"), func() {
	_ = Describe("MultiClusterService", Label("mothership:mcs"), Ordered, ContinueOnFailure, func() {
		var (
			ctx                  context.Context
			afterEachDeleteFuncs []func() error
			afterAllDeleteFuncs  []func() error
		)

		BeforeAll(func() {
			ctx = context.Background()
			kc = kubeclient.NewFromLocal(kubeutil.DefaultSystemNamespace)

			// Create k0rdent-catalog HelmRepository.
			helmRepoDelete := flux.CreateHelmRepositoryWithDelete(ctx, kc.CrClient, kubeutil.CurrentNamespace(), "k0rdent-catalog", sourcev1.HelmRepositorySpec{
				Insecure: true,
				Provider: sourcev1.BucketProviderGeneric,
				Type:     sourcev1.HelmRepositoryTypeOCI,
				URL:      "oci://ghcr.io/k0rdent/catalog/charts",
			})

			fn := templates.CreateServiceTemplateWithDelete(ctx, kc.CrClient, kubeutil.CurrentNamespace(), "ingress-nginx-4-11-3", kcmv1.ServiceTemplateSpec{
				Helm: &kcmv1.HelmSpec{
					ChartSpec: &sourcev1.HelmChartSpec{
						Chart:   "ingress-nginx",
						Version: "4.13.0",
						SourceRef: sourcev1.LocalHelmChartSourceReference{
							Kind: "HelmRepository",
							Name: "k0rdent-catalog",
						},
					},
				},
			})
			afterAllDeleteFuncs = append(afterAllDeleteFuncs, fn)

			fn = templates.CreateServiceTemplateWithDelete(ctx, kc.CrClient, kubeutil.CurrentNamespace(), "postgres-operator-1-14-0", kcmv1.ServiceTemplateSpec{
				Helm: &kcmv1.HelmSpec{
					ChartSpec: &sourcev1.HelmChartSpec{
						Chart:   "postgres-operator",
						Version: "1.14.0",
						SourceRef: sourcev1.LocalHelmChartSourceReference{
							Kind: "HelmRepository",
							Name: "k0rdent-catalog",
						},
					},
				},
			})
			afterAllDeleteFuncs = append(afterAllDeleteFuncs, fn)

			fn = templates.CreateServiceTemplateWithDelete(ctx, kc.CrClient, kubeutil.CurrentNamespace(), "external-secrets-0-18-2", kcmv1.ServiceTemplateSpec{
				Helm: &kcmv1.HelmSpec{
					ChartSpec: &sourcev1.HelmChartSpec{
						Chart:   "external-secrets",
						Version: "0.18.2",
						SourceRef: sourcev1.LocalHelmChartSourceReference{
							Kind: "HelmRepository",
							Name: "k0rdent-catalog",
						},
					},
				},
			})
			afterAllDeleteFuncs = append(afterAllDeleteFuncs, fn)

			// HelmRepository should be deleted at the end.
			afterAllDeleteFuncs = append(afterAllDeleteFuncs, helmRepoDelete)
		})

		AfterEach(func() {
			for _, fn := range afterEachDeleteFuncs {
				if fn != nil {
					err := fn()
					Expect(err).NotTo(HaveOccurred())
				}
			}
		})

		AfterAll(func() {
			for _, fn := range afterAllDeleteFuncs {
				if fn != nil {
					err := fn()
					Expect(err).NotTo(HaveOccurred())
				}
			}
		})

		It("deploy empty MCS", func() {
			Skip("Skipping because of https://github.com/k0rdent/kcm/issues/2183")
			afterEachDeleteFuncs = []func() error{}

			mcs := buildSelfManagementMCS("mcs1", nil, kcmv1.ServiceSpec{})
			fn := mcse2e.CreateMultiClusterServiceWithDelete(ctx, kc.CrClient, mcs)
			afterEachDeleteFuncs = append(afterEachDeleteFuncs, fn)

			// TODO(https://github.com/k0rdent/kcm/issues/2183):
			// This is skipped because sometimes the following validation fails.
			// It seems to happen intermittently which suggests there is some
			// fine tuning to be done in the reconcile loop.
			mcse2e.ValidateMultiClusterService(ctx, kc, mcs.GetName(), 1)
		})

		It("deploy MCS with a service", func() {
			afterEachDeleteFuncs = []func() error{}

			mcs := buildSelfManagementMCS("mcs1", nil,
				kcmv1.ServiceSpec{
					Services: []kcmv1.Service{
						{
							Template:  "ingress-nginx-4-11-3",
							Name:      "ingress-nginx",
							Namespace: "ingress-nginx",
						},
					},
				},
			)
			fn := mcse2e.CreateMultiClusterServiceWithDelete(ctx, kc.CrClient, mcs)
			afterEachDeleteFuncs = append(afterEachDeleteFuncs, fn)
			mcse2e.ValidateMultiClusterService(ctx, kc, mcs.GetName(), 1)
			mcse2e.ValidateServiceSet(ctx, kc.CrClient, kubeutil.CurrentNamespace(), nil, mcs)
		})

		It("deploy mcs2->mcs1", func() {
			afterEachDeleteFuncs = []func() error{}

			mcs2 := buildSelfManagementMCS("mcs2", []string{"mcs1"},
				kcmv1.ServiceSpec{
					Services: []kcmv1.Service{
						{
							Template:  "ingress-nginx-4-11-3",
							Name:      "ingress-nginx",
							Namespace: "ingress-nginx",
						},
					},
				},
			)
			mcs2Delete := mcse2e.CreateMultiClusterServiceWithDelete(ctx, kc.CrClient, mcs2)
			mcse2e.ValidateMCSConditions(ctx, kc.CrClient, client.ObjectKeyFromObject(mcs2), []metav1.Condition{
				{
					// At this point this condition for mcs2 should
					// have failed status because mcs1 doesn't exist yet.
					Type:   kcmv1.MultiClusterServiceDependencyValidationCondition,
					Status: metav1.ConditionFalse,
				},
			})

			mcs1 := buildSelfManagementMCS("mcs1", nil,
				kcmv1.ServiceSpec{
					Services: []kcmv1.Service{
						{
							Template:  "postgres-operator-1-14-0",
							Name:      "postgres-operator",
							Namespace: "postgres-operator",
						},
					},
				},
			)
			_ = mcse2e.CreateMultiClusterServiceWithDelete(ctx, kc.CrClient, mcs1)
			mcse2e.ValidateMultiClusterService(ctx, kc, mcs1.GetName(), 1)
			mcse2e.ValidateServiceSet(ctx, kc.CrClient, kubeutil.CurrentNamespace(), nil, mcs1)

			// Validate mcs2 now that all services of mcs1 have been deployed.
			mcse2e.ValidateMultiClusterService(ctx, kc, mcs2.GetName(), 1)
			mcse2e.ValidateServiceSet(ctx, kc.CrClient, kubeutil.CurrentNamespace(), nil, mcs2)

			// Delete mcs1.
			Expect(client.IgnoreNotFound(kc.CrClient.Delete(ctx, mcs1))).NotTo(HaveOccurred())
			mcse2e.ValidateMCSConditions(ctx, kc.CrClient, client.ObjectKeyFromObject(mcs1), []metav1.Condition{
				{
					// The delete validation on mcs1 should fail with
					// this condition because mcs2 still depends on it.
					Type:   kcmv1.MultiClusterServiceDependencyValidationCondition,
					Status: metav1.ConditionFalse,
				},
			})

			Expect(mcs2Delete()).NotTo(HaveOccurred())

			// Now that mcs2 is deleted there shouldn't by any MCS
			// depending on mcs1 so it should be successfully deleted.
			Eventually(func() bool {
				_, err := mcse2e.GetMultiClusterService(ctx, kc.CrClient, client.ObjectKeyFromObject(mcs1))
				return apierrors.IsNotFound(err)
			}).WithTimeout(5 * time.Minute).WithPolling(3 * time.Second).Should(BeTrue())
			_, _ = fmt.Fprintf(GinkgoWriter, "Deleted MultiClusterService %s\n", client.ObjectKeyFromObject(mcs1))
		})

		It("deploy mcs3->(mcs1,mcs2)", func() {
			afterEachDeleteFuncs = []func() error{}

			mcs3 := buildSelfManagementMCS("mcs3", []string{"mcs1", "mcs2"},
				kcmv1.ServiceSpec{
					Services: []kcmv1.Service{
						{
							Template:  "ingress-nginx-4-11-3",
							Name:      "ingress-nginx",
							Namespace: "ingress-nginx",
						},
					},
				},
			)
			mcs3Delete := mcse2e.CreateMultiClusterServiceWithDelete(ctx, kc.CrClient, mcs3)
			mcse2e.ValidateMCSConditions(ctx, kc.CrClient, client.ObjectKeyFromObject(mcs3), []metav1.Condition{
				{
					// At this point this condition for mcs3 should
					// have failed status because mcs1 & mcs2 don't exist yet.
					Type:   kcmv1.MultiClusterServiceDependencyValidationCondition,
					Status: metav1.ConditionFalse,
				},
			})

			mcs1 := buildSelfManagementMCS("mcs1", nil,
				kcmv1.ServiceSpec{
					Services: []kcmv1.Service{
						{
							Template:  "postgres-operator-1-14-0",
							Name:      "postgres-operator",
							Namespace: "postgres-operator",
						},
					},
				},
			)
			_ = mcse2e.CreateMultiClusterServiceWithDelete(ctx, kc.CrClient, mcs1)
			mcse2e.ValidateMultiClusterService(ctx, kc, mcs1.GetName(), 1)
			mcse2e.ValidateServiceSet(ctx, kc.CrClient, kubeutil.CurrentNamespace(), nil, mcs1)

			mcs2 := buildSelfManagementMCS("mcs2", nil,
				kcmv1.ServiceSpec{
					Services: []kcmv1.Service{
						{
							Template:  "external-secrets-0-18-2",
							Name:      "external-secrets",
							Namespace: "external-secrets",
						},
					},
				},
			)
			_ = mcse2e.CreateMultiClusterServiceWithDelete(ctx, kc.CrClient, mcs2)
			mcse2e.ValidateMultiClusterService(ctx, kc, mcs2.GetName(), 1)
			mcse2e.ValidateServiceSet(ctx, kc.CrClient, kubeutil.CurrentNamespace(), nil, mcs2)

			// Validate mcs3 now that all services of mcs1 & mcs2 have been deployed.
			mcse2e.ValidateMultiClusterService(ctx, kc, mcs3.GetName(), 1)
			mcse2e.ValidateServiceSet(ctx, kc.CrClient, kubeutil.CurrentNamespace(), nil, mcs3)

			// Delete mcs1.
			Expect(client.IgnoreNotFound(kc.CrClient.Delete(ctx, mcs1))).NotTo(HaveOccurred())
			mcse2e.ValidateMCSConditions(ctx, kc.CrClient, client.ObjectKeyFromObject(mcs1), []metav1.Condition{
				{
					// The delete validation on mcs1 should fail with
					// this condition because mcs3 still depends on it.
					Type:   kcmv1.MultiClusterServiceDependencyValidationCondition,
					Status: metav1.ConditionFalse,
				},
			})

			// Delete mcs2.
			Expect(client.IgnoreNotFound(kc.CrClient.Delete(ctx, mcs2))).NotTo(HaveOccurred())
			mcse2e.ValidateMCSConditions(ctx, kc.CrClient, client.ObjectKeyFromObject(mcs2), []metav1.Condition{
				{
					// The delete validation on mcs2 should fail with
					// this condition because mcs3 still depends on it.
					Type:   kcmv1.MultiClusterServiceDependencyValidationCondition,
					Status: metav1.ConditionFalse,
				},
			})

			Expect(mcs3Delete()).NotTo(HaveOccurred())

			// Now that mcs3 is deleted there shouldn't by any MCS
			// depending on mcs1 so it should be successfully deleted.
			Eventually(func() bool {
				_, err := mcse2e.GetMultiClusterService(ctx, kc.CrClient, client.ObjectKeyFromObject(mcs1))
				return apierrors.IsNotFound(err)
			}).WithTimeout(5 * time.Minute).WithPolling(3 * time.Second).Should(BeTrue())
			_, _ = fmt.Fprintf(GinkgoWriter, "Deleted MultiClusterService %s\n", client.ObjectKeyFromObject(mcs1))

			// Now that mcs3 is deleted there shouldn't by any MCS
			// depending on mcs2 so it should be successfully deleted.
			Eventually(func() bool {
				_, err := mcse2e.GetMultiClusterService(ctx, kc.CrClient, client.ObjectKeyFromObject(mcs2))
				return apierrors.IsNotFound(err)
			}).WithTimeout(5 * time.Minute).WithPolling(3 * time.Second).Should(BeTrue())
			_, _ = fmt.Fprintf(GinkgoWriter, "Deleted MultiClusterService %s\n", client.ObjectKeyFromObject(mcs2))
		})

		It("deploy (mcs1,mcs2)->mcs3", func() {
			afterEachDeleteFuncs = []func() error{}

			mcs1 := buildSelfManagementMCS("mcs1", []string{"mcs3"},
				kcmv1.ServiceSpec{
					Services: []kcmv1.Service{
						{
							Template:  "ingress-nginx-4-11-3",
							Name:      "ingress-nginx",
							Namespace: "ingress-nginx",
						},
					},
				},
			)
			mcs1Delete := mcse2e.CreateMultiClusterServiceWithDelete(ctx, kc.CrClient, mcs1)
			mcse2e.ValidateMCSConditions(ctx, kc.CrClient, client.ObjectKeyFromObject(mcs1), []metav1.Condition{
				{
					// At this point this condition for mcs1 should
					// have failed status because mcs3 don't exist yet.
					Type:   kcmv1.MultiClusterServiceDependencyValidationCondition,
					Status: metav1.ConditionFalse,
				},
			})

			mcs2 := buildSelfManagementMCS("mcs2", []string{"mcs3"},
				kcmv1.ServiceSpec{
					Services: []kcmv1.Service{
						{
							Template:  "postgres-operator-1-14-0",
							Name:      "postgres-operator",
							Namespace: "postgres-operator",
						},
					},
				},
			)
			mcs2Delete := mcse2e.CreateMultiClusterServiceWithDelete(ctx, kc.CrClient, mcs2)
			mcse2e.ValidateMCSConditions(ctx, kc.CrClient, client.ObjectKeyFromObject(mcs2), []metav1.Condition{
				{
					// At this point this condition for mcs2 should
					// have failed status because mcs3 don't exist yet.
					Type:   kcmv1.MultiClusterServiceDependencyValidationCondition,
					Status: metav1.ConditionFalse,
				},
			})

			mcs3 := buildSelfManagementMCS("mcs3", nil,
				kcmv1.ServiceSpec{
					Services: []kcmv1.Service{
						{
							Template:  "external-secrets-0-18-2",
							Name:      "external-secrets",
							Namespace: "external-secrets",
						},
					},
				},
			)
			_ = mcse2e.CreateMultiClusterServiceWithDelete(ctx, kc.CrClient, mcs3)
			mcse2e.ValidateMultiClusterService(ctx, kc, mcs3.GetName(), 1)
			mcse2e.ValidateServiceSet(ctx, kc.CrClient, kubeutil.CurrentNamespace(), nil, mcs3)

			// Validate mcs1 now that all services of mcs3 have been deployed.
			mcse2e.ValidateMultiClusterService(ctx, kc, mcs1.GetName(), 1)
			mcse2e.ValidateServiceSet(ctx, kc.CrClient, kubeutil.CurrentNamespace(), nil, mcs1)

			// Validate mcs2 now that all services of mcs3 have been deployed.
			mcse2e.ValidateMultiClusterService(ctx, kc, mcs2.GetName(), 1)
			mcse2e.ValidateServiceSet(ctx, kc.CrClient, kubeutil.CurrentNamespace(), nil, mcs2)

			// Delete mcs3.
			Expect(client.IgnoreNotFound(kc.CrClient.Delete(ctx, mcs3))).NotTo(HaveOccurred())
			mcse2e.ValidateMCSConditions(ctx, kc.CrClient, client.ObjectKeyFromObject(mcs3), []metav1.Condition{
				{
					// The delete validation on mcs3 should fail with
					// this condition because mcs1 & mcs2 still depend on it.
					Type:   kcmv1.MultiClusterServiceDependencyValidationCondition,
					Status: metav1.ConditionFalse,
				},
			})

			Expect(mcs1Delete()).NotTo(HaveOccurred())
			Expect(mcs2Delete()).NotTo(HaveOccurred())

			// Now that mcs1 & mcs2 are deleted there shouldn't by any MCS
			// depending on mcs3 so it should be successfully deleted.
			Eventually(func() bool {
				_, err := mcse2e.GetMultiClusterService(ctx, kc.CrClient, client.ObjectKeyFromObject(mcs3))
				return apierrors.IsNotFound(err)
			}).WithTimeout(5 * time.Minute).WithPolling(3 * time.Second).Should(BeTrue())
			_, _ = fmt.Fprintf(GinkgoWriter, "Deleted MultiClusterService %s\n", client.ObjectKeyFromObject(mcs3))
		})
	})
})

func buildSelfManagementMCS(name string, dependsOn []string, serviceSpec kcmv1.ServiceSpec) *kcmv1.MultiClusterService {
	mcs := buildMCS(name, map[string]string{
		kcmv1.K0rdentManagementClusterLabelKey: kcmv1.K0rdentManagementClusterLabelValue,
	}, dependsOn, serviceSpec)
	mcs.Spec.ServiceSpec.Provider.SelfManagement = true
	return mcs
}

func buildMCS(name string, matchLabels map[string]string, dependsOn []string, serviceSpec kcmv1.ServiceSpec) *kcmv1.MultiClusterService {
	return &kcmv1.MultiClusterService{
		TypeMeta: metav1.TypeMeta{
			Kind: kcmv1.MultiClusterServiceKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: kcmv1.MultiClusterServiceSpec{
			ClusterSelector: metav1.LabelSelector{
				MatchLabels: matchLabels,
			},
			DependsOn:   dependsOn,
			ServiceSpec: serviceSpec,
		},
	}
}
