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

package clusterdeployment

import (
	"context"
	"fmt"
	"time"

	hcv2 "github.com/fluxcd/helm-controller/api/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1alpha1"
	internalutils "github.com/K0rdent/kcm/internal/utils"
)

func Upgrade(ctx context.Context, cl crclient.Client, namespace, clusterName, newTemplate string) {
	cluster := &kcmv1.ClusterDeployment{}
	err := cl.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      clusterName,
	}, cluster)
	Expect(err).NotTo(HaveOccurred())

	patch := crclient.MergeFrom(cluster.DeepCopy())
	cluster.Spec.Template = newTemplate
	err = cl.Patch(ctx, cluster, patch)
	Expect(err).NotTo(HaveOccurred())

	template := &kcmv1.ClusterTemplate{}
	err = cl.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      newTemplate,
	}, template)
	Expect(err).NotTo(HaveOccurred())

	Eventually(func() bool {
		errorMessage, upgraded := validateClusterUpgraded(ctx, cl, internalutils.DefaultSystemNamespace, clusterName, template.Status.ChartRef.Name)
		if !upgraded {
			_, _ = fmt.Fprintf(GinkgoWriter, errorMessage, "\n")
			return false
		}
		return true
	}, 20*time.Minute, 20*time.Second).Should(BeTrue())
}

func validateClusterUpgraded(ctx context.Context, cl crclient.Client, clusterNamespace, clusterName, chartName string) (string, bool) {
	hr := &hcv2.HelmRelease{}
	err := cl.Get(ctx, types.NamespacedName{
		Namespace: clusterNamespace,
		Name:      clusterName,
	}, hr)
	if err != nil {
		return fmt.Sprintf("failed to get %s/%s HelmRelease %v", clusterNamespace, clusterName, err), false
	}
	if hr.Spec.ChartRef.Name != chartName {
		return fmt.Sprintf("waiting for chartName to be updated in %s/%s HelmRelease", clusterNamespace, clusterName), false
	}
	readyCondition := apimeta.FindStatusCondition(hr.GetConditions(), kcmv1.ReadyCondition)
	if readyCondition == nil {
		return fmt.Sprintf("waiting for %s/%s HelmRelease to have Ready condition", clusterNamespace, clusterName), false
	}
	if readyCondition.ObservedGeneration != hr.Generation {
		return "waiting for status.observedGeneration to be updated", false
	}
	if readyCondition.Status != metav1.ConditionTrue {
		return "waiting for Ready condition to have status: true", false
	}
	if readyCondition.Reason != hcv2.UpgradeSucceededReason {
		return "waiting for Ready condition to have `UpgradeSucceeded` reason", false
	}
	return "", true
}
