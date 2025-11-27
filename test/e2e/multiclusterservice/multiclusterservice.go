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
		if err := cl.Delete(ctx, mcs); client.IgnoreNotFound(err) != nil {
			return err
		}
		Eventually(func() bool {
			err := cl.Get(ctx, mcsKey, &kcmv1.MultiClusterService{})
			return apierrors.IsNotFound(err)
		}).WithTimeout(30 * time.Minute).WithPolling(5 * time.Minute).Should(BeTrue())
		_, _ = fmt.Fprintf(GinkgoWriter, "Deleted MultiClusterService %s\n", mcsKey)
		return nil
	}
}

func DeleteMultiClusterService(ctx context.Context, cl client.Client, mcs *kcmv1.MultiClusterService) error {
	if err := cl.Delete(ctx, mcs); client.IgnoreNotFound(err) != nil {
		return err
	}
	return nil
}

func checkMultiClusterServiceConditions(ctx context.Context, kc *kubeclient.KubeClient, multiclusterServiceName string, expectedCount int) error {
	multiclusterService, err := kc.GetMultiClusterService(ctx, multiclusterServiceName)
	if err != nil {
		return err
	}

	conditions, err := statusutil.ConditionsFromUnstructured(multiclusterService)
	if err != nil {
		return err
	}
	objKind, objName := statusutil.ObjKindName(multiclusterService)
	for _, c := range conditions {
		if c.Type == kcmv1.ClusterInReadyStateCondition {
			if !strings.Contains(c.Message, fmt.Sprintf("%d/%d", expectedCount, expectedCount)) {
				return fmt.Errorf("%s %s is not ready with conditions:\n%s", objKind, objName, validationutil.ConvertConditionsToString(c))
			}
		}
	}
	return validationutil.ValidateConditionsTrue(multiclusterService)
}

// ValidateMultiClusterService wraps the Eventually check for validation.
func ValidateMultiClusterService(kc *kubeclient.KubeClient, name string, expectedCount int) {
	Eventually(func() error {
		err := checkMultiClusterServiceConditions(context.Background(), kc, name, expectedCount)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "[%s] validation error: %v\n", name, err)
		}
		return err
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
				err = fmt.Errorf("[%s] failed validation of conditions: %v", mcsKey.String(), err)
				_, _ = fmt.Fprintf(GinkgoWriter, "%v\n", err.Error())
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
	}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
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
