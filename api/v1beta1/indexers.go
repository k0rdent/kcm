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
	"context"
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func SetupIndexers(ctx context.Context, mgr ctrl.Manager) error {
	var merr error
	for _, f := range []func(context.Context, ctrl.Manager) error{
		setupClusterDeploymentIndexer,
		setupClusterDeploymentServicesIndexer,
		setupClusterDeploymentServiceTemplateChainIndexer,
		setupClusterDeploymentCredentialIndexer,
		setupClusterDeploymentAuthenticationIndexer,
		setupReleaseVersionIndexer,
		setupReleaseTemplatesIndexer,
		setupClusterTemplateChainIndexer,
		setupServiceTemplateChainIndexer,
		setupClusterTemplateProvidersIndexer,
		setupMultiClusterServiceServicesIndexer,
		setupMultiClusterServiceTemplateChainIndexer,
		setupOwnerReferenceIndexers,
		setupManagementBackupIndexer,
		setupManagementBackupAutoUpgradesIndexer,
		setupServiceSetClusterIndexer,
		setupServiceSetMultiClusterServiceIndexer,
		setupServiceSetProviderIndexer,
		setupCredentialRegionIndexer,
	} {
		merr = errors.Join(merr, f(ctx, mgr))
	}

	return merr
}

// cluster deployment

// ClusterDeploymentTemplateIndexKey indexer field name to extract ClusterTemplate name reference from a ClusterDeployment object.
const ClusterDeploymentTemplateIndexKey = ".spec.template"

func setupClusterDeploymentIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ClusterDeployment{}, ClusterDeploymentTemplateIndexKey, ExtractTemplateNameFromClusterDeployment)
}

// ExtractTemplateNameFromClusterDeployment returns referenced ClusterTemplate name
// declared in a ClusterDeployment object.
func ExtractTemplateNameFromClusterDeployment(rawObj client.Object) []string {
	cluster, ok := rawObj.(*ClusterDeployment)
	if !ok {
		return nil
	}

	return []string{cluster.Spec.Template}
}

// ClusterDeploymentServiceTemplatesIndexKey indexer field name to extract service templates names from a ClusterDeployment object.
const ClusterDeploymentServiceTemplatesIndexKey = ".spec.services[].Template"

func setupClusterDeploymentServicesIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ClusterDeployment{}, ClusterDeploymentServiceTemplatesIndexKey, ExtractServiceTemplateNamesFromClusterDeployment)
}

// ExtractServiceTemplateNamesFromClusterDeployment returns a list of service templates names
// declared in a ClusterDeployment object.
func ExtractServiceTemplateNamesFromClusterDeployment(rawObj client.Object) []string {
	cluster, ok := rawObj.(*ClusterDeployment)
	if !ok {
		return nil
	}

	templates := []string{}
	for _, s := range cluster.Spec.ServiceSpec.Services {
		templates = append(templates, s.Template)
	}

	return templates
}

// ClusterDeploymentServiceTemplateChainIndexKey indexer field name to extract service template chain name from a ClusterDeployment object.
const ClusterDeploymentServiceTemplateChainIndexKey = ".spec.serviceSpec.services[].templateChain"

func setupClusterDeploymentServiceTemplateChainIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ClusterDeployment{}, ClusterDeploymentServiceTemplateChainIndexKey, ExtractServiceTemplateChainNameFromClusterDeployment)
}

// ExtractServiceTemplateChainNameFromClusterDeployment returns a list of service template chain names
// declared in a ClusterDeployment object.
func ExtractServiceTemplateChainNameFromClusterDeployment(rawObj client.Object) []string {
	cluster, ok := rawObj.(*ClusterDeployment)
	if !ok {
		return nil
	}

	templateChains := []string{}
	for _, s := range cluster.Spec.ServiceSpec.Services {
		if s.TemplateChain == "" {
			continue
		}
		templateChains = append(templateChains, s.TemplateChain)
	}

	return templateChains
}

// ClusterDeploymentCredentialIndexKey indexer field name to extract Credential name reference from a ClusterDeployment object.
const ClusterDeploymentCredentialIndexKey = ".spec.credential"

func setupClusterDeploymentCredentialIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ClusterDeployment{}, ClusterDeploymentCredentialIndexKey, ExtractCredentialNameFromClusterDeployment)
}

// ExtractCredentialNameFromClusterDeployment returns referenced Credential name
// declared in a ClusterDeployment object.
func ExtractCredentialNameFromClusterDeployment(rawObj client.Object) []string {
	cluster, ok := rawObj.(*ClusterDeployment)
	if !ok {
		return nil
	}

	return []string{cluster.Spec.Credential}
}

// ClusterDeploymentAuthenticationIndexKey indexer field name to extract ClusterAuthentication name reference from a ClusterDeployment object.
const ClusterDeploymentAuthenticationIndexKey = ".spec.authentication"

func setupClusterDeploymentAuthenticationIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ClusterDeployment{}, ClusterDeploymentAuthenticationIndexKey, extractClusterAuthenticationNameFromClusterDeployment)
}

// extractClusterAuthenticationNameFromClusterDeployment returns referenced ClusterAuthentication name
// declared in a ClusterDeployment object.
func extractClusterAuthenticationNameFromClusterDeployment(rawObj client.Object) []string {
	cluster, ok := rawObj.(*ClusterDeployment)
	if !ok {
		return nil
	}

	return []string{cluster.Spec.ClusterAuth}
}

// release

// ReleaseVersionIndexKey indexer field name to extract release version from a Release object.
const ReleaseVersionIndexKey = ".spec.version"

func setupReleaseVersionIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &Release{}, ReleaseVersionIndexKey, extractReleaseVersion)
}

func extractReleaseVersion(rawObj client.Object) []string {
	release, ok := rawObj.(*Release)
	if !ok {
		return nil
	}
	return []string{release.Spec.Version}
}

// ReleaseTemplatesIndexKey indexer field name to extract component template names from a Release object.
const ReleaseTemplatesIndexKey = "releaseTemplates"

func setupReleaseTemplatesIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &Release{}, ReleaseTemplatesIndexKey, extractReleaseTemplates)
}

func extractReleaseTemplates(rawObj client.Object) []string {
	release, ok := rawObj.(*Release)
	if !ok {
		return nil
	}

	return release.Templates()
}

// template chains

// TemplateChainSupportedTemplatesIndexKey indexer field name to extract supported template names from an according TemplateChain object.
const TemplateChainSupportedTemplatesIndexKey = ".spec.supportedTemplates[].Name"

func setupClusterTemplateChainIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ClusterTemplateChain{}, TemplateChainSupportedTemplatesIndexKey, extractSupportedTemplatesNames)
}

func setupServiceTemplateChainIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ServiceTemplateChain{}, TemplateChainSupportedTemplatesIndexKey, extractSupportedTemplatesNames)
}

func extractSupportedTemplatesNames(rawObj client.Object) []string {
	chainSpec := TemplateChainSpec{}
	switch chain := rawObj.(type) {
	case *ClusterTemplateChain:
		chainSpec = chain.Spec
	case *ServiceTemplateChain:
		chainSpec = chain.Spec
	default:
		return nil
	}

	supportedTemplates := make([]string, 0, len(chainSpec.SupportedTemplates))
	for _, t := range chainSpec.SupportedTemplates {
		supportedTemplates = append(supportedTemplates, t.Name)
	}

	return supportedTemplates
}

// cluster template

// ClusterTemplateProvidersIndexKey indexer field name to extract provider names from a ClusterTemplate object.
const ClusterTemplateProvidersIndexKey = "clusterTemplateProviders"

func setupClusterTemplateProvidersIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ClusterTemplate{}, ClusterTemplateProvidersIndexKey, ExtractProvidersFromClusterTemplate)
}

// ExtractProvidersFromClusterTemplate returns provider names from a ClusterTemplate object.
func ExtractProvidersFromClusterTemplate(o client.Object) []string {
	ct, ok := o.(*ClusterTemplate)
	if !ok {
		return nil
	}

	return ct.Status.Providers
}

// multicluster service

// MultiClusterServiceTemplatesIndexKey indexer field name to extract service templates names from a MultiClusterService object.
const MultiClusterServiceTemplatesIndexKey = "serviceTemplates"

func setupMultiClusterServiceServicesIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &MultiClusterService{}, MultiClusterServiceTemplatesIndexKey, ExtractServiceTemplateNamesFromMultiClusterService)
}

// ExtractServiceTemplateNamesFromMultiClusterService returns a list of service templates names
// declared in a MultiClusterService object.
func ExtractServiceTemplateNamesFromMultiClusterService(rawObj client.Object) []string {
	mcs, ok := rawObj.(*MultiClusterService)
	if !ok {
		return nil
	}

	templates := make([]string, len(mcs.Spec.ServiceSpec.Services))
	for i, s := range mcs.Spec.ServiceSpec.Services {
		templates[i] = s.Template
	}

	return templates
}

// MultiClusterServiceTemplateChainIndexKey indexer field name to extract template chain names from a MultiClusterService object.
const MultiClusterServiceTemplateChainIndexKey = ".spec.serviceSpec.services[].templateChain"

func setupMultiClusterServiceTemplateChainIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &MultiClusterService{}, MultiClusterServiceTemplateChainIndexKey, ExtractServiceTemplateChainNamesFromMultiClusterService)
}

// ExtractServiceTemplateChainNamesFromMultiClusterService returns a list of template chain names
// declared in a MultiClusterService object.
func ExtractServiceTemplateChainNamesFromMultiClusterService(rawObj client.Object) []string {
	mcs, ok := rawObj.(*MultiClusterService)
	if !ok {
		return nil
	}

	templateChains := []string{}
	for _, s := range mcs.Spec.ServiceSpec.Services {
		if s.TemplateChain == "" {
			continue
		}
		templateChains = append(templateChains, s.TemplateChain)
	}

	return templateChains
}

// ownerref indexers

// OwnerRefIndexKey indexer field name to extract ownerReference names from objects
const OwnerRefIndexKey = ".metadata.ownerReferences"

func setupOwnerReferenceIndexers(ctx context.Context, mgr ctrl.Manager) error {
	var merr error
	for _, obj := range []client.Object{
		&ProviderTemplate{},
		&ServiceSet{},
	} {
		merr = errors.Join(merr, mgr.GetFieldIndexer().IndexField(ctx, obj, OwnerRefIndexKey, extractOwnerReferences))
	}

	return merr
}

// extractOwnerReferences returns a list of ownerReference names
func extractOwnerReferences(rawObj client.Object) []string {
	ownerRefs := rawObj.GetOwnerReferences()
	owners := make([]string, 0, len(ownerRefs))
	for _, ref := range ownerRefs {
		owners = append(owners, ref.Name)
	}
	return owners
}

// management backup indexers

// ManagementBackupIndexKey indexer field name to extract only [ManagementBackup] objects
// that either has schedule or has NOT been completed yet.
const ManagementBackupIndexKey = "k0rdent.management-backup"

func setupManagementBackupIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ManagementBackup{}, ManagementBackupIndexKey, ExtractScheduledOrIncompleteBackups)
}

// ExtractScheduledOrIncompleteBackups returns either scheduled or incomplete backups.
func ExtractScheduledOrIncompleteBackups(o client.Object) []string {
	mb, ok := o.(*ManagementBackup)
	if !ok {
		return nil
	}

	if mb.Spec.Schedule != "" || !mb.IsCompleted() {
		return []string{"true"}
	}

	return nil
}

// ManagementBackupAutoUpgradeIndexKey indexer field name to extract only [ManagementBackup] objects
// with schedule and auto-upgrade set.
const ManagementBackupAutoUpgradeIndexKey = "k0rdent.management-backup-upgrades"

func setupManagementBackupAutoUpgradesIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ManagementBackup{}, ManagementBackupAutoUpgradeIndexKey, func(o client.Object) []string {
		mb, ok := o.(*ManagementBackup)
		if !ok || mb.Spec.Schedule == "" || !mb.Spec.PerformOnManagementUpgrade {
			return nil
		}

		return []string{"true"}
	})
}

// service set indexers

// ServiceSetClusterIndexKey indexer field name to extract cluster name from [ServiceSet] object.
const ServiceSetClusterIndexKey = "k0rdent.service-set.cluster"

func setupServiceSetClusterIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ServiceSet{}, ServiceSetClusterIndexKey, ExtractServiceSetCluster)
}

// ExtractServiceSetCluster returns the cluster name from [ServiceSet] object.
func ExtractServiceSetCluster(o client.Object) []string {
	serviceSet, ok := o.(*ServiceSet)
	if !ok {
		return nil
	}
	return []string{serviceSet.Spec.Cluster}
}

// ServiceSetMultiClusterServiceIndexKey indexer field name to extract multi-cluster-service from [ServiceSet] object.
const ServiceSetMultiClusterServiceIndexKey = "k0rdent.service-set.multi-cluster-service"

func setupServiceSetMultiClusterServiceIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ServiceSet{}, ServiceSetMultiClusterServiceIndexKey, ExtractServiceSetMultiClusterService)
}

// ExtractServiceSetMultiClusterService returns the multi-cluster-service from [ServiceSet] object.
func ExtractServiceSetMultiClusterService(o client.Object) []string {
	serviceSet, ok := o.(*ServiceSet)
	if !ok {
		return nil
	}
	if serviceSet.Spec.MultiClusterService == "" {
		return nil
	}
	return []string{serviceSet.Spec.MultiClusterService}
}

// ServiceSetProviderIndexKey indexer field name to extract provider name from [ServiceSet] object.
const ServiceSetProviderIndexKey = "k0rdent.service-set.provider"

func setupServiceSetProviderIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &ServiceSet{}, ServiceSetProviderIndexKey, ExtractServiceSetProvider)
}

// ExtractServiceSetProvider returns the provider name from [ServiceSet] object.
func ExtractServiceSetProvider(o client.Object) []string {
	serviceSet, ok := o.(*ServiceSet)
	if !ok {
		return nil
	}
	return []string{serviceSet.Spec.Provider.Name}
}

// credential indexers

// CredentialRegionIndexKey indexer field name to extract region name from [Credential] object.
const CredentialRegionIndexKey = "spec.region"

func setupCredentialRegionIndexer(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &Credential{}, CredentialRegionIndexKey, ExtractCredentialRegion)
}

// ExtractCredentialRegion returns the region name from [Credential] object.
func ExtractCredentialRegion(o client.Object) []string {
	cred, ok := o.(*Credential)
	if !ok {
		return nil
	}
	return []string{cred.Spec.Region}
}
