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

package serviceset

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/serviceset"
)

func GetServiceSet(ctx context.Context, cl client.Client, key client.ObjectKey) (*kcmv1.ServiceSet, error) {
	serviceSet := &kcmv1.ServiceSet{}
	if err := cl.Get(ctx, key, serviceSet); err != nil {
		return nil, err
	}
	return serviceSet, nil
}

// ValidateServiceSet validates if the ServiceSet is deployed and the provided services are also deployed.
func ValidateServiceSet(ctx context.Context, cl client.Client, serviceSetKey client.ObjectKey, services []client.ObjectKey) {
	Eventually(func() (err error) {
		defer func() {
			if err != nil {
				err = fmt.Errorf("[%s] failed ServiceSet validation: %v", serviceSetKey.String(), err)
				_, _ = fmt.Fprintf(GinkgoWriter, "%v\n", err)
			}
		}()
		serviceSet, err := GetServiceSet(ctx, cl, serviceSetKey)
		if err != nil {
			return err
		}

		if !serviceSet.Status.Deployed {
			return fmt.Errorf("not deployed yet")
		}

		// TODO: Could additionally check for ServiceSetProfile condition too under status.conditions?

		if len(services) == 0 {
			return nil
		}

		servicesMap := make(map[client.ObjectKey]bool)
		for _, svc := range services {
			servicesMap[svc] = false
		}

		// For each of the services in status:
		// 1. Check if it matches any of the provided services in servicesMap
		// 2. If Yes, then its value in servicesMap to True and
		// 3. Check if its state is "Deployed".
		for _, svc := range serviceSet.Status.Services {
			k := serviceset.ServiceKey(svc.Namespace, svc.Name)
			if _, ok := servicesMap[k]; ok {
				servicesMap[k] = true // we found provided service in status so set to true.
				if svc.State != kcmv1.ServiceStateDeployed {
					return fmt.Errorf("service %s in ServiceSet has state %s instead of %s", k.String(), svc.State, kcmv1.ServiceStateDeployed)
				}
			}
		}

		// Return error if any of the services in
		// servicesMap was not found in status.
		var errs error
		for svc, found := range servicesMap {
			if !found {
				err := fmt.Errorf("service %s not found in status of ServiceSet", svc.String())
				errs = errors.Join(errs, err)
			}
		}

		return errs
	}).WithTimeout(5 * time.Minute).WithPolling(3 * time.Second).Should(Succeed())
}
