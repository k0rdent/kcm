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

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/serviceset"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/config"
	"github.com/K0rdent/kcm/test/e2e/credential"
	"github.com/K0rdent/kcm/test/e2e/flux"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	"github.com/K0rdent/kcm/test/e2e/multiclusterservice"
	"github.com/K0rdent/kcm/test/e2e/templates"
)

const (
	helmRepositoryName   = "k0rdent-catalog"
	templateChainName    = "ingress-nginx"
	nginxChartName       = "ingress-nginx"
	openCostChartName    = "opencost"
	openCostChartVersion = "2.3.2"
	openWebuiChartName   = "open-webui"
	openWebuiVersion     = "8.10.0"
	nginxServiceName     = "managed-ingress-nginx"
	validatorTimeout     = 30 * time.Minute
	validatorPoll        = 5 * time.Second
)

var _ = Describe("Functional e2e tests", Label("provider:cloud", "provider:docker"), Ordered, ContinueOnFailure, func() {
	var (
		clusterName       string
		clusterDeleteFunc func() error

		sharedSD             *kcmv1.ClusterDeployment
		sharedDeleteFn       func() error
		helmRepositorySpec   sourcev1.HelmRepositorySpec
		serviceTemplateSpecs []kcmv1.ServiceTemplateSpec
		supportedTemplates   []kcmv1.SupportedTemplate
	)

	nginxVersions := []string{"4.11.3", "4.11.5", "4.12.3", "4.13.0"}
	multiClusterServiceTemplate := fmt.Sprintf("%s-%s", openCostChartName, strings.ReplaceAll(openCostChartVersion, ".", "-"))

	BeforeAll(func() {
		By("Creating kube client")
		kc = kubeclient.NewFromLocal(kubeutil.DefaultSystemNamespace)

		var err error
		clusterTemplates, err = templates.GetSortedClusterTemplates(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace)
		Expect(err).NotTo(HaveOccurred())

		By("Providing cluster identity and credentials")
		credential.Apply("", "docker")

		helmRepositorySpec = sourcev1.HelmRepositorySpec{
			Type: "oci",
			URL:  "oci://ghcr.io/k0rdent/catalog/charts",
		}

		serviceTemplateSpecs = make([]kcmv1.ServiceTemplateSpec, 0)
		serviceTemplateSpecs = append(serviceTemplateSpecs, kcmv1.ServiceTemplateSpec{
			Helm: &kcmv1.HelmSpec{
				ChartSpec: &sourcev1.HelmChartSpec{
					Chart: openCostChartName,
					SourceRef: sourcev1.LocalHelmChartSourceReference{
						Kind: sourcev1.HelmRepositoryKind,
						Name: helmRepositoryName,
					},
					Version: openCostChartVersion,
				},
			},
		})
		serviceTemplateSpecs = append(serviceTemplateSpecs, kcmv1.ServiceTemplateSpec{
			Helm: &kcmv1.HelmSpec{
				ChartSpec: &sourcev1.HelmChartSpec{
					Chart: openWebuiChartName,
					SourceRef: sourcev1.LocalHelmChartSourceReference{
						Kind: sourcev1.HelmRepositoryKind,
						Name: helmRepositoryName,
					},
					Version: openWebuiVersion,
				},
			},
		})

		for i, v := range nginxVersions {
			name := fmt.Sprintf("%s-%s", nginxChartName, strings.ReplaceAll(v, ".", "-"))
			serviceTemplateSpecs = append(serviceTemplateSpecs, kcmv1.ServiceTemplateSpec{
				Helm: &kcmv1.HelmSpec{
					ChartSpec: &sourcev1.HelmChartSpec{
						Chart: nginxChartName,
						SourceRef: sourcev1.LocalHelmChartSourceReference{
							Kind: sourcev1.HelmRepositoryKind,
							Name: helmRepositoryName,
						},
						Version: v,
					},
				},
			})

			var upgrades []kcmv1.AvailableUpgrade
			for j := i + 1; j < len(nginxVersions); j++ {
				nv := nginxVersions[j]
				upgrades = append(upgrades, kcmv1.AvailableUpgrade{
					Name:    fmt.Sprintf("%s-%s", nginxChartName, strings.ReplaceAll(nv, ".", "-")),
					Version: nv,
				})
			}

			supportedTemplates = append(supportedTemplates, kcmv1.SupportedTemplate{
				Name:              name,
				AvailableUpgrades: upgrades,
			})
		}

		By("creating HelmRepository and ServiceTemplate")
		flux.CreateHelmRepository(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace, helmRepositoryName, helmRepositorySpec)
		for _, serviceTemplateSpec := range serviceTemplateSpecs {
			serviceTemplateName := fmt.Sprintf("%s-%s", serviceTemplateSpec.Helm.ChartSpec.Chart, strings.ReplaceAll(serviceTemplateSpec.Helm.ChartSpec.Version, ".", "-"))
			templates.CreateServiceTemplate(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace, serviceTemplateName, serviceTemplateSpec)
		}
		templates.CreateTemplateChain(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace, templateChainName, kcmv1.TemplateChainSpec{
			SupportedTemplates: supportedTemplates,
		})

		By("Creating shared cluster for tests that don't require custom service dependency specs")
		sharedClusterName := clusterdeployment.GenerateClusterName("docker-shared")
		sharedSD, sharedDeleteFn = createAndWaitCluster(context.Background(), kc, sharedClusterName)
	})

	AfterEach(func() {
		if clusterDeleteFunc != nil {
			err := clusterDeleteFunc()
			clusterDeleteFunc = nil
			Expect(err).NotTo(HaveOccurred(), "failed to delete cluster")
		}
	})

	AfterAll(func() {
		if CurrentSpecReport().Failed() && cleanup() {
			By("Collecting the support bundle from the management cluster")
			logs.SupportBundle(kc, "")
		}

		if cleanup() {
			By("Deleting resources")
			if sharedDeleteFn != nil {
				By(fmt.Sprintf("Deleting shared ClusterDeployment %s", sharedSD.Name))
				Expect(sharedDeleteFn()).NotTo(HaveOccurred())
				sharedDeleteFn = nil
			}
			if clusterDeleteFunc != nil {
				err := clusterDeleteFunc()
				Expect(err).NotTo(HaveOccurred())
			}
		}
	})

	for i, cfg := range config.Config[config.TestingProviderDocker] {
		It("MultiCluster services no longer match", func() {
			const (
				multiClusterServiceName       = "test-multicluster"
				multiClusterServiceMatchLabel = "k0rdent.mirantis.com/test-cluster-name"
			)

			defer GinkgoRecover()
			ctx := context.Background()
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))

			sd := sharedSD

			mcs := multiclusterservice.BuildMultiClusterService(sd, multiClusterServiceTemplate, openCostChartName, multiClusterServiceMatchLabel, multiClusterServiceName)
			multiclusterservice.CreateMultiClusterService(ctx, kc.CrClient, mcs)
			multiclusterservice.ValidateMultiClusterService(ctx, kc, multiClusterServiceName, 1)

			updateClusterDeploymentLabel(ctx, kc.CrClient, sd, multiClusterServiceMatchLabel, "not-matched")
			multiclusterservice.ValidateMultiClusterService(ctx, kc, multiClusterServiceName, 0)

			multiclusterservice.DeleteMultiClusterService(ctx, kc.CrClient, mcs)

			updateClusterDeploymentLabel(ctx, kc.CrClient, sd, multiClusterServiceMatchLabel, sd.Name)
		})

		It("Performing sequential upgrades", func() {
			defer GinkgoRecover()
			ctx := context.Background()
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))

			sd := sharedSD

			waitForServiceDeployments(ctx, kc, sd, sd.Spec.ServiceSpec.Services)

			waitUpgrade := startWatchingServiceSetVersions(ctx, kc, sd.Name, sd.Namespace, []string{
				nginxVersions[1],
				nginxVersions[2],
			})
			updateClusterDeploymentTemplate(ctx, sd, nginxVersions[2])
			waitUpgrade()

			waitDowngrade := startWatchingServiceSetVersions(ctx, kc, sd.Name, sd.Namespace, []string{nginxVersions[0]})
			updateClusterDeploymentTemplate(ctx, sd, nginxVersions[0])
			waitDowngrade()

			serviceSet := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sd.Name,
					Namespace: sd.Namespace,
				},
			}
			Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet)).NotTo(HaveOccurred(), "failed to fetch ServiceSet")
			Expect(serviceSet.Spec.Services).To(HaveLen(1))
		})

		It("Performing upgrades with dependent services", func() {
			defer GinkgoRecover()
			ctx := context.Background()
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))
			clusterName = clusterdeployment.GenerateClusterName(fmt.Sprintf("docker-%d-dep", i))

			serviceName := fmt.Sprintf("%s-%s", openCostChartName, strings.ReplaceAll(openCostChartVersion, ".", "-"))
			sd := clusterdeployment.Generate(templates.TemplateDockerCluster, clusterName, templates.FindLatestTemplatesWithType(clusterTemplates, templates.TemplateDockerCluster, 1)[0])

			setK0smotronNodePorts(sd, 30543, 30232)
			sd.Spec.ServiceSpec.Services[0].TemplateChain = templateChainName
			sd.Spec.ServiceSpec.Services[0].DependsOn = []kcmv1.ServiceDependsOn{
				{
					Name: serviceName,
				},
			}

			sd.Spec.ServiceSpec.Services = append(sd.Spec.ServiceSpec.Services,
				kcmv1.Service{
					Name:      openWebuiChartName,
					Template:  fmt.Sprintf("%s-%s", openWebuiChartName, strings.ReplaceAll(openWebuiVersion, ".", "-")),
					DependsOn: []kcmv1.ServiceDependsOn{{Name: serviceName}},
				})

			sd.Spec.ServiceSpec.Services = append(sd.Spec.ServiceSpec.Services,
				kcmv1.Service{
					Name:     serviceName,
					Template: fmt.Sprintf("%s-%s", openCostChartName, strings.ReplaceAll(openCostChartVersion, ".", "-")),
				})

			By(fmt.Sprintf("Deploying cluster deployment :%v", sd))
			deleteFn := clusterdeployment.Create(ctx, kc.CrClient, sd)

			clusterDeleteFunc = func() error { //nolint:unparam
				By(fmt.Sprintf("Deleting ClusterDeployment %s", clusterName))
				Expect(deleteFn()).NotTo(HaveOccurred(), "failed to delete cluster")

				By(fmt.Sprintf("Verifying ClusterDeployment %s deletion", clusterName))
				validator := clusterdeployment.NewProviderValidator(templates.TemplateDockerCluster, clusterName, clusterdeployment.ValidationActionDelete)
				Eventually(func() error { return validator.Validate(ctx, kc) }, validatorTimeout, validatorPoll).Should(Succeed())
				return nil
			}

			templateBy(templates.TemplateDockerCluster, "Waiting for infrastructure to deploy successfully")
			deployValidator := clusterdeployment.NewProviderValidator(templates.TemplateDockerCluster, clusterName, clusterdeployment.ValidationActionDeploy)
			Eventually(func() error { return deployValidator.Validate(ctx, kc) }, validatorTimeout, validatorPoll).Should(Succeed())

			waitForServiceDeployments(ctx, kc, sd, sd.Spec.ServiceSpec.Services)

			waitUpgrade := startWatchingServiceSetVersions(ctx, kc, sd.Name, sd.Namespace, []string{
				nginxVersions[1],
				nginxVersions[2],
			})
			updateClusterDeploymentTemplate(ctx, sd, nginxVersions[2])
			waitUpgrade()

			serviceSet := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sd.Name,
					Namespace: sd.Namespace,
				},
			}
			Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet)).NotTo(HaveOccurred(), "failed to fetch ServiceSet")
			Expect(serviceSet.Spec.Services).To(HaveLen(3))

			waitDowngrade := startWatchingServiceSetVersions(ctx, kc, sd.Name, sd.Namespace, []string{nginxVersions[0]})
			updateClusterDeploymentTemplate(ctx, sd, nginxVersions[0])
			waitDowngrade()

			Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet)).NotTo(HaveOccurred(), "failed to fetch ServiceSet")
			Expect(serviceSet.Spec.Services).To(HaveLen(3))
			Expect(clusterDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete cluster")
			clusterDeleteFunc = nil
		})

		It("Pause service deployment", func() {
			defer GinkgoRecover()
			ctx := context.Background()
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))

			sd := sharedSD

			waitForServiceDeployments(ctx, kc, sd, sd.Spec.ServiceSpec.Services)

			serviceSet := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sd.Name,
					Namespace: sd.Namespace,
				},
			}
			Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet)).NotTo(HaveOccurred(), "failed to fetch ServiceSet")
			Expect(serviceSet.Spec.Services).To(HaveLen(1))

			Eventually(func() error {
				if err := kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet); err != nil {
					return err
				}
				serviceSet.SetAnnotations(map[string]string{
					kcmv1.ServiceSetPausedAnnotation: "true",
				})
				return kc.CrClient.Update(ctx, serviceSet)
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "failed to set pause annotation on ServiceSet")
			updateClusterDeploymentTemplate(ctx, sd, nginxVersions[2])

			Eventually(ctx, func() error {
				profile := addoncontrollerv1beta1.Profile{}
				if err := kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), &profile); err != nil {
					return fmt.Errorf("failed to get Profile: %w", err)
				}
				_, ok := profile.Annotations[addoncontrollerv1beta1.ProfilePausedAnnotation]
				if !ok {
					return fmt.Errorf("paused annotation not yet propagated to Profile %s", profile.Name)
				}
				return nil
			}, 10*time.Minute, 5*time.Second).Should(Succeed())

			Eventually(func() error {
				if err := kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet); err != nil {
					return err
				}
				serviceSet.SetAnnotations(map[string]string{})
				return kc.CrClient.Update(ctx, serviceSet)
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "failed to clear pause annotation on ServiceSet")
		})

		// TODO sveltos currently doesn't update the status of the helmReleaseSummaries when
		// a deployed helm release is updated to be invalid
		XIt("Invalid multicluster service test", func() {
			const (
				multiClusterServiceName       = "test-multicluster"
				multiClusterServiceMatchLabel = "k0rdent.mirantis.com/test-cluster-name"
			)

			defer GinkgoRecover()
			ctx := context.Background()
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))
			clusterName = clusterdeployment.GenerateUniqueClusterName(fmt.Sprintf("docker-%d", i))
			sd, deleteFn := createAndWaitCluster(ctx, kc, clusterName)

			clusterDeleteFunc = deleteFn

			mcs := multiclusterservice.BuildMultiClusterService(sd, multiClusterServiceTemplate, openCostChartName, multiClusterServiceMatchLabel, multiClusterServiceName)
			mcsServiceSpec := mcs.Spec.ServiceSpec
			mcs.Spec.ServiceSpec = kcmv1.ServiceSpec{}
			multiclusterservice.CreateMultiClusterService(ctx, kc.CrClient, mcs)

			Eventually(func() error {
				Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(mcs), mcs)).NotTo(HaveOccurred(), "failed to fetch MulticlusterService")
				mcs.Spec.ServiceSpec = mcsServiceSpec
				return kc.CrClient.Update(ctx, mcs)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			multiclusterservice.ValidateMultiClusterService(ctx, kc, multiClusterServiceName, 1)

			waitForServiceDeployments(ctx, kc, sd, mcs.Spec.ServiceSpec.Services)
			serviceSetObjectKey := serviceset.ObjectKey(kubeutil.DefaultSystemNamespace, sd, mcs)
			validateClusterProfile(ctx, serviceSetObjectKey, mcs.Spec.ServiceSpec, kc)

			Eventually(func() error {
				Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(mcs), mcs)).NotTo(HaveOccurred(), "failed to fetch MulticlusterService")
				mcs.Spec.ServiceSpec.Services[0].Values = "invalid, abcd, not valid, not valid"
				Expect(kc.CrClient.Update(ctx, mcs)).NotTo(HaveOccurred())
				return nil
			}).WithTimeout(1 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			// TODO validate that the service set service in question is now marked as failed
		})

		It("Valid multicluster service test", func() {
			const (
				multiClusterServiceName       = "test-multicluster"
				multiClusterServiceMatchLabel = "k0rdent.mirantis.com/test-cluster-name"
			)

			defer GinkgoRecover()
			ctx := context.Background()
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))
			clusterName = clusterdeployment.GenerateUniqueClusterName(fmt.Sprintf("docker-%d", i))

			sd, deleteFn := createAndWaitCluster(ctx, kc, clusterName)
			waitForServiceDeployments(ctx, kc, sd, sd.Spec.ServiceSpec.Services)
			clusterDeleteFunc = deleteFn

			mcs := multiclusterservice.BuildMultiClusterService(sd, multiClusterServiceTemplate, openCostChartName, multiClusterServiceMatchLabel, multiClusterServiceName)
			mcsServiceSpec := mcs.Spec.ServiceSpec
			mcs.Spec.ServiceSpec = kcmv1.ServiceSpec{}
			multiclusterservice.CreateMultiClusterService(ctx, kc.CrClient, mcs)

			Eventually(func() error {
				Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(mcs), mcs)).NotTo(HaveOccurred(), "failed to fetch MulticlusterService")
				mcs.Spec.ServiceSpec = mcsServiceSpec
				return kc.CrClient.Update(ctx, mcs)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			serviceSetObjectKey := serviceset.ObjectKey(kubeutil.DefaultSystemNamespace, sd, mcs)
			waitForServiceSetTransition(ctx, kc, serviceSetObjectKey, mcs.Spec.ServiceSpec.Services)
			multiclusterservice.ValidateMultiClusterService(ctx, kc, multiClusterServiceName, 1)
			validateClusterProfile(ctx, serviceSetObjectKey, mcs.Spec.ServiceSpec, kc)

			Eventually(func() error {
				Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(mcs), mcs)).NotTo(HaveOccurred(), "failed to fetch MulticlusterService")
				mcs.Spec.ServiceSpec.Services[0].Values = "opencost:\n            ui:\n              enabled: false"
				Expect(kc.CrClient.Update(ctx, mcs)).NotTo(HaveOccurred())
				return nil
			}).WithTimeout(1 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			waitForServiceDeployments(ctx, kc, sd, mcs.Spec.ServiceSpec.Services)
			validateClusterProfile(ctx, serviceSetObjectKey, mcs.Spec.ServiceSpec, kc)

			multiclusterservice.DeleteMultiClusterService(ctx, kc.CrClient, mcs)
			Expect(clusterDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete cluster")
			clusterDeleteFunc = nil
		})
	}
})

func waitForServiceSetTransition(
	ctx context.Context,
	kc *kubeclient.KubeClient,
	key crclient.ObjectKey,
	expectedServices []kcmv1.Service,
) {
	serviceSet := &kcmv1.ServiceSet{}
	Eventually(func() error {
		if err := kc.CrClient.Get(ctx, key, serviceSet); err != nil {
			return fmt.Errorf("failed to get ServiceSet %s: %w", key, err)
		}
		trackedNames := make(map[string]struct{}, len(serviceSet.Status.Services))
		for _, s := range serviceSet.Status.Services {
			trackedNames[s.Name] = struct{}{}
		}
		for _, svc := range expectedServices {
			if _, ok := trackedNames[svc.Name]; !ok {
				return fmt.Errorf("service %q not yet tracked in ServiceSet %s", svc.Name, key)
			}
		}
		if !serviceSet.Status.Deployed {
			return fmt.Errorf("ServiceSet %s services tracked but not yet Deployed=true", key)
		}
		return nil
	}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

func validateClusterProfile(ctx context.Context, key crclient.ObjectKey, spec kcmv1.ServiceSpec, kc *kubeclient.KubeClient) {
	profile := new(addoncontrollerv1beta1.Profile)
	Eventually(func() error {
		Expect(kc.CrClient.Get(ctx, key, profile)).Error().NotTo(HaveOccurred(), "failed to fetch Profile")
		return validateProfileSpec(ctx, profile.Spec, spec, kc)
	}).WithTimeout(20 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

func validateProfileSpec(ctx context.Context, profileSpec addoncontrollerv1beta1.Spec, serviceSpec kcmv1.ServiceSpec, kc *kubeclient.KubeClient) error {
	serviceTemplatesToCharts := make(map[string]string)
	for _, svc := range serviceSpec.Services {
		svcTemplate := &kcmv1.ServiceTemplate{}
		if err := kc.CrClient.Get(ctx, crclient.ObjectKey{Namespace: kubeutil.DefaultSystemNamespace, Name: svc.Template}, svcTemplate); err != nil {
			return err
		}
		if svcTemplate.HelmChartSpec() != nil {
			serviceTemplatesToCharts[svc.Template] = svcTemplate.HelmChartSpec().Chart
		}
	}

	allFound := false
	for _, helmChart := range profileSpec.HelmCharts {
		for _, svc := range serviceSpec.Services {
			if serviceTemplatesToCharts[svc.Template] == helmChart.ChartName {
				if helmChart.Values == svc.Values {
					allFound = true
				}
				break
			}
		}
	}
	if !allFound {
		return fmt.Errorf("failed to validate profile")
	}
	return nil
}

// createAndWaitCluster centralizes cluster creation, waiting for deploy and returns the created ClusterDeployment + delete function.
func createAndWaitCluster(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) (*kcmv1.ClusterDeployment, func() error) {
	dockerTemplates := templates.FindLatestTemplatesWithType(
		clusterTemplates,
		templates.TemplateDockerCluster,
		1,
	)
	Expect(dockerTemplates).NotTo(BeEmpty(), "expected at least one Docker template")
	clusterTemplate := dockerTemplates[0]

	templateBy(templates.TemplateDockerCluster, fmt.Sprintf("Creating ClusterDeployment %s with template %s", clusterName, clusterTemplate))
	sd := clusterdeployment.Generate(templates.TemplateDockerCluster, clusterName, clusterTemplate)
	sd.Spec.ServiceSpec.Services[0].TemplateChain = templateChainName

	deleteClusterFn := clusterdeployment.Create(ctx, kc.CrClient, sd)

	deleteFn := func() error {
		By(fmt.Sprintf("Deleting ClusterDeployment %s", clusterName))
		Expect(deleteClusterFn()).NotTo(HaveOccurred(), "failed to delete cluster")

		By(fmt.Sprintf("Verifying ClusterDeployment %s deletion", clusterName))
		validator := clusterdeployment.NewProviderValidator(templates.TemplateDockerCluster, clusterName, clusterdeployment.ValidationActionDelete)
		Eventually(func() error { return validator.Validate(ctx, kc) }, 30*time.Minute, validatorPoll).Should(Succeed())
		return nil
	}

	templateBy(templates.TemplateDockerCluster, "Waiting for infrastructure to deploy successfully")
	deployValidator := clusterdeployment.NewProviderValidator(templates.TemplateDockerCluster, clusterName, clusterdeployment.ValidationActionDeploy)
	Eventually(func() error { return deployValidator.Validate(ctx, kc) }, 30*time.Minute, validatorPoll).Should(Succeed())

	return sd, deleteFn
}

// updateClusterDeploymentTemplate updates the template for a service inside a ClusterDeployment.
func updateClusterDeploymentTemplate(ctx context.Context, sd *kcmv1.ClusterDeployment, version string) {
	Eventually(func() error {
		if err := kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(sd), sd); err != nil {
			return fmt.Errorf("failed to fetch ClusterDeployment: %w", err)
		}

		newTemplate := fmt.Sprintf("%s-%s", nginxChartName, strings.ReplaceAll(version, ".", "-"))
		for i, service := range sd.Spec.ServiceSpec.Services {
			if service.Name == nginxServiceName {
				sd.Spec.ServiceSpec.Services[i].Template = newTemplate
			}
		}

		By(fmt.Sprintf("Update service to:%s\n", newTemplate))
		err := kc.CrClient.Update(ctx, sd)
		if err != nil {
			logs.WarnErrorf(err, "failed to update ClusterDeployment")
		}
		return err
	}, 1*time.Minute, 5*time.Second).Should(Succeed())
}

// updateClusterDeploymentLabel sets the given label value on the given ClusterDeployment.
func updateClusterDeploymentLabel(ctx context.Context, cl crclient.Client, cd *kcmv1.ClusterDeployment, label, value string) {
	toUpdate := kcmv1.ClusterDeployment{}
	Expect(cl.Get(ctx, crclient.ObjectKeyFromObject(cd), &toUpdate)).NotTo(HaveOccurred())
	if toUpdate.Labels == nil {
		toUpdate.Labels = map[string]string{}
	}
	toUpdate.Labels[label] = value
	clusterdeployment.Update(ctx, cl, &toUpdate)
}

// waitForServiceDeployments waits until the given services are in deployed state
func waitForServiceDeployments(
	ctx context.Context,
	kc *kubeclient.KubeClient,
	sd *kcmv1.ClusterDeployment,
	services []kcmv1.Service,
) {
	serviceSet := &kcmv1.ServiceSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sd.Name,
			Namespace: sd.Namespace,
		},
	}

	Eventually(func() error {
		if err := kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet); err != nil {
			return fmt.Errorf("could not get ServiceSet: %w", err)
		}

		stateMap := make(map[string]kcmv1.ServiceState, len(serviceSet.Status.Services))
		for _, ss := range serviceSet.Status.Services {
			stateMap[ss.Name] = ss
		}

		for _, svc := range services {
			serviceState, ok := stateMap[svc.Name]
			if !ok {
				return fmt.Errorf("service %s not yet reported in ServiceSet status", svc.Name)
			}
			if serviceState.State == kcmv1.ServiceStateFailed {
				Fail(fmt.Sprintf("service %s permanently failed: %s", svc.Name, serviceState.FailureMessage))
			}
			if serviceState.State != kcmv1.ServiceStateDeployed {
				logs.Printf("Service %s in %s state: %s", svc.Name, serviceState.State, serviceState.FailureMessage)
				return fmt.Errorf("service %s in %s state: %s", svc.Name, serviceState.State, serviceState.FailureMessage)
			}
			logs.Printf("Service %s is deployed", svc.Name)
		}
		return nil
	}, 10*time.Minute, 10*time.Second).Should(Succeed())
}

func startWatchingServiceSetVersions(
	ctx context.Context,
	kc *kubeclient.KubeClient,
	clusterName,
	clusterNamespace string,
	versions []string,
) func() {
	gvr := schema.GroupVersionResource{
		Group:    "k0rdent.mirantis.com",
		Version:  "v1beta1",
		Resource: "servicesets",
	}

	dynClient := kc.GetDynamicClient(gvr, true)

	watcher, err := dynClient.Watch(ctx, metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to create watcher for ServiceSets")

	expectedVersions := map[string]bool{}
	for _, v := range versions {
		expectedVersions[v] = false
	}

	return func() {
		defer watcher.Stop()

		Eventually(func() error {
			for event := range watcher.ResultChan() {
				obj, ok := event.Object.(*unstructured.Unstructured)
				if !ok || obj == nil {
					continue
				}

				var svcSet kcmv1.ServiceSet
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &svcSet); err != nil {
					return fmt.Errorf("failed to convert unstructured to ServiceSet: %w", err)
				}

				if event.Type != watch.Modified {
					continue
				}

				for _, service := range svcSet.Spec.Services {
					if service.Name == nginxServiceName {
						version := *service.Version
						By(fmt.Sprintf("Service %s/%s modified (version: %s)\n", svcSet.Namespace, svcSet.Name, version))

						if _, exists := expectedVersions[version]; exists {
							expectedVersions[version] = true
						}

						allSeen := true
						for _, seen := range expectedVersions {
							if !seen {
								allSeen = false
								break
							}
						}

						if allSeen {
							return nil
						}
					}
				}
			}
			return fmt.Errorf("not all expected versions observed: %+v", expectedVersions)
		}, 10*time.Minute, 100*time.Millisecond).Should(Succeed())

		Eventually(func() error {
			serviceSet := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
			}
			if err := kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet); err != nil {
				return fmt.Errorf("failed to fetch ServiceSet: %w", err)
			}

			for _, service := range serviceSet.Status.Services {
				if service.State != kcmv1.ServiceStateDeployed {
					return fmt.Errorf("service %s is in %s state", service.Name, service.State)
				}
			}
			return nil
		}, 10*time.Minute, 5*time.Second).Should(Succeed())
	}
}

func setK0smotronNodePorts(sd *kcmv1.ClusterDeployment, apiPort, konnectivityPort int) {
	GinkgoHelper()

	var cfg map[string]any
	if sd.Spec.Config != nil {
		Expect(json.Unmarshal(sd.Spec.Config.Raw, &cfg)).To(Succeed())
	}
	if cfg == nil {
		cfg = make(map[string]any)
	}

	k0smotronCfg, ok := cfg["k0smotron"].(map[string]any)
	if !ok || k0smotronCfg == nil {
		k0smotronCfg = make(map[string]any)
	}
	serviceCfg, ok := k0smotronCfg["service"].(map[string]any)
	if !ok || serviceCfg == nil {
		serviceCfg = map[string]any{"type": "NodePort"}
	}
	serviceCfg["apiPort"] = apiPort
	serviceCfg["konnectivityPort"] = konnectivityPort
	k0smotronCfg["service"] = serviceCfg
	cfg["k0smotron"] = k0smotronCfg

	configBytes, err := json.Marshal(cfg)
	Expect(err).NotTo(HaveOccurred())
	sd.Spec.Config = &apiextv1.JSON{Raw: configBytes}
}
