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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/controller/backup"
)

var _ = Describe("ManagementBackup Controller", func() {
	const testManagementBackupName = "test-mgmt-backup"

	var (
		mgmtBackup *kcmv1.ManagementBackup

		reconcileRequest = ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      testManagementBackupName,
				Namespace: metav1.NamespaceAll,
			},
		}
	)

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
		By("Deleting all Velero Backups")
		l := new(velerov1.BackupList)
		Expect(k8sClient.List(ctx, l)).To(Succeed())

		for _, v := range l.Items {
			Expect(k8sClient.Delete(ctx, &v)).To(Succeed())
		}

		By("Deleting a ManagementBackup")
		Expect(k8sClient.Delete(ctx, mgmtBackup)).To(Succeed())
	})

	It("Should reconcile a ManagementBackup", func() {
		controllerReconciler := &ManagementBackupReconciler{
			Client:          mgrClient,
			SystemNamespace: metav1.NamespaceDefault,
			internal:        backup.NewReconciler(mgrClient, metav1.NamespaceDefault),
		}

		_, err := controllerReconciler.Reconcile(ctx, reconcileRequest)
		Expect(err).To(Succeed())
	})
})
