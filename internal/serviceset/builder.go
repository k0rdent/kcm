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

package serviceset

import (
	"errors"
	"fmt"
	"maps"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// Builder is a builder for ServiceSet objects.
// It defines all necessary parameters and dependencies to
// either create or update a ServiceSet object.
type Builder struct {
	// ServiceSet is the base ServiceSet which will be mutated as needed
	ServiceSet *kcmv1.ServiceSet

	// ClusterDeployment is the related ClusterDeployment
	ClusterDeployment *kcmv1.ClusterDeployment

	// MultiClusterServiceCommon is the related MultiClusterService or NamespacedMultiClusterService if any
	MultiClusterServiceCommon kcmv1.MultiClusterServiceCommon

	// Selector is the selector used to extract labels for the ServiceSet
	Selector *metav1.LabelSelector

	// ServicesToDeploy is the list of services to deploy
	ServicesToDeploy []kcmv1.ServiceWithValues
}

// NewBuilder returns a new Builder with mandatory parameters set.
func NewBuilder(clusterDeployment *kcmv1.ClusterDeployment, serviceSet *kcmv1.ServiceSet, selector *metav1.LabelSelector) *Builder {
	return &Builder{
		ClusterDeployment: clusterDeployment,
		ServiceSet:        serviceSet,
		Selector:          selector,
	}
}

// WithMultiClusterServiceCommon sets the related MultiClusterService.
func (b *Builder) WithMultiClusterServiceCommon(multiClusterServiceCommon kcmv1.MultiClusterServiceCommon) *Builder {
	b.MultiClusterServiceCommon = multiClusterServiceCommon
	return b
}

// WithServicesToDeploy sets the list of services to deploy.
func (b *Builder) WithServicesToDeploy(servicesToDeploy []kcmv1.ServiceWithValues) *Builder {
	b.ServicesToDeploy = servicesToDeploy
	return b
}

// Build constructs and returns a ServiceSet object based on the builder's parameters or returns an error if invalid.
func (b *Builder) Build() (sset *kcmv1.ServiceSet, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("error building ServiceSet %s: %w", client.ObjectKeyFromObject(b.ServiceSet).String(), err)
		}
	}()

	var ownerReference *metav1.OwnerReference
	var providerConfig kcmv1.StateManagementProviderConfig
	_, isNamespacedMCS := b.MultiClusterServiceCommon.(*kcmv1.NamespacedMultiClusterService)

	// If both ClusterDeployment and MultiClusterServiceCommon are not nil, then the ownership
	// is claimed by the ClusterDeployment, which is why it is the first case in this switch statement.
	//
	// TODO: Should the ClusterDeployment be the owner if the MCS matching the CD is what is
	// responsible for the creation of this ServiceSet?
	switch {
	case b.ClusterDeployment != nil:
		ownerReference = metav1.NewControllerRef(b.ClusterDeployment, kcmv1.GroupVersion.WithKind(kcmv1.ClusterDeploymentKind))
		providerConfig, err = StateManagementProviderConfigFromServiceSpec(b.ClusterDeployment.Spec.ServiceSpec)
	case !kcmv1.IsMCSNil(b.MultiClusterServiceCommon):
		if isNamespacedMCS {
			ownerReference = metav1.NewControllerRef(b.MultiClusterServiceCommon, kcmv1.GroupVersion.WithKind(kcmv1.NamespacedMultiClusterServiceKind))
		} else {
			ownerReference = metav1.NewControllerRef(b.MultiClusterServiceCommon, kcmv1.GroupVersion.WithKind(kcmv1.MultiClusterServiceKind))
		}
		providerConfig, err = StateManagementProviderConfigFromServiceSpec(b.MultiClusterServiceCommon.GetMultiClusterServiceSpec().ServiceSpec)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to convert ServiceSpec to ProviderConfig: %w", err)
	}

	b.ServiceSet.OwnerReferences = []metav1.OwnerReference{*ownerReference}
	b.ServiceSet.Spec = kcmv1.ServiceSetSpec{Services: b.ServicesToDeploy}
	b.ServiceSet.Spec.Provider = providerConfig

	// The `.spec.clusterDeployment` for a ServiceSet can be set alongside either `.spec.multiClusterService`
	// or `.spec.namespacedMultiClusterService`. This happens when a MultiClusterService or
	// NamespacedMultiClusterService matches a ClusterDeployment via selector labels. Therefore not using else.
	// The `.spec.multiClusterService` and `.spec.namespacedMultiClusterService` fields are mutually exclusive though.
	if b.ClusterDeployment != nil {
		b.ServiceSet.Spec.Cluster = b.ClusterDeployment.Name
	}
	if !kcmv1.IsMCSNil(b.MultiClusterServiceCommon) {
		if isNamespacedMCS {
			b.ServiceSet.Spec.NamespacedMultiClusterService = b.MultiClusterServiceCommon.GetFullname()
		} else {
			b.ServiceSet.Spec.MultiClusterService = b.MultiClusterServiceCommon.GetFullname()
		}
	}

	// We ignore selfManagement for NamespacedMultiClusterService.
	b.ServiceSet.Spec.Provider.SelfManagement = !isNamespacedMCS && b.ClusterDeployment == nil

	labels, err := extractRequiredLabels(b.Selector)
	if err != nil {
		return nil, fmt.Errorf("failed to extract required labels from StateManagementProvider selector: %w", err)
	}
	if b.ServiceSet.Labels == nil {
		b.ServiceSet.Labels = labels
	} else {
		maps.Copy(b.ServiceSet.Labels, labels)
	}

	return b.ServiceSet, nil
}

// extractRequiredLabels extracts the required labels from a selector.
func extractRequiredLabels(selector *metav1.LabelSelector) (map[string]string, error) {
	if selector == nil {
		return nil, errors.New("selector cannot be nil")
	}

	result := make(map[string]string)
	maps.Copy(result, selector.MatchLabels)

	sel, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, err
	}

	requirements, _ := sel.Requirements()
	for _, req := range requirements {
		switch req.Operator() {
		case selection.Equals, selection.DoubleEquals:
			values := req.Values()
			if values.Len() == 1 {
				result[req.Key()] = values.List()[0]
			}
		case selection.In:
			// for 'In' with single value, we can extract it, for multiple values
			// we'll set the first one
			values := req.Values()
			if values.Len() > 0 {
				result[req.Key()] = values.List()[0]
			}
		case selection.Exists:
			// for 'Exists', we'll add an empty value
			if _, exists := result[req.Key()]; !exists {
				result[req.Key()] = ""
			}
		case selection.NotIn, selection.DoesNotExist, selection.NotEquals:
			// we can't represent negative requirements as positive labels
			// so we'll just ignore them.
		case selection.GreaterThan, selection.LessThan:
			// we can't represent range requirements as positive labels
			// so we'll just ignore them.
		}
	}

	return result, nil
}
