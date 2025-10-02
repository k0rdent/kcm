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

package upgrade

import (
	"context"
	"errors"
	"fmt"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	validationutil "github.com/K0rdent/kcm/test/util/validation"
)

type validationFunc func(context.Context, crclient.Client, crclient.Client, string, string, string) error

type ClusterUpgrade struct {
	mgmtClient  crclient.Client
	childClient crclient.Client
	namespace   string
	name        string
	newTemplate string
	validator   Validator
}

type Validator struct {
	validationFuncs []validationFunc
}

func NewClusterUpgrade(mgmtClient, childClient crclient.Client, namespace, name, newTemplate string, validator Validator) ClusterUpgrade {
	return ClusterUpgrade{
		mgmtClient:  mgmtClient,
		childClient: childClient,
		namespace:   namespace,
		name:        name,
		newTemplate: newTemplate,
		validator:   validator,
	}
}

func NewDefaultClusterValidator() Validator {
	return Validator{validationFuncs: []validationFunc{
		validateHelmRelease,
		validateClusterConditions,
	}}
}

func (v *ClusterUpgrade) Validate(ctx context.Context) {
	Eventually(func() bool {
		for _, validate := range v.validator.validationFuncs {
			if err := validate(ctx, v.mgmtClient, v.childClient, v.namespace, v.name, v.newTemplate); err != nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "[%s/%s] upgrade validation error: %v\n", v.namespace, v.name, err)
				return false
			}
		}
		return true
	}, 20*time.Minute, 20*time.Second).Should(BeTrue())
}

func validateHelmRelease(ctx context.Context, mgmtClient, _ crclient.Client, namespace, name, newTemplate string) error {
	hr := &helmcontrollerv2.HelmRelease{}
	err := mgmtClient.Get(ctx, crclient.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, hr)
	if err != nil {
		return fmt.Errorf("failed to get %s/%s HelmRelease: %v", namespace, name, err)
	}

	template := &kcmv1.ClusterTemplate{}
	if err := mgmtClient.Get(ctx, crclient.ObjectKey{
		Namespace: namespace,
		Name:      newTemplate,
	}, template); err != nil {
		return err
	}
	if hr.Spec.ChartRef.Name != template.Status.ChartRef.Name {
		return fmt.Errorf("waiting for chartName to be updated in %s/%s HelmRelease", namespace, name)
	}
	readyCondition := apimeta.FindStatusCondition(hr.GetConditions(), kcmv1.ReadyCondition)
	if readyCondition == nil {
		return fmt.Errorf("waiting for %s/%s HelmRelease to have Ready condition", namespace, name)
	}
	if readyCondition.ObservedGeneration != hr.Generation {
		return errors.New("waiting for status.observedGeneration to be updated")
	}
	if readyCondition.Status != metav1.ConditionTrue {
		return errors.New("waiting for Ready condition to have status: true")
	}
	if readyCondition.Reason != helmcontrollerv2.UpgradeSucceededReason {
		return errors.New("waiting for Ready condition to have `UpgradeSucceeded` reason")
	}
	return nil
}

func validateClusterConditions(ctx context.Context, mgmtClient, _ crclient.Client, namespace, name, _ string) error {
	cluster := &clusterapiv1.Cluster{}
	if err := mgmtClient.Get(ctx, crclient.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, cluster); err != nil {
		return err
	}

	conditionsToCheck := map[string]struct{}{
		// TODO: uncomment once https://github.com/k0sproject/k0smotron/issues/911 is fixed
		// clusterapiv1beta1.ClusterControlPlaneMachinesUpToDateCondition: {},
		clusterapiv1.ClusterControlPlaneMachinesReadyCondition: {},
		clusterapiv1.ClusterWorkerMachinesReadyCondition:       {},
		clusterapiv1.ClusterWorkerMachinesUpToDateCondition:    {},
	}
	var errs error
	for _, c := range cluster.GetConditions() {
		if _, ok := conditionsToCheck[c.Type]; !ok {
			continue
		}
		if c.Status != metav1.ConditionTrue {
			errs = errors.Join(errors.New(validationutil.ConvertConditionsToString(c)), errs)
		}
	}
	if errs != nil {
		return fmt.Errorf("cluster %s/%s is not ready with conditions:\n%w", namespace, name, errs)
	}
	return nil
}
