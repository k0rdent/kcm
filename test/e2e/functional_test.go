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
	"slices"
	"strings"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
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
	nginxServiceName     = "managed-ingress-nginx"
	validatorTimeout     = 30 * time.Minute
	validatorPoll        = 10 * time.Second
)

var _ = Describe("Functional e2e tests", Label("provider:cloud", "provider:docker"), Ordered, ContinueOnFailure, func() {
	var (
		clusterName       string
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
		supportedTemplates = make([]kcmv1.SupportedTemplate, 0, len(nginxVersions))

		for i := range nginxVersions {
			name := fmt.Sprintf("%s-%s", nginxChartName, strings.ReplaceAll(nginxVersions[i], ".", "-"))

			st := kcmv1.SupportedTemplate{
				Name: name,
			}

			if i == len(nginxVersions)-1 {
				supportedTemplates = append(supportedTemplates, st)
				continue
			}

			nextName := fmt.Sprintf("%s-%s", nginxChartName, strings.ReplaceAll(nginxVersions[i+1], ".", "-"))
			st.AvailableUpgrades = append(st.AvailableUpgrades, kcmv1.AvailableUpgrade{
				Name:    nextName,
				Version: nginxVersions[i+1],
			})

			if i == 0 && len(nginxVersions) > 2 {
				skipName := fmt.Sprintf("%s-%s", nginxChartName, strings.ReplaceAll(nginxVersions[i+2], ".", "-"))
				st.AvailableUpgrades = append(st.AvailableUpgrades, kcmv1.AvailableUpgrade{
					Name:    skipName,
					Version: nginxVersions[i+2],
				})
			}

			supportedTemplates = append(supportedTemplates, st)
		}

		for _, v := range nginxVersions {
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
			err := clusterDeleteFunc()
			clusterDeleteFunc = nil
			Expect(err).NotTo(HaveOccurred(), "failed to delete cluster")
		}

		waitForSveltosResourcesDeleted(context.Background(), kc, clusterName)
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
			clusterName = clusterdeployment.GenerateUniqueClusterName(fmt.Sprintf("docker-%d", i))

			sd, deleteFn := createAndWaitCluster(ctx, kc, clusterName)
			clusterDeleteFunc = deleteFn

			mcs := multiclusterservice.BuildMultiClusterService(sd, multiClusterServiceTemplate, openCostChartName, multiClusterServiceMatchLabel, multiClusterServiceName)
			multiclusterservice.CreateMultiClusterService(ctx, kc.CrClient, mcs)
			multiclusterservice.ValidateMultiClusterService(ctx, kc, multiClusterServiceName, 1)

			updateClusterDeploymentLabel(ctx, kc.CrClient, sd, multiClusterServiceMatchLabel, "not-matched")
			multiclusterservice.ValidateMultiClusterService(ctx, kc, multiClusterServiceName, 0)

			multiclusterservice.DeleteMultiClusterService(ctx, kc.CrClient, mcs)
			Expect(clusterDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete cluster")
			clusterDeleteFunc = nil
		})

		It("Pause service deployment", func() {
			defer GinkgoRecover()
			ctx := context.Background()
			cfg.SetDefaults(clusterTemplates, config.TestingProviderDocker)

			By(fmt.Sprintf("Testing configuration:\n%s\n", cfg.String()))

			clusterName = clusterdeployment.GenerateUniqueClusterName(fmt.Sprintf("docker-%d", i))

			sd, deleteFn := createAndWaitCluster(ctx, kc, clusterName)
			clusterDeleteFunc = deleteFn

			waitForServiceDeployments(ctx, kc, sd, sd.Spec.ServiceSpec.Services)

			serviceSet := &kcmv1.ServiceSet{
				Name:      sd.Name,
				Namespace: sd.Namespace,
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

			Eventually(ctx, func() error {
				Expect(kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet)).NotTo(HaveOccurred())
				serviceSet.SetAnnotations(map[string]string{})
				return kc.CrClient.Update(ctx, serviceSet)
			}, 30*time.Minute, 10*time.Second).Should(Succeed())

			Expect(clusterDeleteFunc()).Error().NotTo(HaveOccurred(), "failed to delete cluster")
			clusterDeleteFunc = nil
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
			logs.WarnErrorf(err, "failed to update ClusterDeployment")
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
) {
	serviceSet := &kcmv1.ServiceSet{
		Name:      sd.Name,
		Namespace: sd.Namespace,
	}

	Eventually(func() error {
		if err := kc.CrClient.Get(ctx, crclient.ObjectKeyFromObject(serviceSet), serviceSet); err != nil {
			return fmt.Errorf("could not get ServiceSet: %w", err)
		}

		stateMap := make(map[string]kcmv1.ServiceState, len(serviceSet.Status.Services))
		for _, ss := range serviceSet.Status.Services {
			stateMap[ss.Name] = ss
		}

		for i, service := range slices.Backward(services) {
			serviceState, ok := stateMap[service.Name]
			if !ok {
				continue
			}

			if serviceState.State != kcmv1.ServiceStateDeployed {
				logs.Printf("Service %s in %s state: %s", service.Name, serviceState.State, serviceState.FailureMessage)
				return fmt.Errorf("service %s in %s state: %s", service.Name, serviceState.State, serviceState.FailureMessage)
			}

			logs.Printf("Service %s is deployed", service.Name)
			services = append(services[:i], services[i+1:]...)
		}

		return nil
	}, 10*time.Minute, 10*time.Second).Should(Succeed())
}

func waitForSveltosResourcesDeleted(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) {
	GinkgoHelper()
	Eventually(func() error {
		ssList := &kcmv1.ServiceSetList{}
		By(fmt.Sprintf("List servicesets for cluster:%s\n", clusterName))
		if err := kc.CrClient.List(ctx, ssList, crclient.InNamespace(kc.Namespace),
			crclient.MatchingLabels{kubeutil.DefaultStateManagementProviderSelectorKey: kubeutil.DefaultStateManagementProviderSelectorValue}); err != nil {
			return err
		}
		if len(ssList.Items) > 0 {
			return fmt.Errorf("found %d ServiceSet CR(s) for cluster %q", len(ssList.Items), clusterName)
		}

		By(fmt.Sprintf("List profile for cluster:%s\n", clusterName))
		profileList := &addoncontrollerv1beta1.ProfileList{}
		if err := kc.CrClient.List(ctx, profileList, crclient.InNamespace(kc.Namespace), crclient.MatchingLabels{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue}); err != nil {
			return err
		}
		if len(profileList.Items) > 0 {
			return fmt.Errorf("found %d Profile CR(s) for cluster %q", len(profileList.Items), clusterName)
		}

		By(fmt.Sprintf("List ClusterSummaries for cluster:%s\n", clusterName))
		clusterSummaryList := &addoncontrollerv1beta1.ClusterSummaryList{}
		if err := kc.CrClient.List(ctx, clusterSummaryList, crclient.InNamespace(kc.Namespace)); err != nil {
			return err
		}
		if len(clusterSummaryList.Items) > 0 {
			return fmt.Errorf("found %d ClusterSummary CR(s) for cluster %q", len(clusterSummaryList.Items), clusterName)
		}

		return nil
	}, 10*time.Minute, 15*time.Second).Should(Succeed())
}
