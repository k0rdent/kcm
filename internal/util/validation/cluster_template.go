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
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// ClusterTemplateProviders validates that all required providers are exposed and
// compatible by the parent object (either [github.com/K0rdent/kcm/api/v1beta1.Management] or
// [github.com/K0rdent/kcm/api/v1beta1.Region]).
func ClusterTemplateProviders(ctx context.Context, mgmtClient client.Client, clusterTemplate *kcmv1.ClusterTemplate, cd *kcmv1.ClusterDeployment) error {
	cred := &kcmv1.Credential{}
	credKey := client.ObjectKey{Namespace: cd.Namespace, Name: cd.Spec.Credential}
	if err := mgmtClient.Get(ctx, credKey, cred); err != nil {
		return fmt.Errorf("failed to get %s Credential: %w", credKey, err)
	}
	parent, err := getParent(ctx, mgmtClient, cred)
	if err != nil {
		return fmt.Errorf("failed to get parent object for %s ClusterDeployment: %w", client.ObjectKeyFromObject(cd), err)
	}
	if err := validateCompatibilityAttrs(ctx, clusterTemplate, parent); err != nil {
		gvk, _ := apiutil.GVKForObject(parent, mgmtClient.Scheme())
		return fmt.Errorf("incompatible providers in %s %s: %w", gvk.Kind, parent.GetName(), err)
	}
	return nil
}

func validateCompatibilityAttrs(ctx context.Context, clusterTemplate *kcmv1.ClusterTemplate, parent ClusterParent) error {
	exposedProviders, requiredProviders := parent.GetComponentsStatus().AvailableProviders, clusterTemplate.Status.Providers

	l := ctrl.LoggerFrom(ctx)

	var (
		merr          error
		missing       []string
		nonSatisfying []string
	)
	for _, v := range requiredProviders {
		if !slices.Contains(exposedProviders, v) {
			missing = append(missing, v)
			continue
		}
	}

	// already validated contract versions format
	for providerName, requiredContract := range clusterTemplate.Status.ProviderContracts {
		l.V(1).Info("validating contracts", "exposed_provider_capi_contracts", parent.GetComponentsStatus().CAPIContracts, "required_provider_name", providerName)

		providerCAPIContracts, ok := parent.GetComponentsStatus().CAPIContracts[providerName] // capi_version: provider_version(s)
		if !ok {
			continue // both the provider and cluster templates contract versions must be set for the validation
		}

		var exposedProviderContracts []string
		for _, supportedVersions := range providerCAPIContracts {
			exposedProviderContracts = append(exposedProviderContracts, strings.Split(supportedVersions, "_")...)
		}

		if !slices.Contains(exposedProviderContracts, requiredContract) {
			nonSatisfying = append(nonSatisfying, "provider "+providerName+" does not support "+requiredContract)
		}
	}

	if len(missing) > 0 {
		slices.Sort(missing)
		merr = errors.Join(merr, fmt.Errorf("one or more required providers are not deployed yet: %v", missing))
	}

	if len(nonSatisfying) > 0 {
		slices.Sort(nonSatisfying)
		merr = errors.Join(merr, fmt.Errorf("one or more required provider contract versions does not satisfy deployed: %v", nonSatisfying))
	}

	return merr
}

func ClusterTemplateK8sCompatibility(ctx context.Context, cl client.Client, clusterTemplate *kcmv1.ClusterTemplate, cd client.ObjectKey, serviceSpec *kcmv1.ServiceSpec) error {
	if len(serviceSpec.Services) == 0 || clusterTemplate.Status.KubernetesVersion == "" {
		return nil // nothing to do
	}

	clTplKubeVersion, err := semver.NewVersion(clusterTemplate.Status.KubernetesVersion)
	if err != nil { // should never happen
		return fmt.Errorf("failed to parse k8s version %s of the ClusterTemplate %s: %w", clusterTemplate.Status.KubernetesVersion, client.ObjectKeyFromObject(clusterTemplate), err)
	}

	for _, svc := range serviceSpec.Services {
		if svc.Disable {
			continue
		}

		var svcTpl kcmv1.ServiceTemplate
		if err := cl.Get(ctx, client.ObjectKey{Namespace: cd.Namespace, Name: svc.Template}, &svcTpl); err != nil {
			return fmt.Errorf("failed to get ServiceTemplate %s/%s: %w", cd.Namespace, svc.Template, err)
		}

		// wahab
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
				client.ObjectKeyFromObject(&svcTpl), cd)
		}
	}

	return nil
}

// // ClusterTemplateK8sCompatibility validates the K8s version of the given [github.com/K0rdent/kcm/api/v1beta1.ClusterTemplate]
// // satisfies the K8s constraints (if any) of the [github.com/K0rdent/kcm/api/v1beta1.ServiceTemplate] objects
// // referenced by the given [github.com/K0rdent/kcm/api/v1beta1.ClusterDeployment].
// func ClusterTemplateK8sCompatibility(ctx context.Context, cl client.Client, clusterTemplate *kcmv1.ClusterTemplate, cd *kcmv1.ClusterDeployment) error {
// 	if len(cd.Spec.ServiceSpec.Services) == 0 || clusterTemplate.Status.KubernetesVersion == "" {
// 		return nil // nothing to do
// 	}

// 	clTplKubeVersion, err := semver.NewVersion(clusterTemplate.Status.KubernetesVersion)
// 	if err != nil { // should never happen
// 		return fmt.Errorf("failed to parse k8s version %s of the ClusterTemplate %s: %w", clusterTemplate.Status.KubernetesVersion, client.ObjectKeyFromObject(clusterTemplate), err)
// 	}

// 	for _, svc := range cd.Spec.ServiceSpec.Services {
// 		if svc.Disable {
// 			continue
// 		}

// 		var svcTpl kcmv1.ServiceTemplate
// 		if err := cl.Get(ctx, client.ObjectKey{Namespace: cd.Namespace, Name: svc.Template}, &svcTpl); err != nil {
// 			return fmt.Errorf("failed to get ServiceTemplate %s/%s: %w", cd.Namespace, svc.Template, err)
// 		}

// 		// wahab
// 		constraint := svcTpl.Status.KubernetesConstraint
// 		if constraint == "" {
// 			continue
// 		}

// 		tplConstraint, err := semver.NewConstraint(constraint)
// 		if err != nil { // should never happen
// 			return fmt.Errorf("failed to parse k8s constrained version %s of the ServiceTemplate %s: %w", constraint, client.ObjectKeyFromObject(&svcTpl), err)
// 		}

// 		if !tplConstraint.Check(clTplKubeVersion) {
// 			return fmt.Errorf("k8s version %s of the ClusterTemplate %s does not satisfy k8s constraint %s from the ServiceTemplate %s referred in the ClusterDeployment %s",
// 				clusterTemplate.Status.KubernetesVersion, client.ObjectKeyFromObject(clusterTemplate), constraint,
// 				client.ObjectKeyFromObject(&svcTpl), client.ObjectKeyFromObject(cd))
// 		}
// 	}

// 	return nil
// }
