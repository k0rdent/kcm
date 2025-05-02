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

package validation

import (
	"context"
	"fmt"

	"github.com/Masterminds/semver/v3"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// ClusterTemplateK8sCompatibility validates the K8s version of the given [github.com/K0rdent/kcm/api/v1beta1.ClusterTemplate]
// satisfies the K8s constraints (if any) of the [github.com/K0rdent/kcm/api/v1beta1.ServiceTemplate] objects
// referenced by the given [github.com/K0rdent/kcm/api/v1beta1.ClusterDeployment].
func ClusterTemplateK8sCompatibility(ctx context.Context, cl client.Client, clusterTemplate *kcmv1.ClusterTemplate, cd *kcmv1.ClusterDeployment) error {
	if len(cd.Spec.ServiceSpec.Services) == 0 || clusterTemplate.Status.KubernetesVersion == "" {
		return nil // nothing to do
	}

	clTplKubeVersion, err := semver.NewVersion(clusterTemplate.Status.KubernetesVersion)
	if err != nil { // should never happen
		return fmt.Errorf("failed to parse k8s version %s of the ClusterTemplate %s: %w", clusterTemplate.Status.KubernetesVersion, client.ObjectKeyFromObject(clusterTemplate), err)
	}

	for _, svc := range cd.Spec.ServiceSpec.Services {
		if svc.Disable {
			continue
		}

		var svcTpl kcmv1.ServiceTemplate
		if err := cl.Get(ctx, client.ObjectKey{Namespace: cd.Namespace, Name: svc.Template}, &svcTpl); err != nil {
			return fmt.Errorf("failed to get ServiceTemplate %s/%s: %w", cd.Namespace, svc.Template, err)
		}

		constraint := svcTpl.Status.KubernetesConstraint
		if constraint == "" {
			continue
		}

		tplConstraint, err := semver.NewConstraint(constraint)
		if err != nil { // should never happen
			return fmt.Errorf("failed to parse k8s constrained version %s of the ServiceTemplate %s: %w", constraint, client.ObjectKeyFromObject(&svcTpl), err)
		}

		if !tplConstraint.Check(clTplKubeVersion) {
			return fmt.Errorf("k8s version %s of the ClusterTemplate %s does not satisfy k8s constraint %s from the ServiceTemplate %s referred in the ClusterDeployment %s",
				clusterTemplate.Status.KubernetesVersion, client.ObjectKeyFromObject(clusterTemplate), constraint,
				client.ObjectKeyFromObject(&svcTpl), client.ObjectKeyFromObject(cd))
		}
	}

	return nil
}
