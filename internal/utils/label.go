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

package utils

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
)

// AddLabel adds the provided label key and value to the object if not presented
// or if the existing label value does not equal the given one.
// Returns an indication of whether it updated the labels of the object.
func AddLabel(o client.Object, labelKey, labelValue string) (labelsUpdated bool) {
	l := o.GetLabels()
	v, ok := l[labelKey]
	if ok && v == labelValue {
		return false
	}
	if l == nil {
		l = make(map[string]string)
	}
	l[labelKey] = labelValue
	o.SetLabels(l)
	return true
}

// HasLabel is a helper function to check for a specific label existence
func HasLabel(obj client.Object, labelName string) bool {
	labels := obj.GetLabels()
	if labels == nil {
		return false
	}

	_, exists := labels[labelName]
	return exists
}

// AddKCMComponentLabel adds the common KCM component label with the kcm value to the given object
// and updates it if it is required.
func AddKCMComponentLabel(ctx context.Context, cl client.Client, o client.Object) (labelsUpdated bool, err error) {
	if !AddLabel(o, kcm.GenericComponentNameLabel, kcm.GenericComponentLabelValueKCM) {
		return false, nil
	}

	if err := cl.Update(ctx, o); err != nil {
		return false, fmt.Errorf("failed to update %s %s labels: %w", o.GetObjectKind().GroupVersionKind().Kind, client.ObjectKeyFromObject(o), err)
	}

	return true, nil
}

// AddPluggableProviderLabels adds infrastructure and cluster API provider labels to the given object
// and updates it if it is required.
func AddPluggableProviderLabels(ctx context.Context, cl client.Client, o client.Object) (labelsUpdated bool, err error) {
	infraLabelUpdated := AddLabel(o, kcm.InfrastructureProviderLabel, fmt.Sprintf("%s%s", kcm.InfrastructureProviderPrefix, o.GetName()))
	capiLabelUpdated := AddLabel(o, kcm.ClusterAPIProviderLabel, fmt.Sprintf("%s%s", kcm.ClusterAPIProviderPrefix, o.GetName()))

	if !infraLabelUpdated && !capiLabelUpdated {
		return false, nil
	}

	if err := cl.Update(ctx, o); err != nil {
		return false, fmt.Errorf("failed to update %s %s labels: %w", o.GetObjectKind().GroupVersionKind().Kind, client.ObjectKeyFromObject(o), err)
	}

	return true, nil
}
