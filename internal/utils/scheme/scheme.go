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

package scheme

import (
	"fmt"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	infobloxv1alpha1 "github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	velerov2alpha1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v2alpha1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	inclusteripamv1alpha2 "sigs.k8s.io/cluster-api-ipam-provider-in-cluster/api/v1alpha2"
	capioperatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha2"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1beta1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func MustGetManagementScheme() *runtime.Scheme {
	s, err := getManagementScheme()
	if err != nil {
		panic(err)
	}
	return s
}

func getManagementScheme() (*runtime.Scheme, error) {
	s, err := GetRegionalScheme()
	if err != nil {
		return nil, err
	}

	for _, f := range []func(*runtime.Scheme) error{
		kcmv1.AddToScheme,
		sourcev1.AddToScheme,
		helmcontrollerv2.AddToScheme,
	} {
		if err := f(s); err != nil {
			return nil, fmt.Errorf("failed to add to scheme: %w", err)
		}
	}
	return s, nil
}

func GetRegionalScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	for _, f := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		// velero deps
		velerov1.AddToScheme,
		velerov2alpha1.AddToScheme,
		apiextv1.AddToScheme,
		apiextv1beta1.AddToScheme,
		// WARN: if snapshot is to be used, then the following resources should also be added to the scheme
		// snapshotv1api.AddToScheme(scheme) // snapshotv1api "github.com/kubernetes-csi/external-snapshotter/client/v7/apis/volumesnapshot/v1"
		// velero deps
		capioperatorv1.AddToScheme,
		clusterapiv1.AddToScheme,
		ipamv1.AddToScheme,
		inclusteripamv1alpha2.AddToScheme,
		infobloxv1alpha1.AddToScheme,
	} {
		if err := f(s); err != nil {
			return nil, fmt.Errorf("failed to add to scheme: %w", err)
		}
	}
	return s, nil
}
