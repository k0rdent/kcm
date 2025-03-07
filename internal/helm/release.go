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

package helm

import (
	"context"
	"time"

	hcv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/meta"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcm "github.com/K0rdent/kcm/api/v1alpha1"
)

const (
	DefaultReconcileInterval = 10 * time.Minute
)

type ReconcileHelmReleaseOpts struct {
	Values            *apiextensionsv1.JSON
	OwnerReference    *metav1.OwnerReference
	ChartRef          *hcv2.CrossNamespaceSourceReference
	ReconcileInterval *time.Duration
	Install           *hcv2.Install
	TargetNamespace   string
	DependsOn         []meta.NamespacedObjectReference
}

func ReconcileHelmRelease(ctx context.Context,
	cl client.Client,
	name string,
	namespace string,
	opts ReconcileHelmReleaseOpts,
) (*hcv2.HelmRelease, controllerutil.OperationResult, error) {
	hr := &hcv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	operation, err := ctrl.CreateOrUpdate(ctx, cl, hr, func() error {
		if hr.Labels == nil {
			hr.Labels = make(map[string]string)
		}
		hr.Labels[kcm.KCMManagedLabelKey] = kcm.KCMManagedLabelValue

		if opts.OwnerReference != nil {
			hr.OwnerReferences = []metav1.OwnerReference{*opts.OwnerReference}
		}

		hr.Spec.ChartRef = opts.ChartRef
		hr.Spec.Interval = metav1.Duration{Duration: func() time.Duration {
			if opts.ReconcileInterval != nil {
				return *opts.ReconcileInterval
			}
			return DefaultReconcileInterval
		}()}
		hr.Spec.ReleaseName = name

		if opts.Values != nil {
			hr.Spec.Values = opts.Values
		}
		if opts.DependsOn != nil {
			hr.Spec.DependsOn = opts.DependsOn
		}
		if opts.TargetNamespace != "" {
			hr.Spec.TargetNamespace = opts.TargetNamespace
		}
		if opts.Install != nil {
			hr.Spec.Install = opts.Install
		}
		return nil
	})
	if err != nil {
		return nil, operation, err
	}

	return hr, operation, nil
}

func DeleteHelmRelease(ctx context.Context, cl client.Client, name, namespace string) error {
	err := cl.Delete(ctx, &hcv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	})
	if client.IgnoreNotFound(err) != nil {
		return err
	}
	return nil
}
