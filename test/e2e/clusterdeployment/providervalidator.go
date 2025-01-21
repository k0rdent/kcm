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

// ProviderValidator is a struct that contains the necessary information to
// validate a provider's resources.  Some providers do not support all of the
// resources that can potentially be validated.
type ProviderValidator struct {
	// Template is the name of the template being validated.
	template Template
	// ClusterName is the name of the cluster to validate.
	clusterName string
	// ResourcesToValidate is a map of resource names to their validation
	// function.
	resourcesToValidate map[string]resourceValidationFunc
	// ResourceOrder is a slice of resource names that determines the order in
	// which resources are validated.
	resourceOrder []string
}

type ValidationAction string

const (
	ValidationActionDeploy ValidationAction = "deploy"
	ValidationActionDelete ValidationAction = "delete"
)

func NewProviderValidator(template Template, clusterName string, action ValidationAction) *ProviderValidator {
	var (
		resourcesToValidate map[string]resourceValidationFunc
		resourceOrder       []string
	)

	if action == ValidationActionDeploy {
		resourcesToValidate = map[string]resourceValidationFunc{
			"clusters":       validateCluster,
			"machines":       validateMachines,
			"control-planes": validateK0sControlPlanes,
			"csi-driver":     validateCSIDriver,
		}
		resourceOrder = []string{"clusters", "machines", "control-planes", "csi-driver"}

		switch template {
		case TemplateAWSStandaloneCP, TemplateAWSHostedCP:
			resourcesToValidate["ccm"] = validateCCM
			resourceOrder = append(resourceOrder, "ccm")
		case TemplateAWSEKS:
			resourcesToValidate = map[string]resourceValidationFunc{
				"clusters":                   validateCluster,
				"machines":                   validateMachines,
				"aws-managed-control-planes": validateAWSManagedControlPlanes,
				"csi-driver":                 validateCSIDriver,
				"ccm":                        validateCCM,
			}
			resourceOrder = []string{"clusters", "machines", "aws-managed-control-planes", "csi-driver", "ccm"}
		case TemplateAzureStandaloneCP, TemplateAzureHostedCP, TemplateVSphereStandaloneCP:
			delete(resourcesToValidate, "csi-driver")
		case TemplateAdoptedCluster:
			resourcesToValidate = map[string]resourceValidationFunc{
				"sveltoscluster": validateSveltosCluster,
			}
		}
	} else {
		resourcesToValidate = map[string]resourceValidationFunc{
			"clusters":           validateClusterDeleted,
			"machinedeployments": validateMachineDeploymentsDeleted,
		}

		resourceOrder = []string{"clusters", "machinedeployments"}
		switch template {
		case TemplateAWSEKS:
			resourcesToValidate["aws-managed-control-planes"] = validateAWSManagedControlPlanesDeleted
			resourceOrder = append(resourceOrder, "aws-managed-control-planes")
		default:
			resourcesToValidate["control-planes"] = validateK0sControlPlanesDeleted
			resourceOrder = append(resourceOrder, "control-planes")
		}
	}

	return &ProviderValidator{
		template:            template,
		clusterName:         clusterName,
		resourcesToValidate: resourcesToValidate,
		resourceOrder:       resourceOrder,
	}
}

// Validate is a provider-agnostic verification that checks for
// a specific set of resources and either validates their readiness or
// their deletion depending on the passed map of resourceValidationFuncs and
// desired order.
// It is meant to be used in conjunction with an Eventually block.
// In some cases it may be necessary to end the Eventually block early if the
// resource will never reach a ready state, in these instances Ginkgo's Fail
// should be used to end the spec early.
func (p *ProviderValidator) Validate(ctx context.Context, kc *kubeclient.KubeClient) error {
	// Sequentially validate each resource type, only returning the first error
	// as to not move on to the next resource type until the first is resolved.
	// We use []string here since order is important.
	for _, name := range p.resourceOrder {
		validator, ok := p.resourcesToValidate[name]
		if !ok {
			continue
		}

		if err := validator(ctx, kc, p.clusterName); err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "[%s/%s] validation error: %v\n", p.template, name, err)
			return err
		}

		_, _ = fmt.Fprintf(GinkgoWriter, "[%s/%s] validation succeeded\n", p.template, name)
		delete(p.resourcesToValidate, name)
	}

	return nil
}
