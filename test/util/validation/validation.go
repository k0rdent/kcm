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
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	statusutil "github.com/K0rdent/kcm/internal/util/status"
)

// ValidateConditionsTrue iterates over the conditions of the given
// unstructured object and returns an error if any of the conditions are not
// true. Conditions are expected to be of type metav1.Condition.
//
// If includeTypes argument is set, then validation includes only the given,
// otherwise all of the available conditions are up to be tested against Status == True.
func ValidateConditionsTrue(unstrObj *unstructured.Unstructured, includeTypes ...string) error {
	objKind, objName := statusutil.ObjKindName(unstrObj)

	conditions, err := statusutil.ConditionsFromUnstructured(unstrObj)
	if err != nil {
		return fmt.Errorf("failed to get conditions from unstructured object: %w", err)
	}

	include := make(map[string]struct{}, len(includeTypes))
	for _, v := range includeTypes {
		include[v] = struct{}{}
	}

	var errs error
	for _, c := range conditions {
		if len(include) > 0 {
			if _, ok := include[c.Type]; !ok {
				continue
			}
		}
		if c.Status == metav1.ConditionTrue {
			continue
		}

		errs = errors.Join(errors.New(ConvertConditionsToString(c)), errs)
	}

	if errs != nil {
		return fmt.Errorf("%s %s is not ready with conditions:\n%w", objKind, objName, errs)
	}

	return nil
}

func ConvertConditionsToString(condition metav1.Condition) string {
	return fmt.Sprintf("Type: %s, Status: %s, Reason: %s, Message: %s",
		condition.Type, condition.Status, condition.Reason, condition.Message)
}

// ValidateObjectNamePrefix checks if the given object name has the given prefix.
func ValidateObjectNamePrefix(obj *unstructured.Unstructured, prefix string) error {
	objKind, objName := statusutil.ObjKindName(obj)

	// Verify the machines are prefixed with the cluster name and fail
	// the test if they are not.
	if !strings.HasPrefix(objName, prefix) {
		return fmt.Errorf("object %s %s does not have prefix: %s", objKind, objName, prefix)
	}

	return nil
}
