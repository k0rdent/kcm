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

package multiclusterservice

import (
	"context"
	"time"

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

// BuildNamespacedMultiClusterService constructs a NamespacedMultiClusterService spec for the given
// ClusterDeployment. Unlike BuildMultiClusterService it is namespace-scoped: it lives in, and
// only ever matches ClusterDeployments within, cd.Namespace.
func BuildNamespacedMultiClusterService(cd *kcmv1.ClusterDeployment, namespacedMultiClusterServiceTemplate, serviceNamespace, matchLabel, name string) *kcmv1.NamespacedMultiClusterService {
	return &kcmv1.NamespacedMultiClusterService{
		TypeMeta: metav1.TypeMeta{
			Kind: kcmv1.NamespacedMultiClusterServiceKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cd.Namespace,
		},
		Spec: kcmv1.MultiClusterServiceSpec{
			ClusterSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					matchLabel: cd.Name,
				},
			},
			ServiceSpec: kcmv1.ServiceSpec{
				Provider: kcmv1.StateManagementProviderConfig{},
				Services: []kcmv1.Service{
					{
						Name:      namespacedMultiClusterServiceTemplate,
						Namespace: serviceNamespace,
						Template:  namespacedMultiClusterServiceTemplate,
					},
				},
			},
		},
	}
}

func CreateNamespacedMultiClusterService(ctx context.Context, cl client.Client, nmcs *kcmv1.NamespacedMultiClusterService) {
	Expect(nmcs).NotTo(BeNil())
	Expect(nmcs.Kind).To(Equal(kcmv1.NamespacedMultiClusterServiceKind))

	Eventually(func() error {
		err := client.IgnoreAlreadyExists(cl.Create(ctx, nmcs))
		if err != nil {
			logs.WarnErrorf(err, "failed to create NamespacedMultiClusterService")
		}
		return err
	}).WithTimeout(1 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	logs.Printf("Created NamespacedMultiClusterService %s", client.ObjectKeyFromObject(nmcs))
}

func CreateNamespacedMultiClusterServiceWithDelete(
	ctx context.Context,
	cl client.Client,
	nmcs *kcmv1.NamespacedMultiClusterService,
) func() error {
	CreateNamespacedMultiClusterService(ctx, cl, nmcs)
	nmcsKey := client.ObjectKeyFromObject(nmcs)
	return func() error {
		logs.Printf("Deleting NamespacedMultiClusterService [%s]", nmcsKey)

		if err := cl.Delete(ctx, nmcs); client.IgnoreNotFound(err) != nil {
			return err
		}

		Eventually(func() bool {
			err := cl.Get(ctx, nmcsKey, &kcmv1.NamespacedMultiClusterService{})
			return apierrors.IsNotFound(err)
		}).WithTimeout(5 * time.Minute).WithPolling(3 * time.Second).Should(BeTrue())

		logs.Printf("Deleted NamespacedMultiClusterService [%s]", nmcsKey)

		return nil
	}
}

func DeleteNamespacedMultiClusterService(ctx context.Context, cl client.Client, nmcs *kcmv1.NamespacedMultiClusterService) {
	nmcsKey := client.ObjectKeyFromObject(nmcs)
	Eventually(func() error {
		err := client.IgnoreNotFound(cl.Delete(ctx, nmcs))
		if err != nil {
			logs.WarnErrorf(err, "failed to delete NamespacedMultiClusterService")
		}
		return err
	}, 1*time.Minute, 10*time.Second).Should(Succeed())

	Eventually(func() bool {
		err := cl.Get(ctx, nmcsKey, &kcmv1.NamespacedMultiClusterService{})
		return apierrors.IsNotFound(err)
	}).WithTimeout(5 * time.Minute).WithPolling(3 * time.Second).Should(BeTrue())
	logs.Printf("Deleted NamespacedMultiClusterService [%s]", nmcsKey)
}

// ValidateNamespacedMultiClusterService wraps the Eventually check for validation, mirroring
// ValidateMultiClusterService for the namespace-scoped type. Unlike MultiClusterService,
// NamespacedMultiClusterService is not necessarily in kc.Namespace, so namespace must be given
// explicitly.
func ValidateNamespacedMultiClusterService(ctx context.Context, kc *kubeclient.KubeClient, namespace, name string, expectedCount int) {
	Eventually(func() (err error) {
		defer func() {
			if err != nil {
				logs.WarnErrorf(err, "failed to validate NamespacedMultiClusterService %s/%s", namespace, name)
			}
		}()

		nmcs, err := kc.GetNamespacedMultiClusterService(ctx, namespace, name)
		if err != nil {
			return err
		}

		conditions, err := statusutil.ConditionsFromUnstructured(nmcs)
		if err != nil {
			return err
		}

		// checkClusterReadyConditionInMCS is generic over the object's name and conditions - it
		// doesn't care whether they came from a MultiClusterService or a NamespacedMultiClusterService.
		if err = checkClusterReadyConditionInMCS(name, expectedCount, conditions); err != nil {
			return err
		}

		return validationutil.ValidateConditionsTrue(nmcs)
	}).WithTimeout(20 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

func GetNamespacedMultiClusterService(ctx context.Context, cl client.Client, key client.ObjectKey) (*kcmv1.NamespacedMultiClusterService, error) {
	nmcs := &kcmv1.NamespacedMultiClusterService{}
	if err := cl.Get(ctx, key, nmcs); err != nil {
		return nil, err
	}
	return nmcs, nil
}

// ValidateNamespacedServiceSet validates the ServiceSet associated with the provided CD and NamespacedMultiClusterService.
func ValidateNamespacedServiceSet(ctx context.Context, cl client.Client, systemNamespace string, cd *kcmv1.ClusterDeployment, nmcs *kcmv1.NamespacedMultiClusterService) {
	serviceSetKey := serviceset.ObjectKey(systemNamespace, cd, nmcs)
	services := make([]client.ObjectKey, len(nmcs.Spec.ServiceSpec.Services))
	for i, svc := range nmcs.Spec.ServiceSpec.Services {
		services[i] = client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}
	}
	servicesete2e.ValidateServiceSet(ctx, cl, serviceSetKey, services)
}
