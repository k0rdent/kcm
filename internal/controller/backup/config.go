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
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Reconciler has logic to create and reconcile [github.com/vmware-tanzu/velero/pkg/apis/velero/v1.Backup] objects.
type Reconciler struct {
	mgmtCl client.Client

	regionalFactory RegionalClientFactory

	systemNamespace string
}

// NewReconciler creates instance of the [Reconciler].
func NewReconciler(cl client.Client, systemNamespace string, opts ...ReconcilerOption) *Reconciler {
	r := &Reconciler{
		mgmtCl:          cl,
		systemNamespace: systemNamespace,
		regionalFactory: defaultRegionalClientFactory,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// ReconcilerOption is a configuration option for the [Reconciler].
type ReconcilerOption func(*Reconciler)

// WithRegionalClientFactory returns an option to set a custom regional client factory.
func WithRegionalClientFactory(factory RegionalClientFactory) ReconcilerOption {
	return func(r *Reconciler) {
		r.regionalFactory = factory
	}
}
