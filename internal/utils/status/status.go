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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
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

func ObjKindName(unstrObj *unstructured.Unstructured) (name, kind string) {
	return unstrObj.GetKind(), unstrObj.GetName()
}
