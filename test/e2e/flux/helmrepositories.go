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

package flux

import (
	"context"
	"fmt"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func CreateHelmRepository(ctx context.Context, client crclient.Client, namespace, name string, spec sourcev1.HelmRepositorySpec) {
	hr := &sourcev1.HelmRepository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"k0rdent.mirantis.com/managed": "true",
			},
		},
		Spec: spec,
	}
	err := client.Create(ctx, hr)
	Expect(crclient.IgnoreAlreadyExists(err)).NotTo(HaveOccurred(), "failed to create HelmRepository")
	_, _ = fmt.Fprintf(GinkgoWriter, "Created HelmRepository %s\n", crclient.ObjectKeyFromObject(hr))
}

func CreateHelmRepositoryWithDelete(ctx context.Context, client crclient.Client, namespace, name string, spec sourcev1.HelmRepositorySpec) func() error {
	CreateHelmRepository(ctx, client, namespace, name, spec)
	hr := &sourcev1.HelmRepository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
			},
		},
		Spec: spec,
	}
	hrKey := crclient.ObjectKeyFromObject(hr)

	return func() error {
		if err := client.Delete(ctx, hr); crclient.IgnoreNotFound(err) != nil {
			return err
		}
		Eventually(func() bool {
			err := client.Get(ctx, hrKey, &sourcev1.HelmRepository{})
			return apierrors.IsNotFound(err)
		}).WithTimeout(5 * time.Minute).WithPolling(3 * time.Second).Should(BeTrue())
		_, _ = fmt.Fprintf(GinkgoWriter, "Deleted HelmRepository %s\n", hrKey.String())
		return nil
	}
}
