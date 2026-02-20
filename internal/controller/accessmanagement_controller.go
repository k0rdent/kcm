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

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/record"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	labelsutil "github.com/K0rdent/kcm/internal/util/labels"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
)

// AccessManagementReconciler reconciles an AccessManagement object
type AccessManagementReconciler struct {
	client.Client
	SystemNamespace string
}

type (
	// amSystemResources accessmanagement reconciler specific
	amSystemResources struct {
		ctChains     map[string]templateChain
		stChains     map[string]templateChain
		credentials  map[string]*kcmv1.CredentialSpec
		clusterAuths map[string]*kcmv1.ClusterAuthentication
		dataSources  map[string]*kcmv1.DataSource
		managed      []client.Object
	}

	// amResourceKeeper accessmanagement reconciler specific
	amResourceKeeper struct {
		ctChains     map[string]bool
		stChains     map[string]bool
		credentials  map[string]bool
		clusterAuths map[string]bool
		dataSources  map[string]bool
	}
)

func newResourceKeeper() *amResourceKeeper {
	return &amResourceKeeper{
		ctChains:     make(map[string]bool),
		stChains:     make(map[string]bool),
		credentials:  make(map[string]bool),
		clusterAuths: make(map[string]bool),
		dataSources:  make(map[string]bool),
	}
}

func (k *amResourceKeeper) shouldKeepResource(obj client.Object) bool {
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	namespacedName := getNamespacedName(obj.GetNamespace(), obj.GetName())

	switch kind {
	case kcmv1.ClusterTemplateChainKind:
		return k.ctChains[namespacedName]
	case kcmv1.ServiceTemplateChainKind:
		return k.stChains[namespacedName]
	case kcmv1.CredentialKind:
		return k.credentials[namespacedName]
	case kcmv1.ClusterAuthenticationKind:
		return k.clusterAuths[namespacedName]
	case kcmv1.DataSourceKind:
		return k.dataSources[namespacedName]
	default:
		return false
	}
}

func (r *AccessManagementReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling AccessManagement")

	management := &kcmv1.Management{}
	if err := r.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, management); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Management: %w", err)
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping AccessManagement reconciliation")
		return ctrl.Result{}, nil
	}

	accessMgmt := &kcmv1.AccessManagement{}
	if err := r.Get(ctx, req.NamespacedName, accessMgmt); err != nil {
		l.Error(err, "unable to fetch AccessManagement")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if updated, err := labelsutil.AddKCMComponentLabel(ctx, r.Client, accessMgmt); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{}, err
	}

	errMsg := ""
	err := r.reconcileObj(ctx, accessMgmt)
	if err != nil {
		errMsg = err.Error()
	}
	accessMgmt.Status.ObservedGeneration = accessMgmt.Generation
	accessMgmt.Status.Error = errMsg

	return ctrl.Result{}, errors.Join(err, r.updateStatus(ctx, accessMgmt))
}

func (r *AccessManagementReconciler) reconcileObj(ctx context.Context, accessMgmt *kcmv1.AccessManagement) error {
	resources, err := r.collectSystemResources(ctx)
	if err != nil {
		return err
	}

	keeper := newResourceKeeper()

	var errs error
	for _, rule := range accessMgmt.Spec.AccessRules {
		namespaces, err := getTargetNamespaces(ctx, r.Client, rule.TargetNamespaces)
		if err != nil {
			return fmt.Errorf("failed to collect target namespaces: %w", err)
		}

		for _, targetNamespace := range namespaces {
			if err := r.processRuleResources(ctx, accessMgmt, rule, targetNamespace, resources, keeper); err != nil {
				errs = errors.Join(errs, err)
			}
		}
	}

	if err := r.cleanupManagedResources(ctx, accessMgmt, resources.managed, keeper); err != nil {
		errs = errors.Join(errs, err)
	}

	if errs != nil {
		return errs
	}

	accessMgmt.Status.Current = accessMgmt.Spec.AccessRules
	return nil
}

func (r *AccessManagementReconciler) collectSystemResources(ctx context.Context) (*amSystemResources, error) {
	systemCtChains, managedCtChains, err := r.getCurrentTemplateChains(ctx, kcmv1.ClusterTemplateChainKind)
	if err != nil {
		return nil, fmt.Errorf("failed to collect ClusterTemplateChains: %w", err)
	}

	systemStChains, managedStChains, err := r.getCurrentTemplateChains(ctx, kcmv1.ServiceTemplateChainKind)
	if err != nil {
		return nil, fmt.Errorf("failed to collect ServiceTemplateChains: %w", err)
	}

	systemCredentials, managedCredentials, err := r.getCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to collect Credentials: %w", err)
	}

	systemClusterAuths, managedClusterAuths, err := r.getClusterAuths(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to collect ClusterAuthentications: %w", err)
	}

	systemDataSources, managedDataSources, err := r.getDataSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to collect DataSources: %w", err)
	}

	return &amSystemResources{
		ctChains:     systemCtChains,
		stChains:     systemStChains,
		credentials:  systemCredentials,
		clusterAuths: systemClusterAuths,
		dataSources:  systemDataSources,
		managed:      slices.Concat(managedCtChains, managedStChains, managedCredentials, managedClusterAuths, managedDataSources),
	}, nil
}

func (r *AccessManagementReconciler) processRuleResources(ctx context.Context, accessMgmt *kcmv1.AccessManagement, rule kcmv1.AccessRule, targetNamespace string, resources *amSystemResources, keeper *amResourceKeeper) error {
	var errs error

	if err := r.processTemplateChains(ctx, accessMgmt, rule.ClusterTemplateChains, targetNamespace, resources.ctChains, keeper.ctChains, kcmv1.ClusterTemplateChainKind); err != nil {
		errs = errors.Join(errs, fmt.Errorf("failed to process ClusterTemplateChains: %w", err))
	}

	if err := r.processTemplateChains(ctx, accessMgmt, rule.ServiceTemplateChains, targetNamespace, resources.stChains, keeper.stChains, kcmv1.ServiceTemplateChainKind); err != nil {
		errs = errors.Join(errs, fmt.Errorf("failed to process ServiceTemplateChains: %w", err))
	}

	if err := r.processCredentials(ctx, accessMgmt, rule.Credentials, targetNamespace, resources.credentials, keeper.credentials); err != nil {
		errs = errors.Join(errs, fmt.Errorf("failed to process Credentials: %w", err))
	}

	if err := r.processClusterAuths(ctx, accessMgmt, rule.ClusterAuthentications, targetNamespace, resources.clusterAuths, keeper.clusterAuths); err != nil {
		errs = errors.Join(errs, fmt.Errorf("failed to process ClusterAuthentications: %w", err))
	}

	if err := r.processDataSources(ctx, accessMgmt, rule.DataSources, targetNamespace, resources.dataSources, keeper.dataSources); err != nil {
		errs = errors.Join(errs, fmt.Errorf("failed to process DataSources: %w", err))
	}

	return errs
}

func (r *AccessManagementReconciler) processTemplateChains(ctx context.Context, accessMgmt *kcmv1.AccessManagement, chains []string, targetNamespace string, systemChains map[string]templateChain, keepMap map[string]bool, kind string) error {
	var errs error
	for _, chain := range chains {
		namespacedName := getNamespacedName(targetNamespace, chain)
		keepMap[namespacedName] = true

		if systemChains[chain] == nil {
			errs = errors.Join(errs, fmt.Errorf("%s %s/%s is not found", kind, r.SystemNamespace, chain))
			continue
		}

		created, err := r.createTemplateChain(ctx, systemChains[chain], targetNamespace)
		if err != nil {
			r.warnf(accessMgmt, kind+"CreationFailed", "Failed to create %s %s/%s: %v", kind, targetNamespace, chain, err)
			errs = errors.Join(errs, err)
			continue
		}

		if created {
			r.eventf(accessMgmt, kind+"Created", "Successfully created %s %s/%s", kind, targetNamespace, chain)
		}
	}

	return errs
}

func (r *AccessManagementReconciler) processCredentials(ctx context.Context, accessMgmt *kcmv1.AccessManagement, credentials []string, targetNamespace string, systemCredentials map[string]*kcmv1.CredentialSpec, keepMap map[string]bool) error {
	var errs error
	for _, credentialName := range credentials {
		namespacedName := getNamespacedName(targetNamespace, credentialName)
		keepMap[namespacedName] = true

		if systemCredentials[credentialName] == nil {
			errs = errors.Join(errs, fmt.Errorf("credential %s/%s is not found", r.SystemNamespace, credentialName))
			continue
		}

		created, err := r.createCredential(ctx, targetNamespace, credentialName, systemCredentials[credentialName])
		if err != nil {
			r.warnf(accessMgmt, "CredentialCreationFailed", "Failed to create Credential %s/%s: %v", targetNamespace, credentialName, err)
			errs = errors.Join(errs, err)
			continue
		}
		if created {
			r.eventf(accessMgmt, "CredentialCreated", "Successfully created Credential %s/%s", targetNamespace, credentialName)
		}
	}
	return errs
}

func (r *AccessManagementReconciler) processClusterAuths(ctx context.Context, accessMgmt *kcmv1.AccessManagement, clusterAuths []string, targetNamespace string, systemClusterAuths map[string]*kcmv1.ClusterAuthentication, keepMap map[string]bool) error {
	var errs error
	for _, clAuthName := range clusterAuths {
		namespacedName := getNamespacedName(targetNamespace, clAuthName)
		keepMap[namespacedName] = true

		if systemClusterAuths[clAuthName] == nil {
			errs = errors.Join(errs, fmt.Errorf("ClusterAuthentication %s/%s is not found", r.SystemNamespace, clAuthName))
			continue
		}

		created, err := r.createClusterAuth(ctx, targetNamespace, clAuthName, systemClusterAuths[clAuthName])
		if err != nil {
			r.warnf(accessMgmt, "ClusterAuthenticationCreationFailed", "Failed to create ClusterAuthentication %s/%s: %v", targetNamespace, clAuthName, err)
			errs = errors.Join(errs, err)
			continue
		}

		if created {
			r.eventf(accessMgmt, "ClusterAuthenticationCreated", "Successfully created ClusterAuthentication %s/%s", targetNamespace, clAuthName)
		}
	}
	return errs
}

func (r *AccessManagementReconciler) processDataSources(ctx context.Context, accessMgmt *kcmv1.AccessManagement, dataSources []string, targetNamespace string, systemDataSources map[string]*kcmv1.DataSource, keepMap map[string]bool) error {
	var errs error
	for _, dsName := range dataSources {
		namespacedName := getNamespacedName(targetNamespace, dsName)
		keepMap[namespacedName] = true

		if systemDataSources[dsName] == nil {
			errs = errors.Join(errs, fmt.Errorf("DataSource %s/%s is not found", r.SystemNamespace, dsName))
			continue
		}

		created, err := r.createDataSource(ctx, targetNamespace, dsName, systemDataSources[dsName])
		if err != nil {
			r.warnf(accessMgmt, "DataSourceCreationFailed", "Failed to create DataSource %s/%s: %v", targetNamespace, dsName, err)
			errs = errors.Join(errs, err)
			continue
		}

		if created {
			r.eventf(accessMgmt, "DataSourceCreated", "Successfully created DataSource %s/%s", targetNamespace, dsName)
		}
	}
	return errs
}

func (r *AccessManagementReconciler) cleanupManagedResources(ctx context.Context, accessMgmt *kcmv1.AccessManagement, managedObjects []client.Object, keeper *amResourceKeeper) error {
	var errs error
	for _, managedObject := range managedObjects {
		if keeper.shouldKeepResource(managedObject) {
			continue
		}

		kind := managedObject.GetObjectKind().GroupVersionKind().Kind
		namespacedName := getNamespacedName(managedObject.GetNamespace(), managedObject.GetName())

		deleted, err := r.deleteManagedObject(ctx, managedObject)
		if err != nil {
			r.warnf(accessMgmt, kind+"DeletionFailed", "Failed to delete %s %s: %v", kind, namespacedName, err)
			errs = errors.Join(errs, err)
			continue
		}

		if deleted {
			r.eventf(accessMgmt, kind+"Deleted", "Successfully deleted %s %s", kind, namespacedName)
		}
	}
	return errs
}

func getNamespacedName(namespace, name string) string {
	return namespace + "/" + name
}

func (r *AccessManagementReconciler) getCurrentTemplateChains(ctx context.Context, templateChainKind string) (map[string]templateChain, []client.Object, error) {
	var templateChains []templateChain
	switch templateChainKind {
	case kcmv1.ClusterTemplateChainKind:
		ctChainList := &kcmv1.ClusterTemplateChainList{}
		err := r.List(ctx, ctChainList)
		if err != nil {
			return nil, nil, err
		}
		for _, chain := range ctChainList.Items {
			templateChains = append(templateChains, &chain)
		}
	case kcmv1.ServiceTemplateChainKind:
		stChainList := &kcmv1.ServiceTemplateChainList{}
		err := r.List(ctx, stChainList)
		if err != nil {
			return nil, nil, err
		}
		for _, chain := range stChainList.Items {
			templateChains = append(templateChains, &chain)
		}
	default:
		return nil, nil, fmt.Errorf("invalid TemplateChain kind. Supported kinds are %s and %s", kcmv1.ClusterTemplateChainKind, kcmv1.ServiceTemplateChainKind)
	}

	var (
		systemTemplateChains  = make(map[string]templateChain, len(templateChains))
		managedTemplateChains = make([]client.Object, 0, len(templateChains))
	)
	for _, chain := range templateChains {
		if chain.GetNamespace() == r.SystemNamespace {
			systemTemplateChains[chain.GetName()] = chain
			continue
		}

		if chain.GetLabels()[kcmv1.KCMManagedLabelKey] == kcmv1.KCMManagedLabelValue {
			managedTemplateChains = append(managedTemplateChains, chain)
		}
	}

	return systemTemplateChains, managedTemplateChains, nil
}

func (r *AccessManagementReconciler) getCredentials(ctx context.Context) (map[string]*kcmv1.CredentialSpec, []client.Object, error) {
	credentialList := &kcmv1.CredentialList{}
	err := r.List(ctx, credentialList)
	if err != nil {
		return nil, nil, err
	}
	var (
		systemCredentials  = make(map[string]*kcmv1.CredentialSpec, len(credentialList.Items))
		managedCredentials = make([]client.Object, 0, len(credentialList.Items))
	)
	for _, cred := range credentialList.Items {
		if cred.Namespace == r.SystemNamespace {
			systemCredentials[cred.Name] = &cred.Spec
			continue
		}

		if cred.GetLabels()[kcmv1.KCMManagedLabelKey] == kcmv1.KCMManagedLabelValue {
			managedCredentials = append(managedCredentials, &cred)
		}
	}
	return systemCredentials, managedCredentials, nil
}

func (r *AccessManagementReconciler) getClusterAuths(ctx context.Context) (map[string]*kcmv1.ClusterAuthentication, []client.Object, error) {
	clAuthsList := &kcmv1.ClusterAuthenticationList{}
	err := r.List(ctx, clAuthsList)
	if err != nil {
		return nil, nil, err
	}
	var (
		systemClusterAuths  = make(map[string]*kcmv1.ClusterAuthentication, len(clAuthsList.Items))
		managedClusterAuths = make([]client.Object, 0, len(clAuthsList.Items))
	)
	for _, clAuth := range clAuthsList.Items {
		if clAuth.Namespace == r.SystemNamespace {
			systemClusterAuths[clAuth.Name] = &clAuth
			continue
		}

		if clAuth.GetLabels()[kcmv1.KCMManagedLabelKey] == kcmv1.KCMManagedLabelValue {
			managedClusterAuths = append(managedClusterAuths, &clAuth)
		}
	}
	return systemClusterAuths, managedClusterAuths, nil
}

func (r *AccessManagementReconciler) getDataSources(ctx context.Context) (map[string]*kcmv1.DataSource, []client.Object, error) {
	datasources := &kcmv1.DataSourceList{}
	if err := r.List(ctx, datasources); err != nil {
		return nil, nil, err
	}

	var (
		systemDataSources  = make(map[string]*kcmv1.DataSource, len(datasources.Items))
		managedDataSources = make([]client.Object, 0, len(datasources.Items))
	)
	for _, ds := range datasources.Items {
		if ds.Namespace == r.SystemNamespace {
			systemDataSources[ds.Name] = &ds
			continue
		}

		if ds.GetLabels()[kcmv1.KCMManagedLabelKey] == kcmv1.KCMManagedLabelValue {
			managedDataSources = append(managedDataSources, &ds)
		}
	}

	return systemDataSources, managedDataSources, nil
}

func getTargetNamespaces(ctx context.Context, cl client.Client, targetNamespaces kcmv1.TargetNamespaces) ([]string, error) {
	if len(targetNamespaces.List) > 0 {
		return targetNamespaces.List, nil
	}
	var selector labels.Selector
	var err error
	if targetNamespaces.StringSelector != "" {
		selector, err = labels.Parse(targetNamespaces.StringSelector)
		if err != nil {
			return nil, err
		}
	} else {
		selector, err = metav1.LabelSelectorAsSelector(targetNamespaces.Selector)
		if err != nil {
			return nil, fmt.Errorf("failed to construct selector from the namespaces selector %s: %w", targetNamespaces.Selector, err)
		}
	}

	var (
		namespaces = new(corev1.NamespaceList)
		listOpts   = new(client.ListOptions)
	)
	if !selector.Empty() {
		listOpts.LabelSelector = selector
	}

	if err := cl.List(ctx, namespaces, listOpts); err != nil {
		return nil, err
	}

	result := make([]string, len(namespaces.Items))
	for i, ns := range namespaces.Items {
		result[i] = ns.Name
	}

	return result, nil
}

func (r *AccessManagementReconciler) createTemplateChain(ctx context.Context, source templateChain, targetNamespace string) (created bool, _ error) {
	l := ctrl.LoggerFrom(ctx)

	if err := kubeutil.EnsureNamespace(ctx, r.Client, targetNamespace); err != nil {
		return false, fmt.Errorf("failed to ensure namespace %s: %w", targetNamespace, err)
	}

	meta := metav1.ObjectMeta{
		Name:      source.GetName(),
		Namespace: targetNamespace,
		Labels: map[string]string{
			kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
		},
	}
	var target templateChain
	kind := source.GetObjectKind().GroupVersionKind().Kind
	switch kind {
	case kcmv1.ClusterTemplateChainKind:
		target = &kcmv1.ClusterTemplateChain{ObjectMeta: meta, Spec: *source.GetSpec()}
	case kcmv1.ServiceTemplateChainKind:
		target = &kcmv1.ServiceTemplateChain{ObjectMeta: meta, Spec: *source.GetSpec()}
	}

	if err := r.Create(ctx, target); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, err
	}
	l.Info(kind+" was successfully created", "target namespace", targetNamespace, "source name", source.GetName())
	return true, nil
}

func (r *AccessManagementReconciler) createCredential(ctx context.Context, namespace, name string, spec *kcmv1.CredentialSpec) (created bool, _ error) {
	l := ctrl.LoggerFrom(ctx)

	if err := kubeutil.EnsureNamespace(ctx, r.Client, namespace); err != nil {
		return false, fmt.Errorf("failed to ensure namespace %s: %w", namespace, err)
	}

	newSpec := spec.DeepCopy()
	if spec.IdentityRef.Namespace != "" {
		newSpec.IdentityRef.Namespace = namespace
	}

	target := &kcmv1.Credential{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
			},
		},
		Spec: *newSpec,
	}
	if err := r.Create(ctx, target); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, err
	}
	l.Info("Credential was successfully created", "namespace", namespace, "name", name)
	return true, nil
}

func (r *AccessManagementReconciler) createClusterAuth(ctx context.Context, namespace, name string, clAuth *kcmv1.ClusterAuthentication) (created bool, _ error) {
	l := ctrl.LoggerFrom(ctx)

	if err := kubeutil.EnsureNamespace(ctx, r.Client, namespace); err != nil {
		return false, fmt.Errorf("failed to ensure namespace %s: %w", namespace, err)
	}

	newSpec := clAuth.Spec.DeepCopy()
	// when the CA Secret namespace is not specified, the Secret is expected to exist in the same
	// namespace as the ClusterAuthentication resource. If the ClusterAuthentication resource is
	// copied to another namespace, it continues to reference the same Secret.
	if clAuth.Spec.CASecret != nil && clAuth.Spec.CASecret.Namespace == "" {
		newSpec.CASecret.Namespace = clAuth.Namespace
	}

	target := &kcmv1.ClusterAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
			},
		},
		Spec: *newSpec,
	}
	if err := r.Create(ctx, target); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, err
	}
	l.Info("ClusterAuthentication was successfully created", "namespace", namespace, "name", name)
	return true, nil
}

func (r *AccessManagementReconciler) createDataSource(ctx context.Context, targetNamespace, name string, source *kcmv1.DataSource) (created bool, _ error) {
	l := ctrl.LoggerFrom(ctx).WithName("datasources")

	if err := kubeutil.EnsureNamespace(ctx, r.Client, targetNamespace); err != nil {
		return false, fmt.Errorf("failed to ensure namespace %s: %w", targetNamespace, err)
	}

	targetSpec := source.Spec.DeepCopy()

	// when the CA Secret is specified, the target DataSource should have the Secret
	// as local reference, so the Secret should also be copied in the target namespace
	if source.Spec.CertificateAuthority != nil && source.Spec.CertificateAuthority.Namespace == "" {
		targetSpec.CertificateAuthority.Namespace = source.Namespace
	}

	target := &kcmv1.DataSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: targetNamespace,
			Labels: map[string]string{
				kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
			},
		},
		Spec: *targetSpec,
	}

	if err := r.Create(ctx, target); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, err
	}

	l.Info("DataSource was successfully created", "namespace", targetNamespace, "name", name)

	return true, nil
}

func (r *AccessManagementReconciler) deleteManagedObject(ctx context.Context, obj client.Object) (deleted bool, _ error) {
	l := ctrl.LoggerFrom(ctx)

	if err := r.Delete(ctx, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	l.Info(obj.GetObjectKind().GroupVersionKind().Kind+" was successfully deleted", "namespace", obj.GetNamespace(), "name", obj.GetName())
	return true, nil
}

func (r *AccessManagementReconciler) updateStatus(ctx context.Context, accessMgmt *kcmv1.AccessManagement) error {
	if err := r.Status().Update(ctx, accessMgmt); err != nil {
		return fmt.Errorf("failed to update status for AccessManagement %s: %w", accessMgmt.Name, err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AccessManagementReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.AccessManagement{}).
		Complete(r)
}

// TODO: FIXME: pass meaningful non-empty action
func (*AccessManagementReconciler) eventf(am *kcmv1.AccessManagement, reason, message string, args ...any) {
	record.Eventf(am, nil, reason, "Reconcile", message, args...)
}

// TODO: FIXME: pass meaningful non-empty action
func (*AccessManagementReconciler) warnf(am *kcmv1.AccessManagement, reason, message string, args ...any) {
	record.Warnf(am, nil, reason, "Reconcile", message, args...)
}
