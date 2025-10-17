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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	statusutil "github.com/K0rdent/kcm/internal/util/status"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/e2e/logs"
	validationutil "github.com/K0rdent/kcm/test/util/validation"
)

// BuildMultiClusterService constructs a MultiClusterService spec for the given ClusterDeployment.
func BuildMultiClusterService(cd *kcmv1.ClusterDeployment, multiClusterServiceTemplate, multiClusterServiceMatchLabel, name string) *kcmv1.MultiClusterService {
	return &kcmv1.MultiClusterService{
		TypeMeta: metav1.TypeMeta{},
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

func CreateMultiClusterService(ctx context.Context, cl crclient.Client, mc *kcmv1.MultiClusterService) {
	Eventually(func() error {
		err := crclient.IgnoreAlreadyExists(cl.Create(ctx, mc))
		if err != nil {
			logs.Println("failed to create MultiClusterService: " + err.Error())
		}
		return err
	}, 1*time.Minute, 10*time.Second).Should(Succeed())
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
