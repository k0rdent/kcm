package serviceset

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
)

// Processor is used to process ServiceSet objects.
type Processor struct {
	client.Client

	ProviderSpec kcmv1beta1.ProviderSpec

	Services []kcmv1beta1.Service
}

// NewProcessor creates a new Processor with the given client.
func NewProcessor(cl client.Client) *Processor {
	return &Processor{
		Client: cl,
	}
}

// CreateOrUpdateServiceSet manages the lifecycle of a ServiceSet based on the specified operation (create, update, or no-op).
// It attempts to create or update the given ServiceSet object and handles errors such as conflicts or already exists.
// Returns a requeue flag to indicate if the parent object should be requeued and an error if the operation fails.
func (p *Processor) CreateOrUpdateServiceSet(
	ctx context.Context,
	op kcmv1beta1.ServiceSetOperation,
	serviceSet *kcmv1beta1.ServiceSet,
) (requeue bool, err error) {
	l := ctrl.LoggerFrom(ctx)
	serviceSetObjectKey := client.ObjectKeyFromObject(serviceSet)
	switch op {
	case kcmv1beta1.ServiceSetOperationCreate:
		l.V(1).Info("creating ServiceSet", "namespaced_name", serviceSetObjectKey)
		err = p.Create(ctx, serviceSet)
		// we'll return an error in case ServiceSet creation fails due to any
		// error except AlreadyExists
		if client.IgnoreAlreadyExists(err) != nil {
			return false, fmt.Errorf("failed to create ServiceSet %s: %w", serviceSetObjectKey, err)
		}
		// we'll requeue if the ServiceSet object already exists, so that
		// on next reconciliation we'll attempt to update it if needed
		if apierrors.IsAlreadyExists(err) {
			return true, nil
		}
		l.V(1).Info("Successfully created ServiceSet", "namespaced_name", serviceSetObjectKey)
		return true, nil
	case kcmv1beta1.ServiceSetOperationUpdate:
		l.V(1).Info("updating ServiceSet", "namespaced_name", serviceSetObjectKey)
		err = p.Update(ctx, serviceSet)
		// we'll requeue if ServiceSet update fails due to a conflict
		if apierrors.IsConflict(err) {
			return true, nil
		}
		// otherwise we'll return the error if any occurred
		if err != nil {
			return false, fmt.Errorf("failed to update ServiceSet %s: %w", serviceSetObjectKey, err)
		}
		l.V(1).Info("Successfully updated ServiceSet", "namespaced_name", serviceSetObjectKey)
		return true, nil
	case kcmv1beta1.ServiceSetOperationDelete, kcmv1beta1.ServiceSetOperationNone:
		// no-op
	}
	return false, nil
}
