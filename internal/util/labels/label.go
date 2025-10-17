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

package labels

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// AddKCMComponentLabel adds the common KCM component label with the kcm value to the given object
// and updates it if it is required.
func AddKCMComponentLabel(ctx context.Context, cl client.Client, o client.Object) (labelsUpdated bool, err error) {
	v, ok := o.GetLabels()[kcmv1.GenericComponentNameLabel]
	if ok && v == kcmv1.GenericComponentLabelValueKCM {
		return false, nil
	}
	patch := fmt.Appendf(nil, `{"metadata":{"labels":{"%s":"%s"}}}`, kcmv1.GenericComponentNameLabel, kcmv1.GenericComponentLabelValueKCM)
	if err := cl.Patch(ctx, o, client.RawPatch(types.MergePatchType, patch)); err != nil {
		return false, fmt.Errorf("failed to update %s %s labels: %w", o.GetObjectKind().GroupVersionKind().Kind, client.ObjectKeyFromObject(o), err)
	}
	return true, nil
}
