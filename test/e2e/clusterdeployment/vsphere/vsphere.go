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

package vsphere

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"os"
	"sync"

	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
)

func CheckEnv() {
	clusterdeployment.ValidateDeploymentVars([]string{
		clusterdeployment.EnvVarVSphereUser,
		clusterdeployment.EnvVarVSpherePassword,
		clusterdeployment.EnvVarVSphereServer,
		clusterdeployment.EnvVarVSphereThumbprint,
		clusterdeployment.EnvVarVSphereDatacenter,
		clusterdeployment.EnvVarVSphereDatastore,
		clusterdeployment.EnvVarVSphereResourcepool,
		clusterdeployment.EnvVarVSphereFolder,
		clusterdeployment.EnvVarVSphereControlPlaneEndpoint,
		clusterdeployment.EnvVarVSphereVMTemplate,
		clusterdeployment.EnvVarVSphereNetwork,
		clusterdeployment.EnvVarVSphereSSHKey,
	})
}

func SetControlPlaneEndpointEnv() error {
	return setCPEndpointEnv(clusterdeployment.EnvVarVSphereControlPlaneEndpoint)
}

func SetHostedControlPlaneEndpointEnv() error {
	return setCPEndpointEnv(clusterdeployment.EnvVarVSphereHostedControlPlaneEndpoint)
}

func setCPEndpointEnv(envName string) error {
	// already checked the var is set
	sdIP := netip.MustParseAddr(os.Getenv(clusterdeployment.EnvVarVSphereControlPlaneEndpoint)) // always check the SD CP Endpoint

	var hdIP netip.Addr
	if envName != clusterdeployment.EnvVarVSphereControlPlaneEndpoint {
		if v, ok := os.LookupEnv(envName); ok && len(v) > 0 {
			hdIP = netip.MustParseAddr(v)
		}
	}

	last, err := getRandomLastByte(sdIP, hdIP)
	if err != nil {
		return err
	}

	i4 := sdIP.As4()
	i4[3] = last
	newIP := netip.AddrFrom4(i4)

	if !newIP.IsValid() {
		return fmt.Errorf("got new IP4 address %s which is invalid", newIP)
	}

	if err := os.Setenv(envName, newIP.String()); err != nil {
		return fmt.Errorf("unexpected error setting %s env var: %w", envName, err)
	}

	return nil
}

var (
	mu sync.RWMutex

	usedBytes map[byte]struct{}
)

// getRandomLastByte gets random unused byte in range [210, 254]
// to be compatible with the currently supported VM Network DHCP Range.
// Fails no free bytes left.
// First call reserves the last octet from the given standalone IP.
func getRandomLastByte(sdIP, hdIP netip.Addr) (byte, error) {
	var (
		last   byte
		probes uint
	)
	for {
		if probes > 43 {
			return 0, fmt.Errorf("no free last bytes left")
		}

		last = byte(210 + rand.IntN(45))
		mu.RLock()
		_, used := usedBytes[last]
		mu.RUnlock()
		if !used && last != sdIP.As4()[3] && (!hdIP.IsValid() || last != hdIP.As4()[3]) {
			break
		}

		probes++
	}

	mu.Lock()
	if usedBytes == nil {
		usedBytes = map[byte]struct{}{
			sdIP.As4()[3]: {}, // the very first ip octet (initial standalone CP IP)
		}
	}
	usedBytes[last] = struct{}{}
	mu.Unlock()

	return last, nil
}
