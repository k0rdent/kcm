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
	"slices"
	"strings"
	"sync"

	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/K0rdent/kcm/test/e2e/templates"
)

type TestingProvider string

const (
	TestingProviderAWS        TestingProvider = "aws"
	TestingProviderAzure      TestingProvider = "azure"
	TestingProviderGCP        TestingProvider = "gcp"
	TestingProviderOpenstack  TestingProvider = "openstack"
	TestingProviderVsphere    TestingProvider = "vsphere"
	TestingProviderAdopted    TestingProvider = "adopted"
	TestingProviderRemote     TestingProvider = "remote"
	TestingProviderDocker     TestingProvider = "docker"
	TestingProviderMothership TestingProvider = "mothership"
)

type Architecture string

var (
	ArchitectureAmd64 Architecture = "amd64"
	ArchitectureArm64 Architecture = "arm64"
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
	// Architecture defines the target architecture for cluster deployment. Supported values are "amd64"
	// and "arm64".
	Architecture Architecture `yaml:"architecture,omitempty"`
}

func Parse() error {
	parseOnce.Do(func() {
		if len(configBytes) == 0 {
			initialize()
			return
		}

		if err := yaml.Unmarshal(configBytes, &Config); err != nil {
			errParse = fmt.Errorf("failed to decode base64 configuration: %w", err)
			return
		}
		applyDefaultConfiguration()
	})
	return errParse
}

func initialize() {
	providers := []TestingProvider{
		TestingProviderAWS,
		TestingProviderAzure,
		TestingProviderGCP,
		TestingProviderOpenstack,
		TestingProviderVsphere,
		TestingProviderAdopted,
		TestingProviderRemote,
		TestingProviderMothership,
	}

	Config = make(map[TestingProvider][]ProviderTestingConfig)
	for _, provider := range providers {
		Config[provider] = getDefaultTestingConfiguration()
	}
}

func applyDefaultConfiguration() {
	for provider, configs := range Config {
		if len(configs) == 0 {
			Config[provider] = getDefaultTestingConfiguration()
		}
	}
}

func UpgradeRequired() bool {
	for _, configs := range Config {
		for _, config := range configs {
			if config.Upgrade {
				return true
			}
		}
	}
	return false
}

func (c *ProviderTestingConfig) SetDefaults(clusterTemplates []string, provider TestingProvider) {
	if c.Architecture == "" {
		c.Architecture = ArchitectureAmd64
	}
	err := c.SetTemplates(clusterTemplates, getTemplateType(provider))
	Expect(err).NotTo(HaveOccurred())

	if c.Hosted != nil {
		if c.Hosted.Architecture == "" {
			c.Hosted.Architecture = c.Architecture
		}
		err = c.Hosted.SetTemplates(clusterTemplates, getHostedTemplateType(provider))
		Expect(err).NotTo(HaveOccurred())
	}
}

func (c *ProviderTestingConfig) Description() string {
	hostedDesc := "skipped"
	if c.Hosted != nil {
		hostedDesc = strings.ToLower(c.Hosted.description())
	}
	return fmt.Sprintf("%s. Hosted: %s", c.description(), hostedDesc)
}

func (c *ProviderTestingConfig) String() string {
	prettyConfig, err := yaml.Marshal(c)
	Expect(err).NotTo(HaveOccurred())

	return string(prettyConfig)
}

func (c *ClusterTestingConfig) SetTemplates(clusterTemplates []string, templateType templates.Type) error {
	if c.Template != "" && !slices.Contains(clusterTemplates, c.Template) {
		return fmt.Errorf("the ClusterTemplate %s does not exist", c.Template)
	}
	if c.UpgradeTemplate != "" && !slices.Contains(clusterTemplates, c.UpgradeTemplate) {
		return fmt.Errorf("the ClusterTemplate %s does not exist", c.UpgradeTemplate)
	}
	tmpls := templates.FindLatestTemplatesWithType(clusterTemplates, templateType, 2)
	if !c.Upgrade {
		if c.Template == "" {
			if len(tmpls) == 0 {
				return fmt.Errorf("no Template of the %s type was found", templateType)
			}
			c.Template = tmpls[0]
			return nil
		}
		return nil
	}
	if len(tmpls) < 2 {
		return fmt.Errorf("could not find 2 templates with %s type to test upgrade. Found templates: %+v", templateType, tmpls)
	}
	c.Template = tmpls[1]
	c.UpgradeTemplate = tmpls[0]
	return nil
}

func (c *ClusterTestingConfig) description() string {
	upgradeDesc := fmt.Sprintf("upgrade: %t", c.Upgrade)
	if c.Upgrade && c.UpgradeTemplate != "" {
		upgradeDesc += fmt.Sprintf(", upgradeTemplate: %s", c.UpgradeTemplate)
	}
	return fmt.Sprintf("Template: %s, architecture: %s, %s", c.Template, c.Architecture, upgradeDesc)
}
