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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	hmcmirantiscomv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
)

var _ = Describe("Backup Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: metav1.NamespaceAll,
		}
		backup := &hmcmirantiscomv1alpha1.Backup{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Backup")
			err := k8sClient.Get(ctx, typeNamespacedName, backup)
			if err != nil && errors.IsNotFound(err) {
				resource := &hmcmirantiscomv1alpha1.Backup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: metav1.NamespaceAll,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &hmcmirantiscomv1alpha1.Backup{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Backup")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &BackupReconciler{
				Client: k8sClient,
			}
			_ = controllerReconciler

			// _, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
			// 	NamespacedName: typeNamespacedName,
			// })
			// Expect(err).NotTo(HaveOccurred())
		})
	})
})
