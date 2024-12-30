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

package backup

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
)

// Reconciler has logic to create and reconcile [github.com/vmware-tanzu/velero/pkg/apis/velero/v1.Backup] objects.
type Reconciler struct {
	scheme *runtime.Scheme
	cl     client.Client

	systemNamespace string
}

// NewReconciler creates instance of the [Reconciler].
func NewReconciler(cl client.Client, scheme *runtime.Scheme, systemNamespace string) *Reconciler {
	return &Reconciler{
		cl:              cl,
		scheme:          scheme,
		systemNamespace: systemNamespace,
	}
}

// ErrNoManagementExists is a sentinel error indicating no [github.com/K0rdent/kcm/api/v1alpha1.Management] object exists.
var ErrNoManagementExists = errors.New("no Management object exists")

// GetManagement fetches a [github.com/K0rdent/kcm/api/v1alpha1.Management] object.
func (r *Reconciler) GetManagement(ctx context.Context) (*kcmv1alpha1.Management, error) {
	mgmts := new(kcmv1alpha1.ManagementList)
	if err := r.cl.List(ctx, mgmts, client.Limit(1)); err != nil {
		return nil, fmt.Errorf("failed to list Management: %w", err)
	}

	if len(mgmts.Items) == 0 {
		return nil, ErrNoManagementExists
	}

	return &mgmts.Items[0], nil
}
