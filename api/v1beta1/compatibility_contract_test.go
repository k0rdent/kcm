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

package v1beta1

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestOptionalRootSpecsSerializeSpec(t *testing.T) {
	tests := []struct {
		name   string
		object any
	}{
		{"AccessManagement", AccessManagement{}},
		{"ClusterAuthentication", ClusterAuthentication{}},
		{"ClusterIPAM", ClusterIPAM{}},
		{"ClusterIPAMClaim", ClusterIPAMClaim{}},
		{"ClusterTemplate", ClusterTemplate{}},
		{"ClusterTemplateChain", ClusterTemplateChain{}},
		{"Management", Management{}},
		{"ManagementBackup", ManagementBackup{}},
		{"MultiClusterService", MultiClusterService{}},
		{"ProviderInterface", ProviderInterface{}},
		{"ProviderTemplate", ProviderTemplate{}},
		{"Region", Region{}},
		{"Release", Release{}},
		{"ServiceSet", ServiceSet{}},
		{"ServiceTemplate", ServiceTemplate{}},
		{"ServiceTemplateChain", ServiceTemplateChain{}},
		{"StateManagementProvider", StateManagementProvider{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.object)
			if err != nil {
				t.Fatal(err)
			}

			object := map[string]json.RawMessage{}
			if err := json.Unmarshal(data, &object); err != nil {
				t.Fatal(err)
			}
			if _, ok := object["spec"]; !ok {
				t.Fatalf("serialized %s does not contain spec: %s", test.name, data)
			}
		})
	}
}

func Test_isCAPIContractVersion(t *testing.T) {
	tests := []struct {
		version string
		isValid bool
	}{
		{"v1", true},
		{"v1alpha1", true},
		{"v1beta1", true},
		{"v2", true},
		{"v3alpha2", true},
		{"v33beta22", true},
		{"v1alpha1_v1beta1", true},
		{"v1alpha1v1alha2_v1beta1", false},
		{"v4beta1", true},
		{"", false},
		{"v", false},
		{"invalid", false},
		{"vv1", false},
		{"v1v2", false},
		{"_v1", false},
		{"v1_", false},
		{"v1__v1beta1", false},
		{"v1alpha", false},
		{"v1beta", false},
		{"v1alpha1beta1", false},
		{"vNONSENSEalpha1beta1", false},
		{"v©", false},
	}

	for _, test := range tests {
		result := isCAPIContractVersion(test.version)
		if result != test.isValid {
			t.Errorf("isValidVersion(%q) = %v, want %v", test.version, result, test.isValid)
		}
	}
}

func Example_isNonMajor() {
	_, _ = fmt.Printf("isNonMajor(\"1alpha1\", \"alpha\", 1): %v\n", isNonMajor("1alpha1", "alpha", 1))
	_, _ = fmt.Printf("isNonMajor(\"1beta1\", \"beta\", 1): %v\n", isNonMajor("1beta1", "beta", 1))
	_, _ = fmt.Printf("isNonMajor(\"NONSENSEbeta1\", \"beta\", 8): %v\n", isNonMajor("NONSENSEbeta1", "beta", 8))
	_, _ = fmt.Printf("isNonMajor(\"beta1\", \"beta\", 1): %v\n", isNonMajor("beta1", "beta", 1))

	// Output:
	// isNonMajor("1alpha1", "alpha", 1): true
	// isNonMajor("1beta1", "beta", 1): true
	// isNonMajor("NONSENSEbeta1", "beta", 8): false
	// isNonMajor("beta1", "beta", 1): false
}

func Test_isCAPIContractSingleVersion(t *testing.T) {
	tests := []struct {
		version string
		isValid bool
	}{
		{"v1", true},
		{"v1alpha1", true},
		{"v1beta1", true},
		{"v2", true},
		{"v3alpha2", true},
		{"v33beta22", true},
		{"v4beta1", true},
		{"", false},
		{"v", false},
		{"invalid", false},
		{"vv1", false},
		{"v1v2", false},
		{"v1alpha", false},
		{"v1beta", false},
		{"v1alpha1beta1", false},
		{"v1alpha1_v1beta1", false},
	}

	for _, test := range tests {
		result := isCAPIContractSingleVersion(test.version)
		if result != test.isValid {
			t.Errorf("isValidVersion(%q) = %v, want %v", test.version, result, test.isValid)
		}
	}
}
