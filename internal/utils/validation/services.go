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

	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1alpha1"
)

// ServicesHaveValidTemplates validates the given array of [github.com/K0rdent/kcm/api/v1alpha1.Service] checking
// if referenced [github.com/K0rdent/kcm/api/v1alpha1.ServiceTemplate] is valid and is ready to be consumed.
func ServicesHaveValidTemplates(ctx context.Context, cl client.Client, services []kcmv1.Service, ns string) error {
	var errs error
	for _, svc := range services {
		svcTemplate := new(kcmv1.ServiceTemplate)
		key := client.ObjectKey{Namespace: ns, Name: svc.Template}
		if err := cl.Get(ctx, key, svcTemplate); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to get ServiceTemplate %s: %w", key, err))
			continue
		}

		if !svcTemplate.Status.Valid {
			errs = errors.Join(errs, fmt.Errorf("the ServiceTemplate %s is invalid with the error: %s", key, svcTemplate.Status.ValidationError))
		}
	}

	return errs
}
