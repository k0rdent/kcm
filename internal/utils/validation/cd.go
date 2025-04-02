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
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	kcmv1 "github.com/K0rdent/kcm/api/v1alpha1"
)

// ClusterDeployCrossNamespaceServicesRefs validates that the service and templates references of the given [github.com/K0rdent/kcm/api/v1alpha1.ClusterDeployment]
// reference all objects only in the obj's namespace.
func ClusterDeployCrossNamespaceServicesRefs(ctx context.Context, cd *kcmv1.ClusterDeployment) (errs error) {
	logdev := log.FromContext(ctx).V(1)

	logdev.Info("Validating that the template references do not refer to any resource outside the namespace")
	for _, ref := range cd.Spec.ServiceSpec.TemplateResourceRefs {
		// Sveltos will use same namespace as cluster if namespace is empty:
		// https://projectsveltos.github.io/sveltos/template/intro_template/#templateresourcerefs-namespace-and-name
		if ref.Resource.Namespace != "" && ref.Resource.Namespace != cd.Namespace {
			errs = errors.Join(errs, fmt.Errorf(
				"cross-namespace template references are disallowed, %s %s's namespace %s, obj's namespace %s",
				ref.Resource.Kind, ref.Resource.Name, ref.Resource.Namespace, cd.Namespace))
		}
	}

	logdev.Info("Validating that the services values references do not refer to any resource outside the namespace")
	for _, svc := range cd.Spec.ServiceSpec.Services {
		for _, v := range svc.ValuesFrom {
			// Sveltos will use same namespace as cluster if namespace is empty.
			if v.Namespace != "" && v.Namespace != cd.Namespace {
				errs = errors.Join(errs, fmt.Errorf(
					"cross-namespace service values references are disallowed, %s %s's namespace %s, obj's namespace %s",
					v.Kind, v.Name, v.Namespace, cd.Namespace))
			}
		}
	}

	return errs
}
