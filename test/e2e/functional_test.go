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
	"fmt"
	"strings"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
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
	validatorPoll        = 10 * time.Second
)

var _ = Describe("Functional e2e tests", Label("provider:cloud", "provider:docker"), Ordered, ContinueOnFailure, func() {
	var (
		clusterNames      []string
		clusterDeleteFunc func() error

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
	})

	AfterEach(func() {
		if clusterDeleteFunc != nil {
			Expect(clusterDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete cluster")
			clusterDeleteFunc = nil
		}
	})

	AfterAll(func() {
		if CurrentSpecReport().Failed() && cleanup() {
			By("Collecting the support bundle from the management cluster")
			logs.SupportBundle(kc, "")
		}

		if cleanup() {
			By("Deleting resources")
			if clusterDeleteFunc != nil {
				err := clusterDeleteFunc()
				Expect(err).NotTo(HaveOccurred())
			}
		}
	})

	It("MultiCluster services no longer match", func() {
		const (
			multiClusterServiceName       = "test-multicluster"
			multiClusterServiceMatchLabel = "k0rdent.mirantis.com/test-cluster-name"
		)

		defer GinkgoRecover()
		for i, cfg := range config.Config[config.TestingProviderDocker] {
			ctx := context.Background()
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))
			clusterName := clusterdeployment.GenerateClusterName(fmt.Sprintf("docker-%d", i))

			sd, deleteFn := createAndWaitCluster(ctx, kc, clusterName, templateChainName)
			clusterNames = append(clusterNames, clusterName)
			clusterDeleteFunc = deleteFn

			mcs := multiclusterservice.BuildMultiClusterService(sd, multiClusterServiceTemplate, multiClusterServiceMatchLabel, multiClusterServiceName)
			multiclusterservice.CreateMultiClusterService(ctx, kc.CrClient, mcs)
			multiclusterservice.ValidateMultiClusterService(kc, multiClusterServiceName, 1) // this fails

			updateClusterDeploymentLabel(ctx, kc.CrClient, sd, multiClusterServiceMatchLabel, "not-matched")
			multiclusterservice.ValidateMultiClusterService(kc, multiClusterServiceName, 0)

			multiclusterservice.DeleteMultiClusterService(ctx, kc.CrClient, mcs)
			Expect(clusterDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete cluster")
			clusterDeleteFunc = nil
		}
	})

	It("Performing sequential upgrades", func() {
		defer GinkgoRecover()
		for i, cfg := range config.Config[config.TestingProviderDocker] {
			ctx := context.Background()
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))

			clusterName := clusterdeployment.GenerateClusterName(fmt.Sprintf("docker-%d", i))

			sd, deleteFn := createAndWaitCluster(ctx, kc, clusterName, templateChainName)
			clusterNames = append(clusterNames, clusterName)
			clusterDeleteFunc = deleteFn

			waitForServiceDeployments(ctx, kc, sd, sd.Spec.ServiceSpec.Services, 10*time.Minute, 10*time.Second)

			updateClusterDeploymentTemplate(ctx, sd, nginxVersions[2])

			expectedVersions := []string{
				nginxVersions[1],
				nginxVersions[2],
			}
			waitForServiceSetVersions(ctx, kc, sd.Name, sd.Namespace, expectedVersions)

			updateClusterDeploymentTemplate(ctx, sd, nginxVersions[0])
			expectedVersions = []string{nginxVersions[0]}
			waitForServiceSetVersions(ctx, kc, sd.Name, sd.Namespace, expectedVersions)

			serviceSet := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sd.Name,
					Namespace: sd.Namespace,
				},
			}
			Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet)).NotTo(HaveOccurred(), "failed to fetch ServiceSet")
			Expect(serviceSet.Spec.Services).To(HaveLen(1))

			Expect(clusterDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete cluster")
			clusterDeleteFunc = nil
		}
	})

	It("Performing upgrades with dependent services", func() {
		defer GinkgoRecover()
		for i, cfg := range config.Config[config.TestingProviderDocker] {
			ctx := context.Background()
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))
			clusterName := clusterdeployment.GenerateClusterName(fmt.Sprintf("docker-%d", i))

			serviceName := fmt.Sprintf("%s-%s", openCostChartName, strings.ReplaceAll(openCostChartVersion, ".", "-"))
			sd := clusterdeployment.Generate(templates.TemplateDockerCluster, clusterName, templates.FindLatestTemplatesWithType(clusterTemplates, templates.TemplateDockerCluster, 1)[0])
			sd.Spec.ServiceSpec.Services[0].TemplateChain = templateChainName
			sd.Spec.ServiceSpec.Services[0].DependsOn = []kcmv1.ServiceDependsOn{
				{
					Name:      serviceName,
					Namespace: openCostChartName,
				},
			}

			sd.Spec.ServiceSpec.Services = append(sd.Spec.ServiceSpec.Services,
				kcmv1.Service{
					Name:      openWebuiChartName,
					Namespace: openWebuiChartName,
					Template:  fmt.Sprintf("%s-%s", openWebuiChartName, strings.ReplaceAll(openWebuiVersion, ".", "-")),
					DependsOn: []kcmv1.ServiceDependsOn{{Name: serviceName, Namespace: openCostChartName}},
				})

			sd.Spec.ServiceSpec.Services = append(sd.Spec.ServiceSpec.Services,
				kcmv1.Service{
					Name:      serviceName,
					Namespace: openCostChartName,
					Template:  fmt.Sprintf("%s-%s", openCostChartName, strings.ReplaceAll(openCostChartVersion, ".", "-")),
				})

			By(fmt.Sprintf("Deploying cluster deployment :%v", sd))
			deleteFn := clusterdeployment.Create(ctx, kc.CrClient, sd)
			clusterNames = append(clusterNames, clusterName)
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

			waitForServiceDeployments(ctx, kc, sd, sd.Spec.ServiceSpec.Services, 10*time.Minute, 10*time.Second)
			updateClusterDeploymentTemplate(ctx, sd, nginxVersions[2])

			expectedVersions := []string{
				nginxVersions[1],
				nginxVersions[2],
			}
			waitForServiceSetVersions(ctx, kc, sd.Name, sd.Namespace, expectedVersions)

			serviceSet := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sd.Name,
					Namespace: sd.Namespace,
				},
			}
			Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet)).NotTo(HaveOccurred(), "failed to fetch ServiceSet")
			Expect(serviceSet.Spec.Services).To(HaveLen(3))

			updateClusterDeploymentTemplate(ctx, sd, nginxVersions[0])
			expectedVersions = []string{nginxVersions[0]}
			waitForServiceSetVersions(ctx, kc, sd.Name, sd.Namespace, expectedVersions)

			Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet)).NotTo(HaveOccurred(), "failed to fetch ServiceSet")
			Expect(serviceSet.Spec.Services).To(HaveLen(3))
			Expect(clusterDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete cluster")
			clusterDeleteFunc = nil
		}
	})

	It("Pause service deployment", func() {
		defer GinkgoRecover()
		for i, cfg := range config.Config[config.TestingProviderDocker] {
			ctx := context.Background()
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))

			clusterName := clusterdeployment.GenerateClusterName(fmt.Sprintf("docker-%d", i))

			sd, deleteFn := createAndWaitCluster(ctx, kc, clusterName, templateChainName)
			clusterNames = append(clusterNames, clusterName)
			clusterDeleteFunc = deleteFn

			waitForServiceDeployments(ctx, kc, sd, sd.Spec.ServiceSpec.Services, 10*time.Minute, 10*time.Second)

			serviceSet := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sd.Name,
					Namespace: sd.Namespace,
				},
			}
			Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet)).NotTo(HaveOccurred(), "failed to fetch ServiceSet")
			Expect(serviceSet.Spec.Services).To(HaveLen(1))

			serviceSet.SetAnnotations(map[string]string{
				kcmv1.ServiceSetPausedAnnotation: "true",
			})
			Expect(kc.CrClient.Update(ctx, serviceSet)).NotTo(HaveOccurred(), "failed to update ServiceSet")
			updateClusterDeploymentTemplate(ctx, sd, nginxVersions[2])

			Eventually(ctx, func() error {
				profile := addoncontrollerv1beta1.Profile{}
				Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), &profile)).NotTo(HaveOccurred())
				_, ok := profile.Annotations[addoncontrollerv1beta1.ProfilePausedAnnotation]
				Expect(ok).To(BeTrue())
				return nil
			}, 30*time.Minute, 10*time.Second).Should(Succeed())

			serviceSet.SetAnnotations(map[string]string{})
			Expect(kc.CrClient.Update(ctx, serviceSet)).NotTo(HaveOccurred(), "failed to update ServiceSet")

			Expect(clusterDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete cluster")
			clusterDeleteFunc = nil
		}
	})
})

// createAndWaitCluster centralizes cluster creation, waiting for deploy and returns the created ClusterDeployment + delete function.
func createAndWaitCluster(ctx context.Context, kc *kubeclient.KubeClient, clusterName, templateChainName string) (*kcmv1.ClusterDeployment, func() error) {
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
		Eventually(func() error { return validator.Validate(ctx, kc) }, 30*time.Minute, 10*time.Second).Should(Succeed())
		return nil
	}

	templateBy(templates.TemplateDockerCluster, "Waiting for infrastructure to deploy successfully")
	deployValidator := clusterdeployment.NewProviderValidator(templates.TemplateDockerCluster, clusterName, clusterdeployment.ValidationActionDeploy)
	Eventually(func() error { return deployValidator.Validate(ctx, kc) }, 30*time.Minute, 10*time.Second).Should(Succeed())

	return sd, deleteFn
}

// updateClusterDeploymentTemplate updates the template for a service inside a ClusterDeployment.
func updateClusterDeploymentTemplate(ctx context.Context, sd *kcmv1.ClusterDeployment, version string) {
	Eventually(func() error {
		Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(sd), sd)).NotTo(HaveOccurred(), "failed to fetch ServiceDeployment")

		newTemplate := fmt.Sprintf("%s-%s", nginxChartName, strings.ReplaceAll(version, ".", "-"))
		for i, service := range sd.Spec.ServiceSpec.Services {
			if service.Name == nginxServiceName {
				sd.Spec.ServiceSpec.Services[i].Template = newTemplate
			}
		}

		By(fmt.Sprintf("Update service to:%s\n", newTemplate))
		err := kc.CrClient.Update(ctx, sd)
		if err != nil {
			logs.Println("failed to update ClusterDeployment: " + err.Error())
		}
		return err
	}, 1*time.Minute, 10*time.Second).Should(Succeed())
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
	timeout, interval time.Duration,
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

		for i := len(services) - 1; i >= 0; i-- {
			serviceState, ok := stateMap[services[i].Name]
			if !ok {
				continue
			}

			if serviceState.State != kcmv1.ServiceStateDeployed {
				logs.Println(fmt.Sprintf("service %s in %s state: %s", services[i].Name, serviceState.State, serviceState.FailureMessage))
				return fmt.Errorf("service %s in %s state: %s", services[i].Name, serviceState.State, serviceState.FailureMessage)
			}

			logs.Println(fmt.Sprintf("service %s is deployed", services[i].Name))
			services = append(services[:i], services[i+1:]...)
		}

		return nil
	}, timeout, interval).Should(Succeed())
}

// waitForServiceSetVersions waits until the serviceset is updated with the given versions
func waitForServiceSetVersions(
	ctx context.Context,
	kc *kubeclient.KubeClient,
	clusterName,
	clusterNamespace string,
	versions []string,
) {
	gvr := schema.GroupVersionResource{
		Group:    "k0rdent.mirantis.com",
		Version:  "v1beta1",
		Resource: "servicesets",
	}

	dynClient := kc.GetDynamicClient(gvr, true)

	watcher, err := dynClient.Watch(ctx, metav1.ListOptions{})
	defer func() {
		if watcher != nil {
			watcher.Stop()
		}
	}()
	Expect(err).NotTo(HaveOccurred(), "failed to create watcher for ServiceSets")

	expectedVersions := map[string]bool{}
	for _, v := range versions {
		expectedVersions[v] = false
	}

	Eventually(func() error {
		for event := range watcher.ResultChan() {
			obj, ok := event.Object.(*unstructured.Unstructured)
			if !ok || obj == nil {
				continue
			}

			var svcSet kcmv1.ServiceSet
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &svcSet)
			Expect(err).NotTo(HaveOccurred(), "failed to convert unstructured to ServiceSet")

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
		Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet)).NotTo(HaveOccurred(), "failed to fetch ServiceSet")

		for _, service := range serviceSet.Status.Services {
			if service.State != kcmv1.ServiceStateDeployed {
				return fmt.Errorf("service %s is in %s state", service.Name, service.State)
			}
		}
		return nil
	}, 10*time.Minute, 10*time.Second).Should(Succeed())
}
