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

package collector

import (
	"context"
	"fmt"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type scope int8

const (
	scopeManagement scope = 1 << iota
	scopeRegional
	scopeOnline
	scopeLocal

	fetcherMgmtOnline     = scopeManagement | scopeOnline
	fetcherMgmtLocal      = scopeManagement | scopeLocal
	fetcherRegionalOnline = scopeRegional | scopeOnline
	fetcherRegionalLocal  = scopeRegional | scopeLocal
)

//nolint:exhaustive // no need to print internals
func (s scope) String() string {
	switch s {
	case fetcherMgmtOnline:
		return "mgmt_online"
	case fetcherMgmtLocal:
		return "mgmt_local"
	case fetcherRegionalOnline:
		return "regional_online"
	case fetcherRegionalLocal:
		return "regional_local"
	default:
		return "unknown_scope"
	}
}

func (s scope) isOnline() bool {
	return s&scopeLocal == 0
}

func (s scope) isMgmt() bool {
	return s&scopeRegional == 0
}

func isMgmtCluster(ctx context.Context, cl client.Client) (bool, error) {
	mgmtCRD := &metav1.PartialObjectMetadata{}
	mgmtCRD.SetGroupVersionKind(apiextv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"))

	if err := cl.Get(ctx, client.ObjectKey{Name: "managements.k0rdent.mirantis.com"}, mgmtCRD); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get CRD: %w", err)
	}

	return true, nil
}
