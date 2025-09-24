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

package backup

import (
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

var _ = Describe("Template Provider Backup Tests", func() {
	const (
		testMgmtBackupName = "template-provider-test"
		regionName         = "provider-region"

		templateA = "template-a"
		templateB = "template-b"
		templateC = "template-c"

		clusterDeployA = "cluster-deploy-a"
		clusterDeployB = "cluster-deploy-b"
		clusterDeployC = "cluster-deploy-c"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	var mgmtBackup *kcmv1.ManagementBackup
	var region *kcmv1.Region

	setupTemplatesAndDeployments := func() {
		// Create region
		region = &kcmv1.Region{
			ObjectMeta: metav1.ObjectMeta{
				Name: regionName,
			},
			Spec: kcmv1.RegionSpec{
				KubeConfig: &fluxmeta.SecretKeyReference{},
			},
		}
		Expect(k8sClient.Create(ctx, region)).To(Succeed())

		// Create templates with various providers
		templateProviders := map[string][]string{
			templateA: {"aws", "k0smotron", "infra-aws"},
			templateB: {"azure", "k0smotron", "infra-azure"},
			templateC: {"aws", "azure", "gcp"},
		}

		for name, providers := range templateProviders {
			template := &kcmv1.ClusterTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: kcmv1.ClusterTemplateSpec{
					Helm: kcmv1.HelmSpec{
						ChartRef: &helmcontrollerv2.CrossNamespaceSourceReference{
							Kind: sourcev1.HelmChartKind,
							Name: "fake-chart",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, template)).To(Succeed())
			template.Status.Providers = providers
			Expect(k8sClient.Status().Update(ctx, template)).To(Succeed())
			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(template), template)).To(Succeed())
				Expect(template.Status.Providers).To(HaveLen(len(providers)))
				for i, p := range providers {
					if template.Status.Providers[i] != p {
						return false
					}
				}
				return true
			}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())
		}

		// Create cluster deployments
		deployments := []struct {
			name     string
			template string
			region   string
		}{
			{clusterDeployA, templateA, ""},         // Management cluster deployment with AWS providers
			{clusterDeployB, templateB, regionName}, // Regional cluster deployment with Azure providers
			{clusterDeployC, templateC, ""},         // Another management cluster deployment with multiple providers
		}

		for _, d := range deployments {
			deployment := &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      d.name,
					Namespace: "default",
				},
				Spec: kcmv1.ClusterDeploymentSpec{
					Template: d.template,
				},
			}
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
			if d.region != "" {
				deployment.Status.Region = d.region
				Expect(k8sClient.Status().Update(ctx, deployment)).To(Succeed())
				Eventually(func() bool {
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
					return deployment.Status.Region == d.region
				}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())
			}
		}
	}

	cleanupTemplatesAndDeployments := func() {
		// Delete region
		if region != nil {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, region))).To(Succeed())
		}

		// Delete templates
		for _, name := range []string{templateA, templateB, templateC} {
			template := &kcmv1.ClusterTemplate{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, template))).To(Succeed())
		}

		// Delete deployments
		for _, name := range []string{clusterDeployA, clusterDeployB, clusterDeployC} {
			deployment := &kcmv1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, deployment))).To(Succeed())
		}

		// Delete backups
		deleteVeleroBackups()
	}

	BeforeEach(func() {
		// Create a new ManagementBackup
		mgmtBackup = &kcmv1.ManagementBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testMgmtBackupName,
				Namespace: metav1.NamespaceAll,
				Labels:    map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
			},
			Spec: kcmv1.ManagementBackupSpec{
				StorageLocation: "default",
			},
		}
		Expect(k8sClient.Create(ctx, mgmtBackup)).To(Succeed())

		// Set up templates and deployments
		setupTemplatesAndDeployments()
	})

	AfterEach(func() {
		// Clean up
		cleanupTemplatesAndDeployments()

		By("Deleting the ManagementBackup")
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, mgmtBackup))).To(Succeed())
	})

	It("Should include correct providers in backup selectors", func() {
		controllerReconciler := NewReconciler(indexedClient, backupSystemNamespace, WithRegionalClientFactory(mockRegionalClientFactory))

		By("Reconciling ManagementBackup with different template providers")
		_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// Verify backups were created
		veleroBackups := &velerov1.BackupList{}
		Eventually(func() int {
			Expect(k8sClient.List(ctx, veleroBackups, client.InNamespace(backupSystemNamespace))).To(Succeed())
			return len(veleroBackups.Items)
		}).WithTimeout(timeout).WithPolling(interval).Should(Equal(2)) // Management + 1 region

		// Find the management backup
		var mgmtVeleroBackup *velerov1.Backup
		var regionVeleroBackup *velerov1.Backup
		for i := range veleroBackups.Items {
			backup := &veleroBackups.Items[i]
			switch backup.Name {
			case mgmtBackup.Name:
				mgmtVeleroBackup = backup
			case mgmtBackup.Name + "-" + regionName:
				regionVeleroBackup = backup
			}
		}

		Expect(mgmtVeleroBackup).NotTo(BeNil(), "Management backup not found")
		Expect(regionVeleroBackup).NotTo(BeNil(), "Regional backup not found")

		// Refetch to ensure we have the latest spec
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtVeleroBackup), mgmtVeleroBackup)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(regionVeleroBackup), regionVeleroBackup)).To(Succeed())

		// Verify management backup has correct provider selectors
		// Should include AWS providers from template A and multiple providers from template C
		By("Checking management backup selectors")
		for _, provider := range []string{"aws", "k0smotron", "infra-aws", "gcp", "azure"} {
			found := false
			for _, selector := range mgmtVeleroBackup.Spec.OrLabelSelectors {
				if val, ok := selector.MatchLabels["cluster.x-k8s.io/provider"]; ok && val == provider {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "Provider "+provider+" not found in management backup selectors")
		}

		// Verify regional backup has correct provider selectors
		// Should only include Azure providers from template B
		By("Checking regional backup selectors")
		for _, provider := range []string{"azure", "k0smotron", "infra-azure"} {
			found := false
			for _, selector := range regionVeleroBackup.Spec.OrLabelSelectors {
				if val, ok := selector.MatchLabels["cluster.x-k8s.io/provider"]; ok && val == provider {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "Provider "+provider+" not found in regional backup selectors")
		}

		// Verify AWS provider not present in regional backup
		awsFound := false
		for _, selector := range regionVeleroBackup.Spec.OrLabelSelectors {
			if val, ok := selector.MatchLabels["cluster.x-k8s.io/provider"]; ok && val == "aws" {
				awsFound = true
				break
			}
		}
		Expect(awsFound).To(BeFalse(), "AWS provider should not be in regional backup selectors")

		// Verify Azure provider not present in management backup
		azureFound := false
		for _, selector := range mgmtVeleroBackup.Spec.OrLabelSelectors {
			if val, ok := selector.MatchLabels["cluster.x-k8s.io/provider"]; ok && val == "infra-azure" {
				azureFound = true
				break
			}
		}
		Expect(azureFound).To(BeFalse(), "Azure-specific provider should not be in management backup selectors")
	})

	It("Should deduplicate common providers across templates", func() {
		// Create another deployment with templateA but in the region
		commonProviderDeployment := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "common-provider-deployment",
				Namespace: "default",
			},
			Spec: kcmv1.ClusterDeploymentSpec{
				Template: templateA, // Has aws, k0smotron providers - should overlap with existing
			},
		}
		Expect(k8sClient.Create(ctx, commonProviderDeployment)).To(Succeed())
		commonProviderDeployment.Status.Region = regionName
		Expect(k8sClient.Status().Update(ctx, commonProviderDeployment)).To(Succeed())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(commonProviderDeployment), commonProviderDeployment)).To(Succeed())
			return commonProviderDeployment.Status.Region == regionName
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

		controllerReconciler := NewReconciler(indexedClient, backupSystemNamespace, WithRegionalClientFactory(mockRegionalClientFactory))

		By("Reconciling ManagementBackup with overlapping template providers")
		_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// Find the regional backup
		regionVeleroBackup := new(velerov1.Backup)
		Eventually(func() error {
			return k8sClient.Get(ctx, client.ObjectKey{
				Name:      mgmtBackup.Name + "-" + regionName,
				Namespace: backupSystemNamespace,
			}, regionVeleroBackup)
		}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

		// Count the provider selectors to check for deduplication
		providerCount := make(map[string]int)
		for _, selector := range regionVeleroBackup.Spec.OrLabelSelectors {
			if val, ok := selector.MatchLabels["cluster.x-k8s.io/provider"]; ok {
				providerCount[val]++
			}
		}

		// Common providers like "k0smotron" should only appear once
		for provider, count := range providerCount {
			Expect(count).To(Equal(1), "Provider "+provider+" appears multiple times, not properly deduplicated")
		}

		// The regional backup should include all providers from both templates
		for _, provider := range []string{"aws", "k0smotron", "infra-aws", "azure", "infra-azure"} {
			Expect(providerCount).To(HaveKey(provider), "Provider "+provider+" missing from regional backup selectors")
		}
	})
})
