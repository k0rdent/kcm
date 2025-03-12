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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/K0rdent/kcm/internal/utils"
	"github.com/K0rdent/kcm/test/e2e/logs"
)

const (
	machineType = "q35"
	sourceURL   = "https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img"
)

var (
	defaultStorageRequest = resource.MustParse("3.5Gi")
	defaultMemoryRequest  = resource.MustParse("1024M")
)

func CreateVirtualMachine(ctx context.Context, cl crclient.Client, namespace, name, publicSSHKey string) error {
	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"kubevirt.io/os": "linux",
			},
		},
		Spec: getDefaultVirtualMachineSpec(namespace, name, publicSSHKey),
	}
	return cl.Create(ctx, vm)
}

func WaitVirtualMachineReady(ctx context.Context, cl crclient.Client, namespace, name string) {
	WaitDataVolumeReady(ctx, cl, namespace, name)

	Eventually(func() bool {
		vm, err := GetVirtualMachine(ctx, cl, namespace, name)
		if err != nil {
			logs.Println(err.Error())
			return false
		}
		if !vm.Status.Ready {
			for _, condition := range vm.Status.Conditions {
				if condition.Type == kubevirtv1.VirtualMachineReady {
					logs.Println(fmt.Sprintf("Virtual Machine %s/%s is not ready yet. Reason: %s. Message: %s", namespace, name, condition.Reason, condition.Message))
					return false
				}
			}
			logs.Println(fmt.Sprintf("Virtual Machine %s/%s is not ready yet. Ready condition is not found", namespace, name))
			return false
		}
		logs.Println("Virtual Machine is ready")
		return true
	}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(BeTrue())
}

func GetVirtualMachine(ctx context.Context, cl crclient.Client, namespace, name string) (*kubevirtv1.VirtualMachine, error) {
	vm := &kubevirtv1.VirtualMachine{}
	err := cl.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, vm)
	if err != nil {
		return nil, err
	}
	return vm, nil
}

func getDefaultVirtualMachineSpec(namespace, name, publicSSHKey string) kubevirtv1.VirtualMachineSpec {
	dvName := name
	runStrategy := kubevirtv1.RunStrategyAlways
	spec := kubevirtv1.VirtualMachineSpec{
		RunStrategy: &runStrategy,
		DataVolumeTemplates: []kubevirtv1.DataVolumeTemplateSpec{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      dvName,
				},
				Spec: cdiv1.DataVolumeSpec{
					Storage: &cdiv1.StorageSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: defaultStorageRequest,
							},
						},
						StorageClassName: utils.PtrTo("standard"),
					},
					Source: &cdiv1.DataVolumeSource{
						HTTP: &cdiv1.DataVolumeSourceHTTP{
							URL: sourceURL,
						},
					},
				},
			},
		},
		Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					"kubevirt.io/domain": name,
				},
			},
			Spec: kubevirtv1.VirtualMachineInstanceSpec{
				DNSConfig: &corev1.PodDNSConfig{
					Nameservers: []string{"8.8.8.8"},
				},
				Domain: kubevirtv1.DomainSpec{
					CPU:     &kubevirtv1.CPU{Cores: 2},
					Devices: getDefaultDevices(),
					Machine: &kubevirtv1.Machine{
						Type: machineType,
					},
					Resources: kubevirtv1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: defaultMemoryRequest,
						},
					},
				},
				Volumes: getDefaultVolumes(name, dvName, publicSSHKey),
			},
		},
	}
	return spec
}

func getDefaultDevices() kubevirtv1.Devices {
	return kubevirtv1.Devices{
		Disks: []kubevirtv1.Disk{
			{
				DiskDevice: kubevirtv1.DiskDevice{
					Disk: &kubevirtv1.DiskTarget{
						Bus: kubevirtv1.DiskBusVirtio,
					},
				},
				Name: "disk",
			},
			{
				DiskDevice: kubevirtv1.DiskDevice{
					CDRom: &kubevirtv1.CDRomTarget{
						Bus:      kubevirtv1.DiskBusSATA,
						ReadOnly: utils.PtrTo(true),
					},
				},
				Name: "cloudinitdisk",
			},
		},
	}
}

func getDefaultVolumes(vmName, claimName, publicSSHKey string) []kubevirtv1.Volume {
	return []kubevirtv1.Volume{
		{
			Name: "disk",
			VolumeSource: kubevirtv1.VolumeSource{
				PersistentVolumeClaim: &kubevirtv1.PersistentVolumeClaimVolumeSource{
					PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: claimName,
					},
				},
			},
		},
		{
			Name: "cloudinitdisk",
			VolumeSource: kubevirtv1.VolumeSource{
				CloudInitNoCloud: &kubevirtv1.CloudInitNoCloudSource{
					UserData: getUserData(vmName, publicSSHKey),
				},
			},
		},
	}
}

func getUserData(hostname, publicSSHKey string) string {
	return fmt.Sprintf(`#cloud-config
hostname: %s
disable_root: false
ssh_authorized_keys:
- %s`, hostname, publicSSHKey)
}
