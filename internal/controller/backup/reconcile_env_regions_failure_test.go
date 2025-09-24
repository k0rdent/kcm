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

var _ = Describe("Backup Controller Failure Cases", func() {
	const (
		testMgmtBackupName = "failure-test"
		invalidRegionName  = "invalid-region"
		validRegionName    = "valid-region"
		clusterDeployName  = "test-cluster-region"
		clusterTemplate    = "template-test"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	var mgmtBackup *kcmv1.ManagementBackup
	var validRegion *kcmv1.Region

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

		// Create a valid region
		validRegion = &kcmv1.Region{
			ObjectMeta: metav1.ObjectMeta{
				Name: validRegionName,
			},
			Spec: kcmv1.RegionSpec{
				KubeConfig: &fluxmeta.SecretKeyReference{},
			},
		}
		Expect(k8sClient.Create(ctx, validRegion)).To(Succeed())

		// Create a template
		template := &kcmv1.ClusterTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterTemplate,
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
		template.Status.Providers = kcmv1.Providers{"provider1", "provider2"}
		Expect(k8sClient.Status().Update(ctx, template)).To(Succeed())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(template), template)).To(Succeed())
			Expect(template.Status.Providers).To(HaveLen(2))
			return template.Status.Providers[0] == "provider1" && template.Status.Providers[1] == "provider2"
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())
	})

	AfterEach(func() {
		// Clean up
		if validRegion != nil {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, validRegion))).To(Succeed())
		}

		template := &kcmv1.ClusterTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: clusterTemplate, Namespace: "default"},
		}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, template))).To(Succeed())

		// Delete any cluster deployments
		validCluster := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: clusterDeployName + "-valid", Namespace: "default"},
		}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, validCluster))).To(Succeed())

		invalidCluster := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: clusterDeployName + "-invalid", Namespace: "default"},
		}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, invalidCluster))).To(Succeed())

		// Delete any velero backups
		deleteVeleroBackups()

		By("Deleting the ManagementBackup")
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, mgmtBackup))).To(Succeed())
	})

	It("Should handle missing region references gracefully", func() {
		// Create cluster deployment with invalid region
		invalidCluster := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterDeployName + "-invalid",
				Namespace: "default",
			},
			Spec: kcmv1.ClusterDeploymentSpec{
				Template: clusterTemplate,
			},
		}
		Expect(k8sClient.Create(ctx, invalidCluster)).To(Succeed())
		invalidCluster.Status.Region = invalidRegionName // does not exist
		Expect(k8sClient.Status().Update(ctx, invalidCluster)).To(Succeed())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(invalidCluster), invalidCluster)).To(Succeed())
			return invalidCluster.Status.Region == invalidRegionName
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

		// Create cluster with valid region
		validCluster := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterDeployName + "-valid",
				Namespace: "default",
			},
			Spec: kcmv1.ClusterDeploymentSpec{
				Template: clusterTemplate,
			},
		}
		Expect(k8sClient.Create(ctx, validCluster)).To(Succeed())
		validCluster.Status.Region = validRegionName
		Expect(k8sClient.Status().Update(ctx, validCluster)).To(Succeed())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(validCluster), validCluster)).To(Succeed())
			return validCluster.Status.Region == validRegionName
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

		controllerReconciler := NewReconciler(indexedClient, backupSystemNamespace, WithRegionalClientFactory(mockRegionalClientFactory))

		By("Reconciling ManagementBackup with valid and invalid regions")
		_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)

		// The reconciliation should complete but skip the invalid region
		Expect(err).To(Succeed())

		// Verify that management backup was created
		mgmtVeleroBackup := &velerov1.Backup{}
		Eventually(func() error {
			return k8sClient.Get(ctx, client.ObjectKey{Name: mgmtBackup.Name, Namespace: backupSystemNamespace}, mgmtVeleroBackup)
		}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

		// Verify that valid region backup was created
		validRegionBackupName := mgmtBackup.Name + "-" + validRegionName
		validRegionVeleroBackup := &velerov1.Backup{}
		Eventually(func() error {
			return k8sClient.Get(ctx, client.ObjectKey{Name: validRegionBackupName, Namespace: backupSystemNamespace}, validRegionVeleroBackup)
		}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

		// Verify no backup was created for invalid region
		invalidRegionBackupName := mgmtBackup.Name + "-" + invalidRegionName
		invalidRegionVeleroBackup := &velerov1.Backup{}
		Consistently(func() error {
			return k8sClient.Get(ctx, client.ObjectKey{Name: invalidRegionBackupName, Namespace: backupSystemNamespace}, invalidRegionVeleroBackup)
		}).WithTimeout(2 * time.Second).WithPolling(interval).Should(Not(Succeed()))

		// Check status - should have management and valid region but not invalid region
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())
		Expect(mgmtBackup.Status.LastBackupName).NotTo(BeEmpty())
		Expect(mgmtBackup.Status.LastBackupName).To(Equal(mgmtVeleroBackup.Name))
		Expect(mgmtBackup.Status.RegionsLastBackups).To(HaveLen(1))
		Expect(mgmtBackup.Status.RegionsLastBackups[0].Region).To(Equal(validRegionName))
		Expect(mgmtBackup.Status.RegionsLastBackups[0].LastBackupName).To(Equal(validRegionVeleroBackup.Name))
	})

	It("Should handle progressing backups correctly", func() {
		// Create cluster with valid region
		validCluster := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterDeployName + "-valid",
				Namespace: "default",
			},
			Spec: kcmv1.ClusterDeploymentSpec{
				Template: clusterTemplate,
			},
		}
		Expect(k8sClient.Create(ctx, validCluster)).To(Succeed())
		validCluster.Status.Region = validRegionName
		Expect(k8sClient.Status().Update(ctx, validCluster)).To(Succeed())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(validCluster), validCluster)).To(Succeed())
			return validCluster.Status.Region == validRegionName
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

		// Set up a scheduled backup
		mgmtBackup.Spec.Schedule = scheduleEvery6h
		Expect(k8sClient.Update(ctx, mgmtBackup)).To(Succeed())

		// Set last backup time to past to trigger backup creation
		mgmtBackup.Status.LastBackupTime = &metav1.Time{Time: time.Now().UTC().Add(-24 * time.Hour)}
		Expect(k8sClient.Status().Update(ctx, mgmtBackup)).To(Succeed())

		controllerReconciler := NewReconciler(indexedClient, backupSystemNamespace, WithRegionalClientFactory(mockRegionalClientFactory))

		// First reconciliation to create initial backups
		_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// Verify backups were created
		veleroBackups := &velerov1.BackupList{}
		Eventually(func() int {
			Expect(k8sClient.List(ctx, veleroBackups, client.InNamespace(backupSystemNamespace))).To(Succeed())
			return len(veleroBackups.Items)
		}).WithTimeout(timeout).WithPolling(interval).Should(Equal(2)) // Management + 1 region

		// Set one backup to InProgress
		var progressingBackup *velerov1.Backup
		for i := range veleroBackups.Items {
			progressingBackup = &veleroBackups.Items[i]
			break // Just use the first one
		}
		progressingBackup.Status.Phase = velerov1.BackupPhaseInProgress
		Expect(k8sClient.Update(ctx, progressingBackup)).To(Succeed())

		// Set last backup time to past again to trigger another backup attempt
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())
		mgmtBackup.Status.LastBackupTime = &metav1.Time{Time: time.Now().UTC().Add(-24 * time.Hour)}
		Expect(k8sClient.Status().Update(ctx, mgmtBackup)).To(Succeed())

		// Second reconciliation - should not create new backups due to InProgress
		_, err = controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// Verify no new backups were created
		newVeleroBackups := &velerov1.BackupList{}
		Eventually(func() int {
			Expect(k8sClient.List(ctx, newVeleroBackups, client.InNamespace(backupSystemNamespace))).To(Succeed())
			return len(newVeleroBackups.Items)
		}).WithTimeout(timeout).WithPolling(interval).Should(Equal(2)) // Still just 2
	})

	It("Should handle restoration of backups", func() {
		// Create cluster with valid region
		validCluster := &kcmv1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterDeployName + "-valid",
				Namespace: "default",
			},
			Spec: kcmv1.ClusterDeploymentSpec{
				Template: clusterTemplate,
			},
		}
		Expect(k8sClient.Create(ctx, validCluster)).To(Succeed())
		validCluster.Status.Region = validRegionName
		Expect(k8sClient.Status().Update(ctx, validCluster)).To(Succeed())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(validCluster), validCluster)).To(Succeed())
			return validCluster.Status.Region == validRegionName
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())

		controllerReconciler := NewReconciler(indexedClient, backupSystemNamespace, WithRegionalClientFactory(mockRegionalClientFactory))

		// First reconciliation to create initial backups
		_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// Verify backups were created
		veleroBackups := &velerov1.BackupList{}
		Eventually(func() int {
			Expect(k8sClient.List(ctx, veleroBackups, client.InNamespace(backupSystemNamespace))).To(Succeed())
			return len(veleroBackups.Items)
		}).WithTimeout(timeout).WithPolling(interval).Should(Equal(2)) // Management + 1 region

		// Set completed status on backups
		for i := range veleroBackups.Items {
			backup := &veleroBackups.Items[i]
			backup.Status.Phase = velerov1.BackupPhaseCompleted
			backup.Status.StartTimestamp = &metav1.Time{Time: time.Now()}
			backup.Status.CompletionTimestamp = &metav1.Time{Time: time.Now()}
			Expect(k8sClient.Update(ctx, backup)).To(Succeed())
		}

		// Get the current status
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())

		// Simulate restoration by adding velero labels
		mgmtBackup.Labels[velerov1.BackupNameLabel] = "test-backup"
		mgmtBackup.Labels[velerov1.RestoreNameLabel] = "test-restore"
		Expect(k8sClient.Update(ctx, mgmtBackup)).To(Succeed())

		// Clear status to simulate a fresh restoration
		mgmtBackup.Status = kcmv1.ManagementBackupStatus{}
		Expect(k8sClient.Status().Update(ctx, mgmtBackup)).To(Succeed())

		// Reconcile to handle restoration
		_, err = controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// Verify status was updated and restoration labels removed
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())
		ensureNoRestoreLabels(mgmtBackup)
		Expect(mgmtBackup.Status.LastBackupName).NotTo(BeEmpty())
		Expect(mgmtBackup.Status.RegionsLastBackups).To(HaveLen(1))
	})
})
