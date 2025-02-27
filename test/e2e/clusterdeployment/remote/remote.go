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

package remote

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/K0rdent/kcm/test/e2e/kubevirt"
	"github.com/K0rdent/kcm/test/e2e/logs"
)

func PrepareVMs(ctx context.Context, cl crclient.Client, namespace, clusterName, publicSSHKey string, n int) ([]int, error) {
	vmNames := getVMNames(clusterName, n)
	for _, vmName := range vmNames {
		err := kubevirt.CreateVirtualMachine(ctx, cl, namespace, vmName, publicSSHKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create VirtualMachines %s: %w", vmName, err)
		}
	}

	for _, vmName := range vmNames {
		kubevirt.WaitVirtualMachineReady(ctx, cl, namespace, vmName)
	}

	// Expose virtual machines as services
	ports := make([]int, 0, n)
	for _, vmName := range vmNames {
		port, err := exposeVM(ctx, cl, namespace, vmName)
		if err != nil {
			return nil, fmt.Errorf("failed to expose VirtualMachine %s/%s: %w", namespace, vmName, err)
		}
		ports = append(ports, port)
	}
	return ports, nil
}

func getVMNames(clusterName string, n int) []string {
	vmNames := make([]string, n)
	for i := range vmNames {
		vmNames[i] = fmt.Sprintf("%s-%d", clusterName, i)
	}
	return vmNames
}

func exposeVM(ctx context.Context, cl crclient.Client, namespace, vmName string) (port int, err error) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      getServiceName(vmName),
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       20222,
					TargetPort: intstr.FromInt32(22),
				},
			},
			Selector: map[string]string{
				"kubevirt.io/domain": vmName,
			},
		},
	}
	err = cl.Create(ctx, svc)
	if err != nil {
		return 0, err
	}

	Eventually(func() bool {
		err := cl.Get(ctx, types.NamespacedName{Namespace: namespace, Name: getServiceName(vmName)}, svc)
		if err != nil {
			logs.Println(err.Error())
			return false
		}
		for _, p := range svc.Spec.Ports {
			if p.NodePort == 0 {
				logs.Println("waiting for NodePort to be assigned")
				return false
			}
			port = int(p.NodePort)
		}
		return true
	}).WithTimeout(10 * time.Minute).WithPolling(20 * time.Second).Should(BeTrue())

	return port, nil
}

func getServiceName(vmName string) string {
	return vmName + "-ssh"
}

func GetAddress(ctx context.Context, cl crclient.Client) (string, error) {
	nodes := &corev1.NodeList{}
	err := cl.List(ctx, nodes)
	if err != nil {
		return "", err
	}
	for _, node := range nodes.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				return addr.Address, nil
			}
		}
	}
	return "", fmt.Errorf("couldn't find nodes' InternalIP address")
}
