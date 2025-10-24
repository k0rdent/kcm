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
	"strings"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

var _ = Describe("Functional e2e tests", Label("provider:cloud", "provider:docker"), Ordered, ContinueOnFailure, func() {
	const (
		helmRepositoryName         = "k0rdent-catalog"
		templateChainName          = "ingress-nginx"
		chartName                  = "ingress-nginx"
		multiClusterServiceChart   = "kyverno"
		multiClusterServiceVersion = "3.4.4"
	)

	var (
		kc                *kubeclient.KubeClient
		clusterDeleteFunc func() error
		adoptedDeleteFunc func() error
		kubecfgDeleteFunc func() error
		clusterNames      []string

		helmRepositorySpec = sourcev1.HelmRepositorySpec{
			Type: "oci",
			URL:  "oci://ghcr.io/k0rdent/catalog/charts",
		}
		serviceTemplateSpecs []kcmv1.ServiceTemplateSpec
		supportedTemplates   []kcmv1.SupportedTemplate
	)

	multiClusterServiceTemplate := fmt.Sprintf("%s-%s", multiClusterServiceChart, strings.ReplaceAll(multiClusterServiceVersion, ".", "-"))
	serviceTemplateSpecs = append(serviceTemplateSpecs, kcmv1.ServiceTemplateSpec{
		Helm: &kcmv1.HelmSpec{
			ChartSpec: &sourcev1.HelmChartSpec{
				Chart: multiClusterServiceChart,
				SourceRef: sourcev1.LocalHelmChartSourceReference{
					Kind: sourcev1.HelmRepositoryKind,
					Name: helmRepositoryName,
				},
				Version: multiClusterServiceVersion,
			},
		},
	})

	nginxVersions := []string{"4.11.3", "4.11.5", "4.12.3", "4.13.0"}
	for i, v := range nginxVersions {
		name := fmt.Sprintf("%s-%s", chartName, strings.ReplaceAll(v, ".", "-"))
		serviceTemplateSpecs = append(serviceTemplateSpecs, kcmv1.ServiceTemplateSpec{
			Helm: &kcmv1.HelmSpec{
				ChartSpec: &sourcev1.HelmChartSpec{
					Chart: chartName,
					SourceRef: sourcev1.LocalHelmChartSourceReference{
						Kind: sourcev1.HelmRepositoryKind,
						Name: helmRepositoryName,
					},
					Version: v,
				},
			},
		})
		// Build the SupportedTemplate (and upgrades)
		var upgrades []kcmv1.AvailableUpgrade
		if i < len(nginxVersions)-1 {
			for _, nv := range nginxVersions[i+1:] {
				upgrades = append(upgrades, kcmv1.AvailableUpgrade{
					Name:    fmt.Sprintf("%s-%s", chartName, strings.ReplaceAll(nv, ".", "-")),
					Version: nv,
				})
			}
		}

		if i < len(nginxVersions)-1 {
			upgrades = append(upgrades, kcmv1.AvailableUpgrade{
				Name:    fmt.Sprintf("%s-%s", chartName, strings.ReplaceAll(nginxVersions[i+1], ".", "-")),
				Version: nginxVersions[i+1],
			})
		}
		supportedTemplates = append(supportedTemplates, kcmv1.SupportedTemplate{
			Name:              name,
			AvailableUpgrades: upgrades,
		})
	}

	serviceTemplateChain := kcmv1.TemplateChainSpec{
		SupportedTemplates: supportedTemplates,
	}

	BeforeAll(func() {
		By("Creating kube client")
		kc = kubeclient.NewFromLocal(kubeutil.DefaultSystemNamespace)

		var err error
		clusterTemplates, err = templates.GetSortedClusterTemplates(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace)
		Expect(err).NotTo(HaveOccurred())

		By("Providing cluster identity and credentials")
		credential.Apply("", "docker")

		By("creating HelmRepository and ServiceTemplate", func() {
			flux.CreateHelmRepository(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace, helmRepositoryName, helmRepositorySpec)
			for _, serviceTemplateSpec := range serviceTemplateSpecs {
				serviceTemplateName := fmt.Sprintf("%s-%s", serviceTemplateSpec.Helm.ChartSpec.Chart, strings.ReplaceAll(serviceTemplateSpec.Helm.ChartSpec.Version, ".", "-"))
				templates.CreateServiceTemplate(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace, serviceTemplateName, serviceTemplateSpec)
			}
			templates.CreateTemplateChain(context.Background(), kc.CrClient, kubeutil.DefaultSystemNamespace, templateChainName, serviceTemplateChain)
		})
	})

	AfterAll(func() {
		if CurrentSpecReport().Failed() && cleanup() {
			By("Collecting the support bundle from the management cluster")
			logs.SupportBundle(kc, "")
		}

		if cleanup() {
			By("Deleting resources")
			for _, deleteFunc := range []func() error{
				adoptedDeleteFunc,
				clusterDeleteFunc,
				kubecfgDeleteFunc,
			} {
				if deleteFunc != nil {
					err := deleteFunc()
					Expect(err).NotTo(HaveOccurred())
				}
			}
		}
	})

	It("MultiCluster services no longer match", func() {
		const (
			multiClusterServiceName       = "test-multicluster"
			multiClusterServiceMatchLabel = "k0rdent.mirantis.com/test-cluster-name"
		)

		defer GinkgoRecover()

		ctx := context.Background()
		for i, cfg := range config.Config[config.TestingProviderDocker] {
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))

			clusterName := clusterdeployment.GenerateClusterName(fmt.Sprintf("docker-%d", i))

			// Find latest Docker cluster template
			dockerTemplates := templates.FindLatestTemplatesWithType(
				clusterTemplates,
				templates.TemplateDockerCluster,
				1,
			)
			Expect(dockerTemplates).NotTo(BeEmpty(), "expected at least one Docker template")

			clusterTemplate := dockerTemplates[0]
			templateBy(
				templates.TemplateDockerCluster,
				fmt.Sprintf("Creating ClusterDeployment %s with template %s", clusterName, clusterTemplate),
			)

			sd := clusterdeployment.Generate(templates.TemplateDockerCluster, clusterName, clusterTemplate)
			sd.Spec.ServiceSpec.Services[0].TemplateChain = templateChainName

			deleteClusterFn := clusterdeployment.Create(ctx, kc.CrClient, sd)
			clusterNames = append(clusterNames, clusterName)

			clusterDeleteFunc = func() error { //nolint:unparam // required signature for cleanup
				By(fmt.Sprintf("Deleting ClusterDeployment %s", clusterName))
				Expect(deleteClusterFn()).NotTo(HaveOccurred(), "failed to delete cluster")

				By(fmt.Sprintf("Verifying ClusterDeployment %s deletion", clusterName))
				validator := clusterdeployment.NewProviderValidator(
					templates.TemplateDockerCluster,
					clusterName,
					clusterdeployment.ValidationActionDelete,
				)

				Eventually(func() error {
					return validator.Validate(ctx, kc)
				}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				return nil
			}

			templateBy(templates.TemplateDockerCluster, "Waiting for infrastructure to deploy successfully")

			deployValidator := clusterdeployment.NewProviderValidator(
				templates.TemplateDockerCluster,
				clusterName,
				clusterdeployment.ValidationActionDeploy,
			)

			Eventually(func() error {
				return deployValidator.Validate(ctx, kc)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By(fmt.Sprintf("Create MultiClusterService for template: %s with match label: %s", multiClusterServiceTemplate, multiClusterServiceMatchLabel))
			mcs := multiclusterservice.BuildMultiClusterService(sd, multiClusterServiceTemplate, multiClusterServiceMatchLabel, multiClusterServiceName)
			multiclusterservice.CreateMultiClusterService(context.Background(), kc.CrClient, mcs)

			By(fmt.Sprintf("Validate MultiClusterService for template: %s deploys with match label: %s", multiClusterServiceTemplate, multiClusterServiceMatchLabel))
			multiclusterservice.ValidateMultiClusterService(kc, multiClusterServiceName, 1)

			By("Change label on ClusterDeployment and verify MultiClusterService is removed")
			updateClusterDeploymentLabel(context.Background(), kc.CrClient, sd, multiClusterServiceMatchLabel, "not-matched")
			multiclusterservice.ValidateMultiClusterService(kc, multiClusterServiceName, 0)

			Expect(clusterDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete cluster")
		}
	})

	It("Performing sequential upgrades", func() {
		defer GinkgoRecover()
		for i, cfg := range config.Config[config.TestingProviderDocker] {
			ctx := context.Background()
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))

			clusterName := clusterdeployment.GenerateClusterName(fmt.Sprintf("docker-%d", i))

			dockerTemplates := templates.FindLatestTemplatesWithType(
				clusterTemplates,
				templates.TemplateDockerCluster,
				1,
			)
			Expect(dockerTemplates).NotTo(BeEmpty(), "expected at least one Docker template")

			clusterTemplate := dockerTemplates[0]
			templateBy(
				templates.TemplateDockerCluster,
				fmt.Sprintf("Creating ClusterDeployment %s with template %s", clusterName, clusterTemplate),
			)

			sd := clusterdeployment.Generate(templates.TemplateDockerCluster, clusterName, clusterTemplate)
			sd.Spec.ServiceSpec.Services[0].TemplateChain = templateChainName

			deleteClusterFn := clusterdeployment.Create(ctx, kc.CrClient, sd)
			clusterNames = append(clusterNames, clusterName)

			clusterDeleteFunc = func() error { //nolint:unparam // required signature for cleanup
				By(fmt.Sprintf("Deleting ClusterDeployment %s", clusterName))
				Expect(deleteClusterFn()).NotTo(HaveOccurred(), "failed to delete cluster")

				By(fmt.Sprintf("Verifying ClusterDeployment %s deletion", clusterName))
				validator := clusterdeployment.NewProviderValidator(
					templates.TemplateDockerCluster,
					clusterName,
					clusterdeployment.ValidationActionDelete,
				)

				Eventually(func() error {
					return validator.Validate(ctx, kc)
				}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				return nil
			}

			templateBy(templates.TemplateDockerCluster, "Waiting for infrastructure to deploy successfully")

			deployValidator := clusterdeployment.NewProviderValidator(
				templates.TemplateDockerCluster,
				clusterName,
				clusterdeployment.ValidationActionDeploy,
			)

			Eventually(func() error {
				return deployValidator.Validate(ctx, kc)
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
			Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(sd), sd)).
				NotTo(HaveOccurred(), "failed to fetch ServiceDeployment")

			newTemplate := fmt.Sprintf("%s-%s", chartName, strings.ReplaceAll(nginxVersions[2], ".", "-"))
			sd.Spec.ServiceSpec.Services[0].Template = newTemplate

			By(fmt.Sprintf("Upgrade service to:%s\n", newTemplate))
			clusterdeployment.Update(ctx, kc.CrClient, sd)

			expectedVersions := []string{
				nginxVersions[1],
				nginxVersions[2],
			}
			waitForServiceSetVersions(ctx, kc, expectedVersions, 1*time.Minute, 10*time.Second)

			serviceSet := &kcmv1.ServiceSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sd.Name,
					Namespace: sd.Namespace,
				},
			}
			Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet)).
				NotTo(HaveOccurred(), "failed to fetch ServiceSett")
			Expect(serviceSet.Spec.Services).To(HaveLen(1))

			Eventually(func() error {
				Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(sd), sd)).
					NotTo(HaveOccurred(), "failed to fetch ServiceDeployment")

				newTemplate = fmt.Sprintf("%s-%s", chartName, strings.ReplaceAll(nginxVersions[0], ".", "-"))
				sd.Spec.ServiceSpec.Services[0].Template = newTemplate
				By(fmt.Sprintf("Downgrade service to:%s\n", newTemplate))

				err := kc.CrClient.Update(ctx, sd)
				if err != nil {
					logs.Println("failed to update ClusterDeployment: " + err.Error())
				}
				return err
			}, 1*time.Minute, 10*time.Second).Should(Succeed())

			expectedVersions = []string{
				nginxVersions[0],
			}
			waitForServiceSetVersions(ctx, kc, expectedVersions, 1*time.Minute, 10*time.Second)
			Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet)).
				NotTo(HaveOccurred(), "failed to fetch ServiceSett")
			Expect(serviceSet.Spec.Services).To(HaveLen(1))
			Expect(clusterDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete cluster")
		}
	})
})

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

// waitForServiceSetVersions waits until the serviceset is updated with the given versions
func waitForServiceSetVersions(
	ctx context.Context,
	kc *kubeclient.KubeClient,
	nginxVersions []string,
	timeout, interval time.Duration,
) {
	gvr := schema.GroupVersionResource{
		Group:    "k0rdent.mirantis.com",
		Version:  "v1beta1",
		Resource: "servicesets",
	}

	dynClient := kc.GetDynamicClient(gvr, true)

	watcher, err := dynClient.Watch(ctx, metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to create watcher for ServiceSets")

	expectedVersions := map[string]bool{}
	for _, v := range nginxVersions {
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

			if len(svcSet.Spec.Services) > 0 {
				version := svcSet.Spec.Services[0].Version
				By(fmt.Sprintf("Service %s/%s modified (version: %s)\n",
					svcSet.Namespace, svcSet.Name, version))

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

		return fmt.Errorf("not all expected versions observed: %+v", expectedVersions)
	}, timeout, interval).Should(Succeed())
}
