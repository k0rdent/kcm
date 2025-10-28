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

package gcp

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/config"
)

func CheckEnv() {
	clusterdeployment.ValidateDeploymentVars([]string{
		clusterdeployment.EnvVarGCPEncodedCredentials,
		clusterdeployment.EnvVarGCPProject,
		clusterdeployment.EnvVarGCPRegion,
	})
}

func PopulateStandaloneEnvVars(conf config.ProviderTestingConfig) {
	GinkgoHelper()

	PopulateEnvVars(conf.Architecture)
	if conf.Hosted != nil {
		// GinkgoT().Setenv(clusterdeployment.EnvVarControlPlaneNumberNumber, "2")
		GinkgoT().Setenv(clusterdeployment.EnvVarWorkersNumber, "2")
	}
}

func PopulateEnvVars(architecture config.Architecture) {
	GinkgoHelper()

	switch architecture {
	case config.ArchitectureAmd64:
		GinkgoT().Setenv(clusterdeployment.EnvVarGCPInstanceType, "n1-standard-2")
		GinkgoT().Setenv(clusterdeployment.EnvVarGCPImage, "projects/ubuntu-os-cloud/global/images/ubuntu-2004-focal-v20250213")
		GinkgoT().Setenv(clusterdeployment.EnvVarGCPRootDeviceType, "pd-standard")
	case config.ArchitectureArm64:
		GinkgoT().Setenv(clusterdeployment.EnvVarGCPInstanceType, "c4a-standard-2")
		GinkgoT().Setenv(clusterdeployment.EnvVarGCPImage, "projects/ubuntu-os-cloud/global/images/ubuntu-2204-jammy-arm64-v20250712")
		GinkgoT().Setenv(clusterdeployment.EnvVarGCPRootDeviceType, "hyperdisk-balanced")
	}
}
