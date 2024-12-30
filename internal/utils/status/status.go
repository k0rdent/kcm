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

package status

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// ConditionsFromUnstructured fetches all of the status.conditions from an
// unstructured object and returns them as a slice of metav1.Condition.  The
// status.conditions field is expected to be a slice of map[string]any
// which can be cast into a metav1.Condition.
func ConditionsFromUnstructured(unstrObj *unstructured.Unstructured) ([]metav1.Condition, error) {
	objKind, objName := ObjKindName(unstrObj)

	// Iterate the status conditions and ensure each condition reports a "Ready"
	// status.
	unstrConditions, found, err := unstructured.NestedSlice(unstrObj.Object, "status", "conditions")
	if err != nil {
		return nil, fmt.Errorf("failed to get status conditions for %s: %s: %w", objKind, objName, err)
	}
	if !found {
		return nil, fmt.Errorf("no status conditions found for %s: %s", objKind, objName)
	}

	conditions := make([]metav1.Condition, 0, len(unstrConditions))

	for _, condition := range unstrConditions {
		conditionMap, ok := condition.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected %s: %s condition to be type map[string]any, got: %T",
				objKind, objName, conditionMap)
		}

		var c *metav1.Condition

		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(conditionMap, &c); err != nil {
			return nil, fmt.Errorf("failed to convert condition map to metav1.Condition: %w", err)
		}

		// add some extra information for the origin of the message, i.e. what object reports this
		if c.Message != "" {
			c.Message = objName + ": " + c.Message
		} else {
			c.Message = objName
		}

		conditions = append(conditions, *c)
	}

	return conditions, nil
}

type ResourceNotFoundError struct {
	Resource string
}

func (e ResourceNotFoundError) Error() string {
	return fmt.Sprintf("no %s found, ignoring since object must be deleted or not yet created", e.Resource)
}

type ResourceConditions struct {
	Kind       string
	Name       string
	Conditions []metav1.Condition
}

// GetResourceConditions fetches the conditions from a resource identified by
// the provided GroupVersionResource and labelSelector.  The function returns
// a ResourceConditions struct containing the name/kind of the resource
// and the conditions.
// If the resource is not found, returns a ResourceNotFoundError which can be
// checked by the caller to prevent reconciliation loops.
func GetResourceConditions(
	ctx context.Context, namespace string, dynamicClient dynamic.Interface,
	gvr schema.GroupVersionResource, labelSelector string,
) (resourceConditions *ResourceConditions, err error) {
	list, err := dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ResourceNotFoundError{Resource: gvr.Resource}
		}

		return nil, fmt.Errorf("failed to list %s: %w", gvr.Resource, err)
	}

	if len(list.Items) == 0 {
		return nil, ResourceNotFoundError{Resource: gvr.Resource}
	}

	var conditions []metav1.Condition
	kind, name := ObjKindName(&list.Items[0])
	for _, item := range list.Items {
		c, err := ConditionsFromUnstructured(&item)
		if err != nil {
			return nil, fmt.Errorf("failed to get conditions: %w", err)
		}
		conditions = append(conditions, c...)
	}

	return &ResourceConditions{
		Kind:       kind,
		Name:       name,
		Conditions: conditions,
	}, nil
}

func ObjKindName(unstrObj *unstructured.Unstructured) (name, kind string) {
	return unstrObj.GetKind(), unstrObj.GetName()
}
