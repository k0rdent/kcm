// Copyright 2024
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

	. "github.com/onsi/ginkgo/v2"

	"github.com/K0rdent/kcm/test/e2e/kubeclient"
)

type ServiceValidator struct {
	// managedClusterName is the name of managed cluster
	managedClusterName string

	// template being validated
	template Template

	// namespace is a namespace of deployed service
	namespace string

	// resourcesToValidate is a map of resource names and corresponding validation functions
	resourcesToValidate map[string]resourceValidationFunc
}

func NewServiceValidator(clusterName, namespace string) *ServiceValidator {
	return &ServiceValidator{
		managedClusterName:  clusterName,
		namespace:           namespace,
		resourcesToValidate: make(map[string]resourceValidationFunc),
	}
}

func (v *ServiceValidator) WithResourceValidation(resourceName string, validationFunc resourceValidationFunc) *ServiceValidator {
	v.resourcesToValidate[resourceName] = validationFunc
	return v
}

func (v *ServiceValidator) Validate(ctx context.Context, kc *kubeclient.KubeClient) error {
	clusterKubeClient := kc.NewFromCluster(ctx, v.namespace, v.managedClusterName)

	for res, f := range v.resourcesToValidate {
		err := f(ctx, clusterKubeClient, res)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "[%s/%s] validation error: %v\n", v.template, res, err)
			return err
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "[%s/%s] validation succeeded\n", v.template, res)
	}
	return nil
}
