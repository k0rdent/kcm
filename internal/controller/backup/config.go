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
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
