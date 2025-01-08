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

type ManagedServiceResource struct {
	// ResourceNameSuffix is the suffix added to the resource name
	ResourceNameSuffix string

	// ValidationFunc is the validation function for the resource
	ValidationFunc resourceValidationFunc
}

func (m ManagedServiceResource) resourceFullName(serviceName string) string {
	if m.ResourceNameSuffix == "" {
		return serviceName
	}
	return fmt.Sprintf("%s-%s", serviceName, m.ResourceNameSuffix)
}

type ServiceValidator struct {
	// managedClusterName is the name of managed cluster
	managedClusterName string

	// managedServiceName is the name of managed service
	managedServiceName string

	// template being validated
	template Template

	// namespace is a namespace of deployed service
	namespace string

	// resourcesToValidate is a map of resource names and corresponding resources definitions
	resourcesToValidate map[string]ManagedServiceResource
}

func NewServiceValidator(clusterName, serviceName, namespace string) *ServiceValidator {
	return &ServiceValidator{
		managedClusterName:  clusterName,
		managedServiceName:  serviceName,
		namespace:           namespace,
		resourcesToValidate: make(map[string]ManagedServiceResource),
	}
}

func (v *ServiceValidator) WithResourceValidation(resourceName string, resource ManagedServiceResource) *ServiceValidator {
	v.resourcesToValidate[resourceName] = resource
	return v
}

func (v *ServiceValidator) Validate(ctx context.Context, kc *kubeclient.KubeClient) error {
	clusterKubeClient := kc.NewFromCluster(ctx, v.namespace, v.managedClusterName)

	for resourceName, resource := range v.resourcesToValidate {
		resourceFullName := resource.resourceFullName(v.managedServiceName)
		err := resource.ValidationFunc(ctx, clusterKubeClient, resourceFullName)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "[%s/%s] validation error: %v\n", v.template, resourceName, err)
			return err
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "[%s/%s] validation succeeded\n", v.template, resourceName)
	}
	return nil
}
