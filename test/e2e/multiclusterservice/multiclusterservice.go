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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/serviceset"
	statusutil "github.com/K0rdent/kcm/internal/util/status"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	servicesete2e "github.com/K0rdent/kcm/test/e2e/serviceset"
	validationutil "github.com/K0rdent/kcm/test/util/validation"
)

// BuildMultiClusterService constructs a MultiClusterService spec for the given ClusterDeployment.
func BuildMultiClusterService(cd *kcmv1.ClusterDeployment, multiClusterServiceTemplate, multiClusterServiceMatchLabel, name string) *kcmv1.MultiClusterService {
	return &kcmv1.MultiClusterService{
		TypeMeta: metav1.TypeMeta{
			Kind: kcmv1.MultiClusterServiceKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cd.Namespace,
		},
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
						Namespace: cd.Namespace,
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
			logs.Println("failed to create MultiClusterService: " + err.Error())
		}
		return err
	}).WithTimeout(1 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	_, _ = fmt.Fprintf(GinkgoWriter, "Created MultiClusterService %s\n", client.ObjectKeyFromObject(mcs))
}

func CreateMultiClusterServiceWithDelete(
	ctx context.Context,
	cl client.Client,
	mcs *kcmv1.MultiClusterService,
) func() error {
	CreateMultiClusterService(ctx, cl, mcs)
	mcsKey := client.ObjectKeyFromObject(mcs)
	return func() error {
		fmt.Fprintf(GinkgoWriter, "Deleting MultiClusterService [%s]\n", mcsKey)

		if err := cl.Delete(ctx, mcs); client.IgnoreNotFound(err) != nil {
			return err
		}

		Eventually(func() bool {
			err := cl.Get(ctx, mcsKey, &kcmv1.MultiClusterService{})
			return apierrors.IsNotFound(err)
		}).WithTimeout(5 * time.Minute).WithPolling(3 * time.Second).Should(BeTrue())

		fmt.Fprintf(GinkgoWriter, "Deleted MultiClusterService [%s]\n", mcsKey)

		return nil
	}
}

func DeleteMultiClusterService(ctx context.Context, cl client.Client, mc *kcmv1.MultiClusterService) {
	Eventually(func() error {
		err := client.IgnoreNotFound(cl.Delete(ctx, mc))
		if err != nil {
			logs.Println("failed to delete MultiClusterService: " + err.Error())
		}
		return err
	}, 1*time.Minute, 10*time.Second).Should(Succeed())
}

func checkClusterReadyConditionInMCS(mcsName string, expectedCount int, conditions []metav1.Condition) (err error) {
	var found bool
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
				err = fmt.Errorf("failed validation for MCS %s: %v", name, err)
				_, _ = fmt.Fprintf(GinkgoWriter, "[%s] %s\n", name, err)
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
	}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

func GetMultiClusterService(ctx context.Context, cl client.Client, key client.ObjectKey) (*kcmv1.MultiClusterService, error) {
	mcs := &kcmv1.MultiClusterService{}
	if err := cl.Get(ctx, key, mcs); err != nil {
		return nil, err
	}
	return mcs, nil
}

// ValidateMCSConditions validates that the provided list of expected conditions
// eventually exist in the status of the MCS object represented by the provided key.
func ValidateMCSConditions(ctx context.Context, cl client.Client, mcsKey client.ObjectKey, expectedConditions []metav1.Condition) {
	Eventually(func() (err error) {
		defer func() {
			if err != nil {
				fmt.Fprintf(GinkgoWriter, "[%s] failed validation of conditions: %v\n", mcsKey, err)
			}
		}()

		mcs, err := GetMultiClusterService(ctx, cl, mcsKey)
		if err != nil {
			return err
		}

		conditionsMap := make(map[string]metav1.Condition, len(expectedConditions))
		for _, cond := range mcs.Status.Conditions {
			conditionsMap[cond.Type] = cond
		}

		for _, expectedCond := range expectedConditions {
			actualCond, ok := conditionsMap[expectedCond.Type]
			if !ok {
				return fmt.Errorf("expected condition %s to exist but did not exist in actual", expectedCond.Type)
			}

			if expectedCond.Status != "" && actualCond.Status != expectedCond.Status {
				return fmt.Errorf("condition %s failed: actual status %q != expected status %q", actualCond.Type, actualCond.Status, expectedCond.Status)
			}
			if expectedCond.Message != "" && actualCond.Message != expectedCond.Message {
				return fmt.Errorf("condition %s failed: actual message %q != expected message %q", actualCond.Type, actualCond.Message, expectedCond.Message)
			}
		}

		return nil
	}).WithTimeout(5 * time.Minute).WithPolling(3 * time.Second).Should(Succeed())

	fmt.Fprintf(GinkgoWriter, "[%s] MultiClusterService successfully passed\n", mcsKey)
}

// ValidateServiceSet validates the ServiceSet associated with the provided CD and MCS.
func ValidateServiceSet(ctx context.Context, cl client.Client, systemNamespace string, cd *kcmv1.ClusterDeployment, mcs *kcmv1.MultiClusterService) {
	serviceSetKey := serviceset.ObjectKey(systemNamespace, cd, mcs)
	services := make([]client.ObjectKey, len(mcs.Spec.ServiceSpec.Services))
	for i, svc := range mcs.Spec.ServiceSpec.Services {
		services[i] = client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}
	}
	servicesete2e.ValidateServiceSet(ctx, cl, serviceSetKey, services)
}
