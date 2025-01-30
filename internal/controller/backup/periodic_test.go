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
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
)

var _ = Describe("Runner", func() {
	var (
		r *Runner

		management = &kcmv1alpha1.Management{
			ObjectMeta: metav1.ObjectMeta{
				Name: kcmv1alpha1.ManagementName,
			},
		}
	)

	Describe("Start", func() {
		Context("when starting the runner", func() {
			fake := clientfake.NewClientBuilder().Build()

			It("should start successfully", func() {
				r = NewRunner(WithClient(fake), WithInterval(1*time.Minute))
				newCtx, newCancel := context.WithCancel(ctx)
				newCancel()
				Expect(r.Start(newCtx)).To(Succeed())
			})

			It("should fail when started twice", func() {
				r = NewRunner(WithClient(fake))
				newCtx, newCancel := context.WithCancel(ctx)
				newCancel()
				_ = r.Start(newCtx)
				Expect(r.Start(newCtx)).To(HaveOccurred())
			})

			It("should fail without a client", func() {
				r = NewRunner()
				newCtx, newCancel := context.WithCancel(ctx)
				newCancel()
				Expect(r.Start(newCtx)).To(HaveOccurred())
			})
		})
	})

	Describe("enqueueSchedulesOrIncompleteBackups", func() {
		Context("when Management is not found", func() {
			It("should return nil", func() {
				fake := clientfake.NewClientBuilder().
					WithScheme(k8sClient.Scheme()).
					Build()

				r = NewRunner(WithClient(fake))
				Expect(r.enqueueSchedulesOrIncompleteBackups(ctx)).To(Succeed())
			})
		})

		Context("when Management is being deleted", func() {
			It("should return nil", func() {
				fake := clientfake.NewClientBuilder().
					WithScheme(k8sClient.Scheme()).
					WithObjects(&kcmv1alpha1.Management{
						ObjectMeta: metav1.ObjectMeta{
							Name:              kcmv1alpha1.ManagementName,
							Finalizers:        []string{"foo-finalizer"},
							DeletionTimestamp: &metav1.Time{Time: time.Now()},
						},
					}).
					Build()

				r = NewRunner(WithClient(fake))
				Expect(r.enqueueSchedulesOrIncompleteBackups(ctx)).To(Succeed())
			})
		})

		Context("when no ManagementBackups are found", func() {
			It("should return errEmptyList", func() {
				fake := clientfake.NewClientBuilder().
					WithScheme(k8sClient.Scheme()).
					WithIndex(&kcmv1alpha1.ManagementBackup{}, kcmv1alpha1.ManagementBackupIndexKey, kcmv1alpha1.ExtractScheduledOrIncompleteBackups).
					WithObjects(management).
					Build()

				r = NewRunner(WithClient(fake))
				Expect(r.enqueueSchedulesOrIncompleteBackups(ctx)).To(MatchError(errEmptyList))
			})
		})

		Context("when ManagementBackups are found", func() {
			It("should enqueue the backups", func() {
				fake := clientfake.NewClientBuilder().
					WithScheme(k8sClient.Scheme()).
					WithIndex(&kcmv1alpha1.ManagementBackup{}, kcmv1alpha1.ManagementBackupIndexKey, kcmv1alpha1.ExtractScheduledOrIncompleteBackups).
					WithObjects(management, &kcmv1alpha1.ManagementBackup{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-backup",
						},
					}).
					Build()

				r = NewRunner(WithClient(fake))
				Eventually(func() bool {
					go func() {
						_ = r.enqueueSchedulesOrIncompleteBackups(ctx)
					}()
					select {
					case e := <-r.GetEventChannel():
						Expect(e.Object).To(BeAssignableToTypeOf(&kcmv1alpha1.ManagementBackup{}))
						return true
					default:
						return false
					}
				}, 1*time.Second, 50*time.Millisecond).Should(BeTrue())
			})
		})
	})

	Describe("Runner Options", func() {
		Context("when using WithClient", func() {
			It("should set the client", func() {
				r = NewRunner(WithClient(k8sClient))
				Expect(r.cl).To(Equal(k8sClient))
			})
		})

		Context("when using WithInterval", func() {
			It("should set the interval", func() {
				interval := 5 * time.Minute
				r = NewRunner(WithInterval(interval))
				Expect(r.interval).To(Equal(interval))
			})
		})
	})
})
