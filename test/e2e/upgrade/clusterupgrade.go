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

package upgrade

import (
	"context"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func (v *ClusterUpgrade) Run(ctx context.Context) {
	cluster := &kcmv1.ClusterDeployment{}
	err := v.mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: v.namespace,
		Name:      v.name,
	}, cluster)
	Expect(err).NotTo(HaveOccurred())

	patch := crclient.MergeFrom(cluster.DeepCopy())
	cluster.Spec.Template = v.newTemplate
	err = v.mgmtClient.Patch(ctx, cluster, patch)
	Expect(err).NotTo(HaveOccurred())

	v.Validate(ctx)
}
