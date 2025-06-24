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

package kubevirt

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/gomega"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/K0rdent/kcm/test/e2e/logs"
)

func GetDataVolume(ctx context.Context, cl crclient.Client, namespace, name string) (*cdiv1.DataVolume, error) {
	dv := &cdiv1.DataVolume{}
	err := cl.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, dv)
	if err != nil {
		return nil, err
	}
	return dv, nil
}

func WaitDataVolumeReady(ctx context.Context, cl crclient.Client, namespace, name string) {
	Eventually(func() bool {
		dv, err := GetDataVolume(ctx, cl, namespace, name)
		if err != nil {
			logs.Println(err.Error())
			return false
		}
		if dv.Status.Phase != cdiv1.Succeeded {
			logs.Println(fmt.Sprintf("Data Volume %s/%s is not ready yet. Phase: %s. Progress: %s", namespace, name, dv.Status.Phase, dv.Status.Progress))
			return false
		}
		logs.Println("Data Volume is Ready")
		return true
	}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(BeTrue())
}
