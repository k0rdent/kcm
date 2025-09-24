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

package backup

import (
	"errors"
	"fmt"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

var _ = Describe("Internal ManagementBackup Controller", func() {
	const (
		testManagementBackupName = "test-mgmt-backup"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250

		scheduleMgmtNameLabel = "k0rdent.mirantis.com/management-backup"
	)

	var mgmtBackup *kcmv1.ManagementBackup

	BeforeEach(func() {
		By("Creating a new ManagementBackup")
		mgmtBackup = &kcmv1.ManagementBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testManagementBackupName,
				Namespace: metav1.NamespaceAll,
				Labels:    map[string]string{kcmv1.GenericComponentNameLabel: kcmv1.GenericComponentLabelValueKCM},
			},
			Spec: kcmv1.ManagementBackupSpec{
				StorageLocation: "default",
			},
		}
		Expect(k8sClient.Create(ctx, mgmtBackup)).To(Succeed())
	})

	AfterEach(func() {
		deleteVeleroBackups()

		By("Deleting a ManagementBackup")
		Expect(k8sClient.Delete(ctx, mgmtBackup)).To(Succeed())
	})

	It("Should test single and schedule ManagementBackup", func() {
		controllerReconciler := NewReconciler(k8sClient, backupSystemNamespace)

		By("Reconciling created ManagementBackup without schedule", func() {
			_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
			Expect(err).To(Succeed())

			// verify that a velero Backup is created
			veleroBackup := &velerov1.Backup{}
			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKey{Name: mgmtBackup.Name, Namespace: backupSystemNamespace}, veleroBackup)
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

			// refetch mgmt backup for the status update
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())

			Expect(mgmtBackup.Status.LastBackupName).To(Equal(veleroBackup.Name))
			Expect(mgmtBackup.Spec.StorageLocation).To(Equal(veleroBackup.Spec.StorageLocation))
			Expect(veleroBackup.Spec.OrLabelSelectors).To(HaveExactElements(getDefaultVeleroSpec().OrLabelSelectors))
		})

		By("Reconciling updated ManagementBackup with valid schedule", func() {
			const testSchedule = "@every 1h" // need to schedule to be after the now

			// prepare the spec properly and expect it before reconcile
			mgmtBackup.Spec.Schedule = testSchedule
			Expect(k8sClient.Update(ctx, mgmtBackup)).To(Succeed())

			minusOneDay := &metav1.Time{Time: time.Now().Add(-time.Hour * 24).UTC()}
			mgmtBackup.Status.LastBackupTime = minusOneDay // hack time without fake clocks
			Expect(k8sClient.Status().Update(ctx, mgmtBackup)).To(Succeed())

			// wait object to be actually updated
			Eventually(func() error {
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup); err != nil {
					return err
				}
				if !mgmtBackup.IsSchedule() {
					return fmt.Errorf("expected to mgmtbackup have schedule %s", testSchedule)
				}

				ts, err := time.Parse(time.DateTime, minusOneDay.Format(time.DateTime))
				if err != nil {
					return err
				}
				mts := &metav1.Time{Time: ts}

				if !mgmtBackup.Status.LastBackupTime.Equal(mts) {
					return fmt.Errorf("last backup %s is still not expected %s", mgmtBackup.Status.LastBackupTime.String(), mts.String())
				}
				return nil
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

			_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
			Expect(err).To(Succeed())

			// verify that a velero Backup is created
			veleroBackup := &velerov1.Backup{}
			Eventually(func() error {
				scheduled := new(velerov1.BackupList)
				if err := k8sClient.List(ctx, scheduled, client.MatchingLabels{scheduleMgmtNameLabel: mgmtBackup.Name}); err != nil {
					return err
				}

				if ll := len(scheduled.Items); ll != 1 {
					return fmt.Errorf("expected to have exactly 1 backup after the schedule, got %d", ll)
				}

				veleroBackup = &scheduled.Items[0]
				return nil
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

			// refetch mgmt backup for the status update
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())

			Expect(mgmtBackup.Status.LastBackupName).To(Equal(veleroBackup.Name))
			Expect(mgmtBackup.Status.NextAttempt).NotTo(BeNil())
			Expect(mgmtBackup.Spec.StorageLocation).To(Equal(veleroBackup.Spec.StorageLocation))
			Expect(veleroBackup.Spec.OrLabelSelectors).To(HaveExactElements(getDefaultVeleroSpec().OrLabelSelectors))
		})
	})

	It("It should correctly restore ManagementBackup status after restoration", func() {
		controllerReconciler := NewReconciler(k8sClient, backupSystemNamespace)

		By("Creating velero backups with status")
		now := &metav1.Time{Time: time.Now().UTC()}
		backupStatus := velerov1.BackupStatus{
			Phase:               velerov1.BackupPhaseCompleted,
			Expiration:          &metav1.Time{Time: now.Add(time.Hour * 24 * 30)},
			StartTimestamp:      now,
			CompletionTimestamp: &metav1.Time{Time: now.Add(time.Minute)},
		}

		singleVeleroBackup := &velerov1.Backup{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Backup",
				APIVersion: velerov1.SchemeGroupVersion.Group,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      mgmtBackup.Name,
				Namespace: backupSystemNamespace,
			},
			Spec:   getDefaultVeleroSpec(),
			Status: backupStatus,
		}

		scheduleVeleroBackup := singleVeleroBackup.DeepCopy()
		scheduleVeleroBackup.Labels = map[string]string{scheduleMgmtNameLabel: mgmtBackup.Name}
		scheduleVeleroBackup.Name = mgmtBackup.TimestampedBackupName(now.Time, "")

		Expect(k8sClient.Create(ctx, scheduleVeleroBackup)).To(Succeed())
		Expect(k8sClient.Create(ctx, singleVeleroBackup)).To(Succeed())

		By("Reconciling a restored ManagementBackup without schedule twice")

		const dummyValue = "dummy"
		mgmtBackup.Labels[velerov1.BackupNameLabel] = dummyValue
		mgmtBackup.Labels[velerov1.RestoreNameLabel] = dummyValue
		Expect(k8sClient.Update(ctx, mgmtBackup)).To(Succeed())
		// wait object to be actually updated
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup); err != nil {
				return err
			}

			for _, v := range [2]string{
				velerov1.BackupNameLabel,
				velerov1.RestoreNameLabel,
			} {
				if _, ok := mgmtBackup.Labels[v]; !ok {
					return fmt.Errorf("expected to thave %s label", v)
				}
			}

			return nil
		}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

		_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// refetch mgmt backup for the status update
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())

		Expect(mgmtBackup.Status.Error).To(BeEmpty())
		Expect(mgmtBackup.Status.LastBackupTime).To(Equal(singleVeleroBackup.Status.StartTimestamp))
		Expect(mgmtBackup.Status.LastBackupName).To(Equal(singleVeleroBackup.Name))
		Expect(mgmtBackup.Status.LastBackup).NotTo(BeNil())
		Expect(*mgmtBackup.Status.LastBackup).To(Equal(singleVeleroBackup.Status))

		_, err = controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// refetch mgmt backup for the update
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())

		ensureNoRestoreLabels(mgmtBackup)

		By("Reconciling a restored ManagementBackup with schedule")

		mgmtBackup.Spec.Schedule = "@every 2h" // won't be checked
		mgmtBackup.Labels[velerov1.BackupNameLabel] = dummyValue
		mgmtBackup.Labels[velerov1.RestoreNameLabel] = dummyValue
		Expect(k8sClient.Update(ctx, mgmtBackup)).To(Succeed())
		mgmtBackup.Status = kcmv1.ManagementBackupStatus{}
		Expect(k8sClient.Status().Update(ctx, mgmtBackup)).To(Succeed())
		// wait object to be actually updated
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup); err != nil {
				return err
			}

			if !mgmtBackup.IsSchedule() {
				return errors.New("expected to have schedule")
			}

			for _, v := range [2]string{
				velerov1.BackupNameLabel,
				velerov1.RestoreNameLabel,
			} {
				if _, ok := mgmtBackup.Labels[v]; !ok {
					return fmt.Errorf("expected to thave %s label", v)
				}
			}

			return nil
		}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

		_, err = controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// refetch mgmt backup for the status update
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())

		Expect(mgmtBackup.Status.Error).To(BeEmpty())
		Expect(mgmtBackup.Status.LastBackupTime).To(Equal(scheduleVeleroBackup.Status.StartTimestamp))
		Expect(mgmtBackup.Status.LastBackupName).To(Equal(scheduleVeleroBackup.Name))
		Expect(mgmtBackup.Status.LastBackup).NotTo(BeNil())
		Expect(*mgmtBackup.Status.LastBackup).To(Equal(scheduleVeleroBackup.Status))

		_, err = controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// refetch mgmt backup for the update
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())

		ensureNoRestoreLabels(mgmtBackup)

		By("Reconciling any restored ManagementBackup without velero backups")

		deleteVeleroBackups()
		mgmtBackup.Labels[velerov1.BackupNameLabel] = dummyValue
		mgmtBackup.Labels[velerov1.RestoreNameLabel] = dummyValue
		Expect(k8sClient.Update(ctx, mgmtBackup)).To(Succeed())
		mgmtBackup.Status = kcmv1.ManagementBackupStatus{}
		Expect(k8sClient.Status().Update(ctx, mgmtBackup)).To(Succeed())
		// wait object to be actually updated
		Eventually(func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup); err != nil {
				return err
			}

			for _, v := range [2]string{
				velerov1.BackupNameLabel,
				velerov1.RestoreNameLabel,
			} {
				if _, ok := mgmtBackup.Labels[v]; !ok {
					return fmt.Errorf("expected to thave %s label", v)
				}
			}

			return nil
		}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

		_, err = controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// refetch mgmt backup for the update
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())

		Expect(mgmtBackup.Status.LastBackupName).To(BeEmpty())
		Expect(mgmtBackup.Status.LastBackup).To(BeNil())
		Expect(mgmtBackup.Status.LastBackupTime).To(BeNil())
		ensureNoRestoreLabels(mgmtBackup)
	})

	It("Should not create new velero Backups if they are progressing", func() {
		controllerReconciler := NewReconciler(k8sClient, backupSystemNamespace)

		mgmtBackup.Spec.Schedule = scheduleEvery6h
		Expect(k8sClient.Update(ctx, mgmtBackup)).To(Succeed())
		mgmtBackup.Status.LastBackupTime = &metav1.Time{Time: time.Now().UTC().Add(-time.Hour * 24)} // isDue -> true
		Expect(k8sClient.Status().Update(ctx, mgmtBackup)).To(Succeed())

		By("Creating a progressing Velero backup spawned by a ManagementBackup")

		scheduleVeleroBackup := &velerov1.Backup{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Backup",
				APIVersion: velerov1.SchemeGroupVersion.Group,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      mgmtBackup.TimestampedBackupName(time.Now().UTC(), ""),
				Namespace: backupSystemNamespace,
				Labels:    map[string]string{scheduleMgmtNameLabel: mgmtBackup.Name},
			},
			Spec: getDefaultVeleroSpec(),
			Status: velerov1.BackupStatus{
				Phase: velerov1.BackupPhaseInProgress,
			},
		}

		Expect(k8sClient.Create(ctx, scheduleVeleroBackup)).To(Succeed())

		By("Reconciling a ManagementBackup with schedule")
		_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// refetch mgmt backup for the update
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())

		Expect(mgmtBackup.Status.LastBackupName).To(BeEmpty())
		Expect(mgmtBackup.Status.LastBackupTime).NotTo(BeNil())
		Expect(mgmtBackup.Status.LastBackup).To(BeNil())
		Expect(mgmtBackup.Status.NextAttempt).NotTo(BeNil())

		// check that no new backups have been created
		l := new(velerov1.BackupList)
		Expect(k8sClient.List(ctx, l)).Should(Succeed())
		Expect(l.Items).To(HaveLen(1))
		Expect(l.Items[0].Name).To(Equal(scheduleVeleroBackup.Name))
		Expect(l.Items[0].Status).To(Equal(scheduleVeleroBackup.Status))
	})

	It("Should propagate velero Backup status", func() {
		controllerReconciler := NewReconciler(k8sClient, backupSystemNamespace)

		mgmtBackup.Spec.Schedule = scheduleEvery6h
		Expect(k8sClient.Update(ctx, mgmtBackup)).To(Succeed())
		veleroBackupName := mgmtBackup.TimestampedBackupName(time.Now().UTC(), "")
		mgmtBackup.Status.LastBackupName = veleroBackupName

		Expect(k8sClient.Status().Update(ctx, mgmtBackup)).To(Succeed())

		By("Creating a some Velero backup spawned by a ManagementBackup")

		scheduleVeleroBackup := &velerov1.Backup{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Backup",
				APIVersion: velerov1.SchemeGroupVersion.Group,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      veleroBackupName,
				Namespace: backupSystemNamespace,
				Labels:    map[string]string{scheduleMgmtNameLabel: mgmtBackup.Name},
			},
			Spec: getDefaultVeleroSpec(),
			Status: velerov1.BackupStatus{
				Phase: velerov1.BackupPhaseFailed,
			},
		}

		Expect(k8sClient.Create(ctx, scheduleVeleroBackup)).To(Succeed())

		By("Reconciling a ManagementBackup with schedule")
		_, err := controllerReconciler.ReconcileBackup(ctx, mgmtBackup)
		Expect(err).To(Succeed())

		// refetch mgmt backup for the update
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mgmtBackup), mgmtBackup)).To(Succeed())

		Expect(mgmtBackup.Status.LastBackupName).To(Equal(scheduleVeleroBackup.Name))
		Expect(mgmtBackup.Status.LastBackup).NotTo(BeNil())
		Expect(mgmtBackup.Status.LastBackupTime).To(BeNil()) // because nothing had set it
		Expect(*mgmtBackup.Status.LastBackup).To(Equal(scheduleVeleroBackup.Status))
		Expect(mgmtBackup.Status.NextAttempt).NotTo(BeNil())
	})
})

func ensureNoRestoreLabels(o *kcmv1.ManagementBackup) {
	Expect(o).NotTo(BeNil())
	Expect(o.Labels).NotTo(HaveKey(velerov1.BackupNameLabel))
	Expect(o.Labels).NotTo(HaveKey(velerov1.RestoreNameLabel))
}

func getDefaultVeleroSpec() velerov1.BackupSpec {
	GinkgoHelper()

	selector := func(k, v string) *metav1.LabelSelector {
		return &metav1.LabelSelector{
			MatchLabels: map[string]string{k: v},
		}
	}

	return velerov1.BackupSpec{
		IncludedNamespaces: []string{"*"},
		ExcludedResources:  []string{"clusters.cluster.x-k8s.io"},
		TTL:                metav1.Duration{Duration: 30 * 24 * time.Hour}, // velero's default, set it for the sake of UX
		OrLabelSelectors: []*metav1.LabelSelector{
			selector(clusterapiv1.ProviderNameLabel, "cluster-api"),
			selector(certmanagerv1.PartOfCertManagerControllerLabelKey, "true"),
			selector(kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM),
		},
	}
}

func deleteVeleroBackups() {
	By("Deleting all Velero Backups")
	l := new(velerov1.BackupList)
	Expect(k8sClient.List(ctx, l)).To(Succeed())

	for _, v := range l.Items {
		Expect(k8sClient.Delete(ctx, &v)).To(Succeed())
	}
}
