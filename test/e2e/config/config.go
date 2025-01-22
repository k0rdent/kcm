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

package config

import (
	_ "embed"
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

type TestingProvider string

const (
	TestingProviderAWS     TestingProvider = "aws"
	TestingProviderAzure   TestingProvider = "azure"
	TestingProviderVsphere TestingProvider = "vsphere"
	TestingProviderAdopted TestingProvider = "adopted"
)

var (
	//go:embed config.yaml
	configBytes []byte

	Config TestingConfig

	parseOnce sync.Once
	errParse  error
)

type TestingConfig = map[TestingProvider][]ProviderTestingConfig

type ProviderTestingConfig struct {
	// ClusterTestingConfig contains the testing configuration for the cluster deployment.
	ClusterTestingConfig `yaml:",inline"`
	// Hosted contains the testing configuration for the hosted cluster deployment using the previously deployed
	// cluster as a management. If omitted, the hosted cluster deployment will be skipped.
	Hosted *ClusterTestingConfig `yaml:"hosted,omitempty"`
}

type ClusterTestingConfig struct {
	// Upgrade is a boolean parameter that specifies whether the cluster deployment upgrade should be tested.
	Upgrade bool `yaml:"upgrade,omitempty"`
	// Template is the name of the template to use when creating a cluster deployment.
	// If unset:
	// * The latest available template will be chosen
	// * If upgrade is triggered, the latest available template with available upgrades will be chosen.
	Template string `yaml:"template,omitempty"`
	// UpgradeTemplate specifies the name of the template to upgrade to. Ignored if upgrade is set to false.
	// If unset, the latest template available for the upgrade will be chosen.
	UpgradeTemplate string `yaml:"upgradeTemplate,omitempty"`
}

func Parse() error {
	parseOnce.Do(func() {
		err := yaml.Unmarshal(configBytes, &Config)
		if err != nil {
			errParse = fmt.Errorf("failed to decode base64 configuration: %w", err)
			return
		}
		setDefaults()
		_, _ = fmt.Fprintf(GinkgoWriter, "E2e testing configuration:\n%s\n", Show())
	})
	return errParse
}

func Show() string {
	prettyConfig, err := yaml.Marshal(Config)
	Expect(err).NotTo(HaveOccurred())

	return string(prettyConfig)
}

func (c *ProviderTestingConfig) String() string {
	prettyConfig, err := yaml.Marshal(c)
	Expect(err).NotTo(HaveOccurred())

	return string(prettyConfig)
}

func setDefaults() {
	if len(Config) == 0 {
		Config = map[TestingProvider][]ProviderTestingConfig{
			TestingProviderAWS:     {},
			TestingProviderAzure:   {},
			TestingProviderVsphere: {},
			TestingProviderAdopted: {},
		}
	}
	for provider, configs := range Config {
		if len(configs) == 0 {
			Config[provider] = getDefaultTestingConfiguration(provider)
		}
		for i := range Config[provider] {
			c := Config[provider][i]
			if c.Template == "" {
				c.Template = getDefaultTemplate(provider)
			}
			if c.Hosted != nil && c.Hosted.Template == "" {
				c.Hosted.Template = getDefaultHostedTemplate(provider)
			}
			Config[provider][i] = c
		}
	}
}
