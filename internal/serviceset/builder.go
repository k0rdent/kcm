package serviceset

import (
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/selection"

	kcmv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
)

// Builder is a builder for ServiceSet objects.
// It defines all necessary parameters and dependencies to
// either create or update a ServiceSet object.
type Builder struct {
	// ServiceSet is the base ServiceSet which will be mutated as needed
	ServiceSet *kcmv1beta1.ServiceSet

	// ClusterDeployment is the related ClusterDeployment
	ClusterDeployment *kcmv1beta1.ClusterDeployment

	// MultiClusterService is the related MultiClusterService if any
	MultiClusterService *kcmv1beta1.MultiClusterService

	// Selector is the selector used to extract labels for the ServiceSet
	Selector *metav1.LabelSelector

	// ServicesToDeploy is the list of services to deploy
	ServicesToDeploy []kcmv1beta1.ServiceWithValues
}

// NewBuilder returns a new Builder with mandatory parameters set.
func NewBuilder(clusterDeployment *kcmv1beta1.ClusterDeployment, serviceSet *kcmv1beta1.ServiceSet, selector *metav1.LabelSelector) *Builder {
	return &Builder{
		ClusterDeployment: clusterDeployment,
		ServiceSet:        serviceSet,
		Selector:          selector,
	}
}

// WithMultiClusterService sets the related MultiClusterService.
func (b *Builder) WithMultiClusterService(multiClusterService *kcmv1beta1.MultiClusterService) *Builder {
	b.MultiClusterService = multiClusterService
	return b
}

// WithServicesToDeploy sets the list of services to deploy.
func (b *Builder) WithServicesToDeploy(servicesToDeploy []kcmv1beta1.ServiceWithValues) *Builder {
	b.ServicesToDeploy = servicesToDeploy
	return b
}

// Build constructs and returns a ServiceSet object based on the builder's parameters or returns an error if invalid.
func (b *Builder) Build() (*kcmv1beta1.ServiceSet, error) {
	ownerReference := metav1.NewControllerRef(b.ClusterDeployment, kcmv1beta1.GroupVersion.WithKind(kcmv1beta1.ClusterDeploymentKind))
	b.ServiceSet.OwnerReferences = []metav1.OwnerReference{*ownerReference}

	labels, err := extractRequiredLabels(b.Selector)
	if err != nil {
		return nil, fmt.Errorf("failed to extract required labels from StateManagementProvider selector: %w", err)
	}
	b.ServiceSet.Labels = func() map[string]string {
		// we'll overwrite ServiceSet labels with those matching the StateManagementProvider selector
		// if the ServiceSet also has labels that don't match the selector, then this ServiceSet
		// won't be reconciled
		if b.ServiceSet.Labels == nil {
			return labels
		}
		for k, v := range labels {
			b.ServiceSet.Labels[k] = v
		}
		return b.ServiceSet.Labels
	}()

	b.ServiceSet.Spec = kcmv1beta1.ServiceSetSpec{
		Cluster:            b.ClusterDeployment.Name,
		EffectiveNamespace: b.ClusterDeployment.Namespace,
		Provider:           b.ClusterDeployment.Spec.ServiceSpec.Provider,
		Services:           b.ServicesToDeploy,
	}
	if b.MultiClusterService != nil {
		b.ServiceSet.Spec.Provider = b.MultiClusterService.Spec.ServiceSpec.Provider
		b.ServiceSet.Spec.MultiClusterService = b.MultiClusterService.Name
	}
	return b.ServiceSet, nil
}

// extractRequiredLabels extracts the required labels from a selector.
func extractRequiredLabels(selector *metav1.LabelSelector) (map[string]string, error) {
	if selector == nil {
		return nil, errors.New("selector cannot be nil")
	}

	result := make(map[string]string)

	for k, v := range selector.MatchLabels {
		result[k] = v
	}

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
