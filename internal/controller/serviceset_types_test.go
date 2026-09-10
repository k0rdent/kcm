// Copyright 2026
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// The mutual exclusion of .spec.multiClusterService and .spec.namespacedMultiClusterService is
// enforced only by a CEL rule on ServiceSetSpec, so it is exercised here against a real API
// server: nothing in the Go types would catch the rule being dropped or mistyped.
var _ = Describe("ServiceSet spec validation", func() {
	newServiceSet := func(name string) *kcmv1.ServiceSet {
		return &kcmv1.ServiceSet{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: metav1.NamespaceDefault},
			Spec: kcmv1.ServiceSetSpec{
				Cluster:  "test-cluster",
				Provider: kcmv1.StateManagementProviderConfig{Name: "sveltos"},
			},
		}
	}

	It("should reject a ServiceSet setting both multiClusterService and namespacedMultiClusterService", func() {
		ss := newServiceSet("both-owners")
		ss.Spec.MultiClusterService = "mcs"
		ss.Spec.NamespacedMultiClusterService = "ns/nmcs"

		err := k8sClient.Create(ctx, ss)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("only one of spec.multiClusterService or spec.namespacedMultiClusterService can be set"))
	})

	It("should accept a ServiceSet setting either one alone, or neither", func() {
		for name, mutate := range map[string]func(*kcmv1.ServiceSet){
			"mcs-only":  func(ss *kcmv1.ServiceSet) { ss.Spec.MultiClusterService = "mcs" },
			"nmcs-only": func(ss *kcmv1.ServiceSet) { ss.Spec.NamespacedMultiClusterService = "ns/nmcs" },
			"neither":   func(*kcmv1.ServiceSet) {},
		} {
			ss := newServiceSet(name)
			mutate(ss)

			Expect(k8sClient.Create(ctx, ss)).To(Succeed(), "expected %s to be accepted", name)
			Expect(k8sClient.Delete(ctx, ss)).To(Succeed())
		}
	})
})
