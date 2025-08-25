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
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/selection"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/utils"
)

// Builder is a builder for ServiceSet objects.
// It defines all necessary parameters and dependencies to
// either create or update a ServiceSet object.
type Builder struct {
	// ServiceSet is the base ServiceSet which will be mutated as needed
	ServiceSet *kcmv1.ServiceSet

	// ClusterDeployment is the related ClusterDeployment
	ClusterDeployment *kcmv1.ClusterDeployment

	// MultiClusterService is the related MultiClusterService if any
	MultiClusterService *kcmv1.MultiClusterService

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

// WithMultiClusterService sets the related MultiClusterService.
func (b *Builder) WithMultiClusterService(multiClusterService *kcmv1.MultiClusterService) *Builder {
	b.MultiClusterService = multiClusterService
	return b
}

// WithServicesToDeploy sets the list of services to deploy.
func (b *Builder) WithServicesToDeploy(servicesToDeploy []kcmv1.ServiceWithValues) *Builder {
	b.ServicesToDeploy = servicesToDeploy
	return b
}

// Build constructs and returns a ServiceSet object based on the builder's parameters or returns an error if invalid.
func (b *Builder) Build() (*kcmv1.ServiceSet, error) {
	var ownerReference *metav1.OwnerReference
	if b.ClusterDeployment != nil {
		ownerReference = metav1.NewControllerRef(b.ClusterDeployment, kcmv1.GroupVersion.WithKind(kcmv1.ClusterDeploymentKind))
	} else {
		ownerReference = metav1.NewControllerRef(b.MultiClusterService, kcmv1.GroupVersion.WithKind(kcmv1.MultiClusterServiceKind))
	}

	b.ServiceSet.OwnerReferences = []metav1.OwnerReference{*ownerReference}

	labels, err := extractRequiredLabels(b.Selector)
	if err != nil {
		return nil, fmt.Errorf("failed to extract required labels from StateManagementProvider selector: %w", err)
	}
	if b.ServiceSet.Labels == nil {
		b.ServiceSet.Labels = labels
	} else {
		maps.Copy(b.ServiceSet.Labels, labels)
	}

	var providerConfig kcmv1.StateManagementProviderConfig
	b.ServiceSet.Spec = kcmv1.ServiceSetSpec{Services: b.ServicesToDeploy}
	if b.ClusterDeployment != nil {
		b.ServiceSet.Spec.Cluster = b.ClusterDeployment.Name
		if b.ClusterDeployment.Spec.ServiceSpec.Provider.Name == "" {
			providerConfig, err = ConvertServiceSpecToProviderConfig(b.ClusterDeployment.Spec.ServiceSpec)
		} else {
			providerConfig = b.ClusterDeployment.Spec.ServiceSpec.Provider
		}
	}
	if b.MultiClusterService != nil {
		if b.MultiClusterService.Spec.ServiceSpec.Provider.Name == "" {
			providerConfig, err = ConvertServiceSpecToProviderConfig(b.MultiClusterService.Spec.ServiceSpec)
		} else {
			providerConfig = b.MultiClusterService.Spec.ServiceSpec.Provider
		}
		b.ServiceSet.Spec.MultiClusterService = b.MultiClusterService.Name
	}
	if err != nil {
		return nil, fmt.Errorf("failed to convert ServiceSpec to ProviderConfig: %w", err)
	}
	b.ServiceSet.Spec.Provider = providerConfig
	b.ServiceSet.Spec.Provider.SelfManagement = b.ClusterDeployment == nil
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

// ConvertServiceSpecToProviderConfig moves sveltos-specific configuration
// from .spec.serviceSpec to .spec.serviceSpec.provider.config
func ConvertServiceSpecToProviderConfig(serviceSpec kcmv1.ServiceSpec) (kcmv1.StateManagementProviderConfig, error) {
	type config struct {
		SyncMode             string                                       `json:"syncMode,omitempty"`
		TemplateResourceRefs []addoncontrollerv1beta1.TemplateResourceRef `json:"templateResourceRefs,omitempty"`
		PolicyRefs           []addoncontrollerv1beta1.PolicyRef           `json:"policyRefs,omitempty"`
		DriftIgnore          []libsveltosv1beta1.PatchSelector            `json:"driftIgnore,omitempty"`
		DriftExclusions      []addoncontrollerv1beta1.DriftExclusion      `json:"driftExclusions,omitempty"`
		Priority             int32                                        `json:"priority,omitempty"`
		StopOnConflict       bool                                         `json:"stopOnConflict,omitempty"`
		Reload               bool                                         `json:"reloader,omitempty"`
		ContinueOnError      bool                                         `json:"continueOnError,omitempty"`
	}

	cfg := config{
		SyncMode:             serviceSpec.SyncMode,
		TemplateResourceRefs: serviceSpec.TemplateResourceRefs,
		PolicyRefs:           serviceSpec.PolicyRefs,
		DriftIgnore:          serviceSpec.DriftIgnore,
		DriftExclusions:      serviceSpec.DriftExclusions,
		Priority:             serviceSpec.Priority,
		StopOnConflict:       serviceSpec.StopOnConflict,
		Reload:               serviceSpec.Reload,
		ContinueOnError:      serviceSpec.ContinueOnError,
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return kcmv1.StateManagementProviderConfig{}, err
	}

	return kcmv1.StateManagementProviderConfig{
		Config:         &apiextv1.JSON{Raw: raw},
		Name:           utils.DefaultStateManagementProvider,
		SelfManagement: serviceSpec.Provider.SelfManagement,
	}, nil
}
