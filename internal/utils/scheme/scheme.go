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
	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	infobloxv1alpha1 "github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	velerov2alpha1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v2alpha1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	inclusteripamv1alpha2 "sigs.k8s.io/cluster-api-ipam-provider-in-cluster/api/v1alpha2"
	capioperatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha2"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1beta1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

func RegisterManagement(scheme *runtime.Scheme) {
	utilruntime.Must(kcmv1.AddToScheme(scheme))
	utilruntime.Must(sourcev1.AddToScheme(scheme))
	utilruntime.Must(helmcontrollerv2.AddToScheme(scheme))

	RegisterRegional(scheme)
}

func RegisterRegional(scheme *runtime.Scheme) {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	// velero deps
	utilruntime.Must(velerov1.AddToScheme(scheme))
	utilruntime.Must(velerov2alpha1.AddToScheme(scheme))
	utilruntime.Must(apiextv1.AddToScheme(scheme))
	utilruntime.Must(apiextv1beta1.AddToScheme(scheme))
	// WARN: if snapshot is to be used, then the following resources should also be added to the scheme
	// snapshotv1api.AddToScheme(scheme) // snapshotv1api "github.com/kubernetes-csi/external-snapshotter/client/v7/apis/volumesnapshot/v1"
	// velero deps

	utilruntime.Must(capioperatorv1.AddToScheme(scheme))
	utilruntime.Must(clusterapiv1.AddToScheme(scheme))
	utilruntime.Must(ipamv1.AddToScheme(scheme))
	utilruntime.Must(inclusteripamv1alpha2.AddToScheme(scheme))
	utilruntime.Must(infobloxv1alpha1.AddToScheme(scheme))
}
