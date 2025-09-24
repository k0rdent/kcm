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

var _ = Describe("Multi-Region Backup Controller", func() {
	const (
		testMgmtBackupName = "multi-region-test"
		region1Name        = "region1"
		region2Name        = "region2"
		clusterDeployName1 = "test-cluster-mgmt"
		clusterDeployName2 = "test-cluster-region1"
		clusterDeployName3 = "test-cluster-region2"
		clusterTemplate1   = "template1"
		clusterTemplate2   = "template2"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	var mgmtBackup *kcmv1.ManagementBackup
	var region1 *kcmv1.Region
	var region2 *kcmv1.Region

	// Set up the test environment with regions and cluster templates
	setupTestEnvironment := func() {
		// Create regions
		region1 = &kcmv1.Region{
			ObjectMeta: metav1.ObjectMeta{
				Name: region1Name,
			},
			Spec: kcmv1.RegionSpec{
				KubeConfig: &fluxmeta.SecretKeyReference{},
			},
		}
		Expect(k8sClient.Create(ctx, region1)).To(Succeed())

		region2 = &kcmv1.Region{
			ObjectMeta: metav1.ObjectMeta{
				Name: region2Name,
			},
			Spec: kcmv1.RegionSpec{
				KubeConfig: &fluxmeta.SecretKeyReference{},
			},
		}
		Expect(k8sClient.Create(ctx, region2)).To(Succeed())

		// Create cluster templates with different providers
		template1 := &kcmv1.ClusterTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterTemplate1,
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
		Expect(k8sClient.Create(ctx, template1)).To(Succeed())
		template1.Status.Providers = kcmv1.Providers{"provider1", "provider2"}
		Expect(k8sClient.Status().Update(ctx, template1)).To(Succeed())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(template1), template1)).To(Succeed())
			Expect(template1.Status.Providers).To(HaveLen(2))
			return template1.Status.Providers[0] == "provider1" && template1.Status.Providers[1] == "provider2"
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

		template2 := &kcmv1.ClusterTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterTemplate2,
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
		Expect(k8sClient.Create(ctx, template2)).To(Succeed())
		template2.Status.Providers = kcmv1.Providers{"provider3", "provider4"}
		Expect(k8sClient.Status().Update(ctx, template2)).To(Succeed())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(template2), template2)).To(Succeed())
			Expect(template2.Status.Providers).To(HaveLen(2))
			return template2.Status.Providers[0] == "provider3" && template2.Status.Providers[1] == "provider4"
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

		// Create cluster deployments for management and regional clusters
		mgmtCld := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterDeployName1,
				Namespace: "default",
			},
			Spec: kcmv1.ClusterDeploymentSpec{
				Template: clusterTemplate1,
			},
		}
		Expect(k8sClient.Create(ctx, mgmtCld)).To(Succeed())

		region1Cld := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterDeployName2,
				Namespace: "default",
			},
			Spec: kcmv1.ClusterDeploymentSpec{
				Template: clusterTemplate1,
			},
		}
		Expect(k8sClient.Create(ctx, region1Cld)).To(Succeed())
		region1Cld.Status.Region = region1Name
		Expect(k8sClient.Status().Update(ctx, region1Cld)).To(Succeed())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(region1Cld), region1Cld)).To(Succeed())
			return region1Cld.Status.Region == region1Name
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

		region2Cld := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterDeployName3,
				Namespace: "default",
			},
			Spec: kcmv1.ClusterDeploymentSpec{
				Template: clusterTemplate2,
			},
		}
		Expect(k8sClient.Create(ctx, region2Cld)).To(Succeed())
		region2Cld.Status.Region = region2Name
		Expect(k8sClient.Status().Update(ctx, region2Cld)).To(Succeed())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(region2Cld), region2Cld)).To(Succeed())
			return region2Cld.Status.Region == region2Name
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())
	}

	cleanupTestEnvironment := func() {
		// Delete all test resources
		if region1 != nil {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, region1))).To(Succeed())
		}
		if region2 != nil {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, region2))).To(Succeed())
		}

		// Delete templates
		template1 := &kcmv1.ClusterTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: clusterTemplate1, Namespace: "default"},
		}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, template1))).To(Succeed())

		template2 := &kcmv1.ClusterTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: clusterTemplate2, Namespace: "default"},
		}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, template2))).To(Succeed())

		// Delete cluster deployments
		mgmtCluster := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: clusterDeployName1, Namespace: "default"},
		}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, mgmtCluster))).To(Succeed())

		region1Cluster := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: clusterDeployName2, Namespace: "default"},
		}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, region1Cluster))).To(Succeed())

		region2Cluster := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: clusterDeployName3, Namespace: "default"},
		}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, region2Cluster))).To(Succeed())

		// Delete any velero backups
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

		// Setup test environment
		setupTestEnvironment()
	})

	AfterEach(func() {
		// Clean up
		cleanupTestEnvironment()

		By("Deleting the ManagementBackup")
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, mgmtBackup))).To(Succeed())
	})

	It("Should create backups for multiple regions", func() {
		controllerReconciler := NewReconciler(indexedClient, backupSystemNamespace, WithRegionalClientFactory(mockRegionalClientFactory))

		By("Reconciling ManagementBackup with multiple regions")
		_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// Verify that a velero Backup is created for management cluster
		mgmtVeleroBackup := &velerov1.Backup{}
		Eventually(func() error {
			return k8sClient.Get(ctx, client.ObjectKey{Name: mgmtBackup.Name, Namespace: backupSystemNamespace}, mgmtVeleroBackup)
		}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

		// Verify that velero Backups are created for each region
		region1BackupName := mgmtBackup.Name + "-" + region1Name
		region1VeleroBackup := &velerov1.Backup{}
		Eventually(func() error {
			return k8sClient.Get(ctx, client.ObjectKey{Name: region1BackupName, Namespace: backupSystemNamespace}, region1VeleroBackup)
		}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

		region2BackupName := mgmtBackup.Name + "-" + region2Name
		region2VeleroBackup := &velerov1.Backup{}
		Eventually(func() error {
			return k8sClient.Get(ctx, client.ObjectKey{Name: region2BackupName, Namespace: backupSystemNamespace}, region2VeleroBackup)
		}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

		// Refetch management backup for the status update
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())

		// Verify management backup status
		Expect(mgmtBackup.Status.LastBackupName).To(Equal(mgmtVeleroBackup.Name))

		// Verify region backups are in status
		Expect(mgmtBackup.Status.RegionsLastBackups).To(HaveLen(2))

		// Find region1 backup in status
		region1Found := false
		region2Found := false

		for _, rb := range mgmtBackup.Status.RegionsLastBackups {
			if rb.Region == region1Name {
				region1Found = true
				Expect(rb.LastBackupName).To(Equal(region1BackupName))
			}
			if rb.Region == region2Name {
				region2Found = true
				Expect(rb.LastBackupName).To(Equal(region2BackupName))
			}
		}

		Expect(region1Found).To(BeTrue(), "Region1 backup status not found")
		Expect(region2Found).To(BeTrue(), "Region2 backup status not found")

		Expect(mgmtVeleroBackup.Spec.OrLabelSelectors).To(ContainElement(
			&metav1.LabelSelector{MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "provider1"}}))
		Expect(mgmtVeleroBackup.Spec.OrLabelSelectors).To(ContainElement(
			&metav1.LabelSelector{MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "provider2"}}))

		Expect(region1VeleroBackup.Spec.OrLabelSelectors).To(ContainElement(
			&metav1.LabelSelector{MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "provider1"}}))
		Expect(region1VeleroBackup.Spec.OrLabelSelectors).To(ContainElement(
			&metav1.LabelSelector{MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "provider2"}}))

		Expect(region2VeleroBackup.Spec.OrLabelSelectors).To(ContainElement(
			&metav1.LabelSelector{MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "provider3"}}))
		Expect(region2VeleroBackup.Spec.OrLabelSelectors).To(ContainElement(
			&metav1.LabelSelector{MatchLabels: map[string]string{"cluster.x-k8s.io/provider": "provider4"}}))
	})

	It("Should handle scheduled backups with multiple regions", func() {
		controllerReconciler := NewReconciler(indexedClient, backupSystemNamespace, WithRegionalClientFactory(mockRegionalClientFactory))

		By("Updating ManagementBackup with schedule")
		mgmtBackup.Spec.Schedule = scheduleEvery6h
		Expect(k8sClient.Update(ctx, mgmtBackup)).To(Succeed())

		// Set last backup time to past to trigger backup creation
		mgmtBackup.Status.LastBackupTime = &metav1.Time{Time: time.Now().UTC().Add(-24 * time.Hour)}
		Expect(k8sClient.Status().Update(ctx, mgmtBackup)).To(Succeed())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())
			return mgmtBackup.Status.LastBackupTime != nil
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

		By("Reconciling scheduled ManagementBackup with multiple regions")
		_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// Verify that scheduled velero Backups are created
		veleroBackups := &velerov1.BackupList{}
		Eventually(func() int {
			Expect(k8sClient.List(ctx, veleroBackups, client.InNamespace(backupSystemNamespace))).To(Succeed())
			return len(veleroBackups.Items)
		}).WithTimeout(timeout).WithPolling(interval).Should(Equal(3)) // Management + 2 regions

		// Refetch management backup for the status update
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())

		// Verify next attempt time is set for management and all regions
		Expect(mgmtBackup.Status.NextAttempt).NotTo(BeNil())
		for _, rb := range mgmtBackup.Status.RegionsLastBackups {
			Expect(rb.NextAttempt).NotTo(BeNil())
			// All next attempt times should match
			Expect(rb.NextAttempt).To(Equal(mgmtBackup.Status.NextAttempt))
		}

		// Check for schedule label on backups
		for _, backup := range veleroBackups.Items {
			Expect(backup.Labels).To(HaveKeyWithValue(scheduleMgmtNameLabel, mgmtBackup.Name))
		}
	})

	It("Should handle updates to backup status for multiple regions", func() {
		controllerReconciler := NewReconciler(indexedClient, backupSystemNamespace, WithRegionalClientFactory(mockRegionalClientFactory))

		_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// Create some status for the velero backups
		veleroBackups := &velerov1.BackupList{}
		Expect(k8sClient.List(ctx, veleroBackups, client.InNamespace(backupSystemNamespace))).To(Succeed())
		Expect(veleroBackups.Items).To(HaveLen(3)) // Management + 2 regions

		// Update status of all backups to Completed
		for i := range veleroBackups.Items {
			backup := veleroBackups.Items[i].DeepCopy()
			backup.Status.Phase = velerov1.BackupPhaseCompleted
			Expect(k8sClient.Update(ctx, backup)).To(Succeed())
			Eventually(func() velerov1.BackupPhase {
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(backup), backup)).To(Succeed())
				return backup.Status.Phase
			}).WithTimeout(timeout).WithPolling(interval).Should(Equal(velerov1.BackupPhaseCompleted))
		}

		// Reconcile again to update status
		_, err = controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// Verify status is updated
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())
		Expect(mgmtBackup.Status.LastBackup).NotTo(BeNil())
		Expect(mgmtBackup.Status.LastBackup.Phase).To(Equal(velerov1.BackupPhaseCompleted))

		// Check all regional backups
		for _, rb := range mgmtBackup.Status.RegionsLastBackups {
			Expect(rb.LastBackup).NotTo(BeNil())
			Expect(rb.LastBackup.Phase).To(Equal(velerov1.BackupPhaseCompleted))
		}
	})
})
