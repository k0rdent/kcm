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

package multiclusterservice

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	statusutil "github.com/K0rdent/kcm/internal/util/status"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	validationutil "github.com/K0rdent/kcm/test/util/validation"
)

// BuildMultiClusterService constructs a MultiClusterService spec for the given ClusterDeployment.
func BuildMultiClusterService(cd *kcmv1.ClusterDeployment, multiClusterServiceTemplate, serviceNamespace, multiClusterServiceMatchLabel, name string) *kcmv1.MultiClusterService {
	return &kcmv1.MultiClusterService{
		Kind:      kcmv1.MultiClusterServiceKind,
		Name:      name,
		Namespace: cd.Namespace,
		Spec: kcmv1.MultiClusterServiceSpec{
			ClusterSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					multiClusterServiceMatchLabel: cd.Name,
				},
			},
			ServiceSpec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{},
				Services: []kcmv1.Service{
					{
						Name:      multiClusterServiceTemplate,
						Namespace: serviceNamespace,
						Template:  multiClusterServiceTemplate,
					},
				},
			},
		},
	}
}

func CreateMultiClusterService(ctx context.Context, cl client.Client, mcs *kcmv1.MultiClusterService) {
	Expect(mcs).NotTo(BeNil())
	Expect(mcs.Kind).To(Equal(kcmv1.MultiClusterServiceKind))

	Eventually(func() error {
		err := client.IgnoreAlreadyExists(cl.Create(ctx, mcs))
		if err != nil {
			logs.WarnErrorf(err, "failed to create MultiClusterService")
		}
		return err
	}).WithTimeout(1 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	logs.Printf("Created MultiClusterService %s", client.ObjectKeyFromObject(mcs))
}

func DeleteMultiClusterService(ctx context.Context, cl client.Client, mc *kcmv1.MultiClusterService) {
	mcKey := client.ObjectKeyFromObject(mc)
	Eventually(func() error {
		err := client.IgnoreNotFound(cl.Delete(ctx, mc))
		if err != nil {
			logs.WarnErrorf(err, "failed to delete MultiClusterService")
		}
		return err
	}, 1*time.Minute, 10*time.Second).Should(Succeed())

	Eventually(func() bool {
		err := cl.Get(ctx, mcKey, &kcmv1.MultiClusterService{})
		return apierrors.IsNotFound(err)
	}).WithTimeout(5 * time.Minute).WithPolling(3 * time.Second).Should(BeTrue())
	logs.Printf("Deleted MultiClusterService [%s]", mcKey)
}

func checkClusterReadyConditionInMCS(mcsName string, expectedCount int, conditions []metav1.Condition) (err error) {
	var found bool
	if expectedCount == 0 {
		return nil
	}
	expected := strconv.Itoa(expectedCount) + "/" + strconv.Itoa(expectedCount)

	for _, cond := range conditions {
		if cond.Type == kcmv1.ClusterInReadyStateCondition {
			found = true
			if !strings.Contains(cond.Message, expected) {
				err = fmt.Errorf("expected '%s' in message for condition %s for MCS %s but actual message is '%s'", expected, kcmv1.ClusterInReadyStateCondition, mcsName, cond.Message)
			}
		}
	}
	if !found {
		return fmt.Errorf("condition %s not found in MCS %s", kcmv1.ClusterInReadyStateCondition, mcsName)
	}

	return err
}

// ValidateMultiClusterService wraps the Eventually check for validation.
func ValidateMultiClusterService(ctx context.Context, kc *kubeclient.KubeClient, name string, expectedCount int) {
	Eventually(func() (err error) {
		defer func() {
			if err != nil {
				logs.WarnErrorf(err, "failed to validate MCS %s", name)
			}
		}()

		mcs, err := kc.GetMultiClusterService(ctx, name)
		if err != nil {
			return err
		}

		conditions, err := statusutil.ConditionsFromUnstructured(mcs)
		if err != nil {
			return err
		}

		if err = checkClusterReadyConditionInMCS(name, expectedCount, conditions); err != nil {
			return err
		}

		return validationutil.ValidateConditionsTrue(mcs)
	}).WithTimeout(20 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}
