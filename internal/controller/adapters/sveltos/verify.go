// Copyright 2026
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

package sveltos

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	"github.com/projectsveltos/addon-controller/lib/clusterops"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	// serviceHealthConditionPrefix marks conditions owned by the verifier so
	// they can be replaced atomically without disturbing conditions written by
	// other parts of the controller.
	serviceHealthConditionPrefix = "ServiceHealth"

	// healthRuleTargetLabel selects ConfigMaps whose `rules` key contains
	// CEL health-rule documents to be loaded by the verifier.
	//
	//   value == "global"        → applies to every ServiceSet
	//                              (cluster-global when CM is in SystemNamespace,
	//                              namespace-global when CM is in the SS's
	//                              own namespace)
	//   value == <serviceSet>    → applies only to the named ServiceSet
	//                              (CM must live in that ServiceSet's namespace)
	healthRuleTargetLabel = "k0rdent.mirantis.com/health-rule-target"

	// healthRuleTargetGlobal is the label value selecting the "global" tier.
	healthRuleTargetGlobal = "global"

	// healthRuleConfigMapDataKey is the key within a rule ConfigMap's .data
	// field whose value is the YAML list of rule documents.
	healthRuleConfigMapDataKey = "rules"

	// maxUnhealthyRefsPerCondition caps resource refs enumerated in a
	// single ServiceHealth<Kind> condition message. Larger sets are
	// summarised with "…and N more"; the full list is logged at V(1).
	maxUnhealthyRefsPerCondition = 3

	// maxUnservedVersionsLogged caps how many "group/version/Kind" entries
	// appear in the aggregate "API versions not served" V(1) log line
	// before truncation with "…and N more".
	maxUnservedVersionsLogged = 5
)

// discoveryLabels are the well-known label keys chart authors use to tag
// resources with the release/instance identity. Sveltos does not stamp a
// per-service label of its own, so we query each of these in turn (value =
// service name) and union the results. A resource matching any one of them
// is considered to belong to the service.
//
// Order is irrelevant — the discovery does N list calls per rule's GVK and
// merges them, deduplicating by (namespace, name).
var discoveryLabels = []string{
	"release",                    // older bitnami-style charts
	"app",                        // legacy single-label convention
	"app.kubernetes.io/instance", // helm/k8s recommended labels
	"app.kubernetes.io/name",     // some charts put the release name here
}

// resourceScope mirrors the kubernetes scope of a resource and controls
// whether the list call attaches a namespace selector.
type resourceScope string

const (
	scopeNamespaced resourceScope = "Namespaced"
	scopeCluster    resourceScope = "Cluster"
)

// ruleDoc is the wire shape of an entry in a ConfigMap's `rules` list.
type ruleDoc struct {
	Group   string        `json:"group"`
	Version string        `json:"version"`
	Kind    string        `json:"kind"`
	Scope   resourceScope `json:"scope"`
	Healthy string        `json:"healthy"`
	Message string        `json:"message"`
}

// sourcedRuleDoc tags a raw ruleDoc with the ConfigMap it came from so
// compile errors and per-resource conditions can point back to the source.
type sourcedRuleDoc struct {
	Doc    ruleDoc
	Source string // formatted as "<namespace>/<configmap-name>#<index>"
}

// resourceRule is a compiled rule ready to evaluate.
type resourceRule struct {
	healthy cel.Program
	message cel.Program
	GVK     schema.GroupVersionKind
	Scope   resourceScope
	Source  string // for diagnostics; mirrors sourcedRuleDoc.Source
}

// ruleSet groups compiled rules by (group, kind). Version is intentionally
// ignored for grouping: a helm-deployed Deployment may be served by any
// registered version of apps/Deployment and the rule applies regardless.
//
// At query time, collectVerdicts resolves the actual listing version
// via the target cluster's RESTMapper — the intersection of what any
// rule in the slice authored and what the cluster serves. A rule
// authored for a version the cluster does not serve does not silently
// disappear; the mismatch is surfaced through an aggregate V(1) log line.
//
// Every rule in the slice for a given GroupKind must evaluate as healthy
// for a resource to be considered healthy — the rules layer additively,
// they do NOT override one another.
type ruleSet map[schema.GroupKind][]resourceRule

// ruleLoadError describes a single failure during rule loading or
// compilation. Used to surface bad user-provided rules as a condition on
// the ServiceSet so the cluster admin sees which CM is misconfigured.
type ruleLoadError struct {
	Err    error
	Source string // "<namespace>/<configmap-name>[#<index>]"
}

func (e ruleLoadError) Error() string {
	return fmt.Sprintf("%s: %v", e.Source, e.Err)
}

// resourceVerdict is the per-object outcome of evaluating the rules that
// apply to its (group, kind).
type resourceVerdict struct {
	GVK       schema.GroupVersionKind
	Name      string
	Namespace string
	Reason    string // empty when Healthy
	RuleSrc   string // populated when !Healthy; identifies the failing rule
	Healthy   bool
}

// celEnv is the shared CEL environment all rule programs are compiled
// against. The `ext.Strings()` extension provides .join(sep) and friends
// on string lists.
var celEnv = func() *cel.Env {
	env, err := cel.NewEnv(
		cel.Variable("obj", cel.DynType),
		ext.Strings(),
	)
	if err != nil {
		panic(fmt.Errorf("create CEL env: %w", err))
	}
	return env
}()

// rulesFromDocs compiles a slice of source-tagged rule documents into a
// ruleSet. Individual compile / validation errors are collected and
// returned in []ruleLoadError so a single bad rule does not break the rest
// of the set.
func rulesFromDocs(docs []sourcedRuleDoc) (ruleSet, []ruleLoadError) {
	out := make(ruleSet)
	var loadErrs []ruleLoadError

	for _, sd := range docs {
		doc := sd.Doc
		if doc.Kind == "" || doc.Version == "" {
			loadErrs = append(loadErrs, ruleLoadError{Source: sd.Source, Err: errors.New("kind and version are required")})
			continue
		}
		if doc.Scope != scopeNamespaced && doc.Scope != scopeCluster {
			loadErrs = append(loadErrs, ruleLoadError{Source: sd.Source, Err: fmt.Errorf("scope must be %q or %q, got %q", scopeNamespaced, scopeCluster, doc.Scope)})
			continue
		}

		healthyPrg, err := compileExpr(celEnv, doc.Healthy)
		if err != nil {
			loadErrs = append(loadErrs, ruleLoadError{Source: sd.Source, Err: fmt.Errorf("healthy: %w", err)})
			continue
		}
		messagePrg, err := compileExpr(celEnv, doc.Message)
		if err != nil {
			loadErrs = append(loadErrs, ruleLoadError{Source: sd.Source, Err: fmt.Errorf("message: %w", err)})
			continue
		}

		gvk := schema.GroupVersionKind{Group: doc.Group, Version: doc.Version, Kind: doc.Kind}
		out[gvk.GroupKind()] = append(out[gvk.GroupKind()], resourceRule{
			GVK:     gvk,
			Scope:   doc.Scope,
			Source:  sd.Source,
			healthy: healthyPrg,
			message: messagePrg,
		})
	}

	return out, loadErrs
}

func compileExpr(env *cel.Env, src string) (cel.Program, error) {
	ast, iss := env.Compile(src)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("compile: %w", iss.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("program: %w", err)
	}
	return prg, nil
}

// ruleCacheEntry stores the compiled rules and load errors for a single
// ConfigMap, keyed by ResourceVersion so a CM edit invalidates the entry.
// lastAccess is refreshed on every hit and consulted by sweepRuleParseCache
// to evict entries whose owning CM has been deleted (or whose ServiceSet
// no longer reconciles) after ruleParseCacheTTL.
type ruleCacheEntry struct {
	lastAccess      time.Time
	resourceVersion string
	docs            []sourcedRuleDoc // raw + source-tagged, ready for rulesFromDocs
	loadErrs        []ruleLoadError  // parse errors (not compile errors — those re-run with a single env)
}

// ruleParseCacheTTL bounds how long a parsed-rule cache entry survives
// without being referenced. Refreshed on every cache hit, so hot entries
// whose ResourceVersion never changes stay cached; a CM that gets deleted
// (or its owning ServiceSet stops reconciling) ages out on the order of
// this TTL rather than leaking forever.
const ruleParseCacheTTL = 5 * time.Minute

// ruleParseCache memoises ConfigMap parsing. The CEL compiler is fast but
// JSON-into-list-into-doc parsing repeats unnecessarily across reconciles
// when no CM has changed. Entries are keyed by (namespace, name) and
// invalidated when the CM's ResourceVersion changes; stale entries are
// swept in sweepRuleParseCache, which rulesFromConfigMaps calls once per
// reconcile. The mutex protects both the map and the per-entry lastAccess
// bookkeeping.
var ruleParseCache = struct {
	entries map[client.ObjectKey]ruleCacheEntry
	sync.Mutex
}{entries: make(map[client.ObjectKey]ruleCacheEntry)}

// docsFromConfigMap reads a single ConfigMap's `rules` key, parses it as a
// YAML list of rule documents, and returns the source-tagged slice plus
// any parse errors. Results are cached on ResourceVersion; every hit
// refreshes the entry's lastAccess so sweepRuleParseCache keeps hot
// entries alive and evicts cold ones (deleted CMs, non-reconciling
// ServiceSets) after ruleParseCacheTTL.
func docsFromConfigMap(cm *corev1.ConfigMap) ([]sourcedRuleDoc, []ruleLoadError) {
	key := client.ObjectKey{Namespace: cm.Namespace, Name: cm.Name}
	now := time.Now()

	ruleParseCache.Lock()
	cached, ok := ruleParseCache.entries[key]
	if ok && cached.resourceVersion == cm.ResourceVersion {
		cached.lastAccess = now
		ruleParseCache.entries[key] = cached
		ruleParseCache.Unlock()
		return cached.docs, cached.loadErrs
	}
	ruleParseCache.Unlock()

	rawRules, ok := cm.Data[healthRuleConfigMapDataKey]
	if !ok || strings.TrimSpace(rawRules) == "" {
		entry := ruleCacheEntry{resourceVersion: cm.ResourceVersion, lastAccess: now}
		ruleParseCache.Lock()
		ruleParseCache.entries[key] = entry
		ruleParseCache.Unlock()
		return nil, nil
	}

	var docs []ruleDoc
	if err := yaml.Unmarshal([]byte(rawRules), &docs); err != nil {
		errs := []ruleLoadError{{
			Source: fmt.Sprintf("%s/%s", cm.Namespace, cm.Name),
			Err:    fmt.Errorf("parse %q: %w", healthRuleConfigMapDataKey, err),
		}}
		entry := ruleCacheEntry{resourceVersion: cm.ResourceVersion, loadErrs: errs, lastAccess: now}
		ruleParseCache.Lock()
		ruleParseCache.entries[key] = entry
		ruleParseCache.Unlock()
		return nil, errs
	}

	sourced := make([]sourcedRuleDoc, 0, len(docs))
	for i, d := range docs {
		sourced = append(sourced, sourcedRuleDoc{
			Doc:    d,
			Source: fmt.Sprintf("%s/%s#%d", cm.Namespace, cm.Name, i),
		})
	}

	entry := ruleCacheEntry{resourceVersion: cm.ResourceVersion, docs: sourced, lastAccess: now}
	ruleParseCache.Lock()
	ruleParseCache.entries[key] = entry
	ruleParseCache.Unlock()
	return sourced, nil
}

// sweepRuleParseCache evicts cache entries whose lastAccess timestamp is
// older than ruleParseCacheTTL.
func sweepRuleParseCache() {
	cutoff := time.Now().Add(-ruleParseCacheTTL)
	ruleParseCache.Lock()
	defer ruleParseCache.Unlock()
	for key, entry := range ruleParseCache.entries {
		if entry.lastAccess.Before(cutoff) {
			delete(ruleParseCache.entries, key)
		}
	}
}

// rulesFromConfigMaps discovers health-rule ConfigMaps for a ServiceSet
// across the three tiers, parses them, compiles every rule, and returns
// the merged ruleSet plus per-rule load errors.
//
// Tiers (all layer additively — every applicable rule must pass for a
// resource to be considered healthy):
//
//  1. ConfigMaps in systemNamespace labelled health-rule-target=global.
//     These are the cluster-global defaults shipped with kcm.
//  2. ConfigMaps in serviceSet.Namespace labelled health-rule-target=global.
//     Tenant-authored defaults, scoped to a single namespace.
//  3. ConfigMaps in serviceSet.Namespace labelled
//     health-rule-target=<ownerName>, where ownerName is the name of the
//     MultiClusterService or ClusterDeployment that owns the ServiceSet.
//     Rules that speak to a specific workload. Users know the MCS/CD name
//     at authoring time (the ServiceSet is auto-created, its name is not
//     stable — the owner's is).
func rulesFromConfigMaps(
	ctx context.Context,
	mgmtClient client.Client,
	systemNamespace string,
	serviceSet *kcmv1.ServiceSet,
) (ruleSet, []ruleLoadError, error) {
	var sourced []sourcedRuleDoc
	var loadErrs []ruleLoadError

	seen := make(map[client.ObjectKey]struct{})

	// Tier 1 — kcm-system, global.
	t1, errs1, err := listAndParse(ctx, mgmtClient, systemNamespace, healthRuleTargetGlobal, seen)
	if err != nil {
		return nil, nil, fmt.Errorf("list tier 1 rules: %w", err)
	}
	sourced = append(sourced, t1...)
	loadErrs = append(loadErrs, errs1...)

	// Tier 2 — ServiceSet namespace, global. Skipped if the SS is in
	// systemNamespace (would re-list the tier 1 CMs).
	if serviceSet.Namespace != systemNamespace {
		t2, errs2, err := listAndParse(ctx, mgmtClient, serviceSet.Namespace, healthRuleTargetGlobal, seen)
		if err != nil {
			return nil, nil, fmt.Errorf("list tier 2 rules: %w", err)
		}
		sourced = append(sourced, t2...)
		loadErrs = append(loadErrs, errs2...)
	}

	// Tier 3 — ServiceSet namespace, target == the ServiceSet's owning
	// MultiClusterService or ClusterDeployment name. If the ServiceSet
	// has no such owner (shouldn't happen in normal flow), tier 3 is
	// simply skipped rather than treated as an error.
	if ownerName := serviceSetOwnerName(serviceSet); ownerName != "" {
		t3, errs3, err := listAndParse(ctx, mgmtClient, serviceSet.Namespace, ownerName, seen)
		if err != nil {
			return nil, nil, fmt.Errorf("list tier 3 rules: %w", err)
		}
		sourced = append(sourced, t3...)
		loadErrs = append(loadErrs, errs3...)
	}

	rs, compileErrs := rulesFromDocs(sourced)
	loadErrs = append(loadErrs, compileErrs...)

	// Prune stale parse-cache entries synchronously. Runs once per
	// reconcile — pruning frequency scales with controller activity.
	sweepRuleParseCache()

	return rs, loadErrs, nil
}

// serviceSetOwnerName returns the name of the MultiClusterService or
// ClusterDeployment that owns the ServiceSet. Returns "" when neither is
// present in the owner references — the tier-3 lookup will be skipped in
// that case rather than matching against an empty string.
func serviceSetOwnerName(serviceSet *kcmv1.ServiceSet) string {
	for _, o := range serviceSet.OwnerReferences {
		switch o.Kind {
		case kcmv1.MultiClusterServiceKind, kcmv1.ClusterDeploymentKind:
			return o.Name
		}
	}
	return ""
}

// listAndParse fetches every ConfigMap in namespace labelled
// health-rule-target=<targetValue> and accumulates their parsed rule docs.
// `seen` is shared across tier calls so a CM is never double-loaded
// (matters when the SS namespace IS the system namespace, or when a CM
// somehow matches more than one tier).
func listAndParse(
	ctx context.Context,
	c client.Client,
	namespace, targetValue string,
	seen map[client.ObjectKey]struct{},
) ([]sourcedRuleDoc, []ruleLoadError, error) {
	list := &corev1.ConfigMapList{}
	opts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels{healthRuleTargetLabel: targetValue},
	}
	if err := c.List(ctx, list, opts...); err != nil {
		return nil, nil, fmt.Errorf("list ConfigMaps in %s with %s=%s: %w", namespace, healthRuleTargetLabel, targetValue, err)
	}

	var sourced []sourcedRuleDoc
	var loadErrs []ruleLoadError
	for i := range list.Items {
		cm := &list.Items[i]
		key := client.ObjectKey{Namespace: cm.Namespace, Name: cm.Name}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		docs, errs := docsFromConfigMap(cm)
		sourced = append(sourced, docs...)
		loadErrs = append(loadErrs, errs...)
	}
	return sourced, loadErrs, nil
}

// evaluate runs the rule against a single unstructured object and returns
// the verdict plus a human-readable reason if unhealthy. Pods that are
// already terminating (deletionTimestamp set) are reported as healthy:
// a terminating Pod is by definition not Ready, but counting it as
// unhealthy would make every workload rollout look broken for as long as
// terminationGracePeriodSeconds.
func (r resourceRule) evaluate(obj *unstructured.Unstructured) (healthy bool, reason string, err error) {
	if r.GVK.Group == "" && r.GVK.Kind == "Pod" && obj.GetDeletionTimestamp() != nil {
		return true, "", nil
	}

	input := map[string]any{"obj": obj.Object}

	hOut, _, err := r.healthy.Eval(input)
	if err != nil {
		return false, "", fmt.Errorf("evaluate healthy: %w", err)
	}
	hVal, ok := hOut.Value().(bool)
	if !ok {
		return false, "", fmt.Errorf("healthy expression returned %T, want bool", hOut.Value())
	}
	if hVal {
		return true, "", nil
	}

	// The verdict is unhealthy either way. Best-effort resolve the
	// human-readable message; a message-expression failure or a
	// non-string result silently degrades to an empty message.
	mVal := ""
	if mOut, _, err := r.message.Eval(input); err == nil {
		if s, ok := mOut.Value().(string); ok {
			mVal = s
		}
	}
	return false, mVal, nil
}

// verifyHelmServiceOnCluster lists every known-type resource on the target
// cluster that carries any of the well-known chart-author identity labels
// (release/app/app.kubernetes.io/{instance,name}) with value = serviceName,
// evaluates each against every rule registered for its (group, kind), and
// aggregates the result. A resource is considered healthy iff every
// applicable rule passes; the first failing rule's message wins.
//
// Caller must invoke this ONLY when sveltos already reports the service as
// Deployed. The return is downgrade-only:
//
//   - kcmv1.ServiceStateFailed       -> sveltos says Deployed but no labeled
//     resources exist on the target cluster
//   - kcmv1.ServiceStateProvisioning -> at least one labeled resource is
//     unhealthy per its rule
//   - kcmv1.ServiceStateDeployed     -> every labeled resource is healthy
//
// The returned conditions slice contains one entry per unhealthy resource.
// The slice is empty when state is Deployed.
func verifyHelmServiceOnCluster(
	ctx context.Context,
	rgnClient client.Client,
	rules ruleSet,
	serviceName, serviceNamespace string,
) (string, []metav1.Condition, error) {
	verdicts, err := collectVerdicts(ctx, rgnClient, rules, serviceName, serviceNamespace)
	if err != nil {
		return "", nil, err
	}

	// LastTransitionTime is a required field per the ServiceSet CRD's
	// metav1.Condition schema; status.Update fails validation if any
	// condition omits it. Using a single now() value per call keeps the
	// timestamp consistent across all conditions emitted from this round.
	now := metav1.Now()

	if len(verdicts) == 0 {
		return kcmv1.ServiceStateFailed, []metav1.Condition{{
			Type:               serviceHealthConditionPrefix,
			Status:             metav1.ConditionFalse,
			Reason:             "NoResourcesOnCluster",
			Message:            fmt.Sprintf("no resources matching any of %v=%s found on target cluster", discoveryLabels, serviceName),
			LastTransitionTime: now,
		}}, nil
	}

	// Aggregate unhealthy verdicts by Kind. ServiceState.Conditions is a
	// listType=map keyed by Type, so duplicates like ServiceHealthPod +
	// ServiceHealthPod would fail schema validation. One condition per
	// Kind; refs sorted for stability; capped at maxUnhealthyRefsPerCondition
	// so a hundred-pod rollout doesn't produce a hundred-kilobyte message.
	byKind := make(map[string][]resourceVerdict)
	kindOrder := make([]string, 0)
	for _, v := range verdicts {
		if v.Healthy {
			continue
		}
		if _, seen := byKind[v.GVK.Kind]; !seen {
			kindOrder = append(kindOrder, v.GVK.Kind)
		}
		byKind[v.GVK.Kind] = append(byKind[v.GVK.Kind], v)
	}
	sort.Strings(kindOrder)
	if len(kindOrder) == 0 {
		return kcmv1.ServiceStateDeployed, nil, nil
	}

	logger := ctrl.LoggerFrom(ctx)
	conds := make([]metav1.Condition, 0, len(kindOrder))
	for _, kind := range kindOrder {
		items := byKind[kind]
		sort.Slice(items, func(i, j int) bool {
			if items[i].Namespace != items[j].Namespace {
				return items[i].Namespace < items[j].Namespace
			}
			return items[i].Name < items[j].Name
		})

		shown := items
		truncated := 0
		if len(items) > maxUnhealthyRefsPerCondition {
			shown = items[:maxUnhealthyRefsPerCondition]
			truncated = len(items) - maxUnhealthyRefsPerCondition
		}

		parts := make([]string, 0, len(shown))
		for _, v := range shown {
			ref := v.Name
			if v.Namespace != "" {
				ref = v.Namespace + "/" + v.Name
			}
			parts = append(parts, fmt.Sprintf("%s (%s, rule %s)",
				ref, strings.TrimSpace(v.Reason), v.RuleSrc))
		}
		msg := fmt.Sprintf("%d %s unhealthy: %s", len(items), kind, strings.Join(parts, "; "))
		if truncated > 0 {
			msg += fmt.Sprintf("; …and %d more", truncated)
			allRefs := make([]string, 0, len(items))
			for _, v := range items {
				r := v.Name
				if v.Namespace != "" {
					r = v.Namespace + "/" + v.Name
				}
				allRefs = append(allRefs, r)
			}
			logger.V(1).Info("verifier: truncated unhealthy refs in condition",
				"kind", kind,
				"service", client.ObjectKey{Namespace: serviceNamespace, Name: serviceName},
				"totalUnhealthy", len(items),
				"refs", allRefs,
			)
		}

		conds = append(conds, metav1.Condition{
			Type:               serviceHealthConditionPrefix + kind,
			Status:             metav1.ConditionFalse,
			Reason:             kind + "Unhealthy",
			Message:            msg,
			LastTransitionTime: now,
		})
	}

	return kcmv1.ServiceStateProvisioning, conds, nil
}

// collectVerdicts iterates every (group, kind) in the rule set, lists
// matching objects on the target cluster across every discovery label, and
// evaluates each against every rule registered for that (group, kind).
// A resource is unhealthy if any rule says so; the first failing rule wins.
//
// For each GroupKind the listing version is resolved through the target
// cluster's discovery (RESTMapper): the intersection of what any rule
// authored and what the cluster actually serves. If no intersection
// exists, the GK is skipped and its authored (group, version, kind)
// tuples accumulate into an aggregate V(1) log line at the end of the
// call — one summarised line per verifier round, not one per skipped GK.
//
// "Scope" disambiguates a namespaced vs. cluster-scoped list. When multiple
// rules for the same (group, kind) disagree on scope (which shouldn't
// happen in practice — kubernetes scope is intrinsic to the kind), the
// first rule's scope is used.
func collectVerdicts(
	ctx context.Context,
	rgnClient client.Client,
	rules ruleSet,
	serviceName, serviceNamespace string,
) ([]resourceVerdict, error) {
	var verdicts []resourceVerdict
	var listErrs error
	var unserved []string

	for gk, gkRules := range rules {
		if len(gkRules) == 0 {
			continue
		}
		scope := gkRules[0].Scope
		authoredVersions := distinctVersions(gkRules)

		listGVK, resolveErr := resolveServedVersion(rgnClient.RESTMapper(), gk, authoredVersions)
		if resolveErr != nil {
			listErrs = errors.Join(listErrs, resolveErr)
			continue
		}
		if listGVK == nil {
			// Either the kind isn't served at all, or the cluster serves
			// versions the rule set doesn't author for. Accumulate for
			// the aggregate log at the end of the call.
			for _, v := range authoredVersions {
				unserved = append(unserved, formatGroupVersionKind(gk, v))
			}
			continue
		}

		// Dedup objects discovered via multiple labels.
		seen := make(map[client.ObjectKey]struct{})

		for _, labelKey := range discoveryLabels {
			list := &unstructured.UnstructuredList{}
			list.SetGroupVersionKind(*listGVK)

			opts := []client.ListOption{client.MatchingLabels{labelKey: serviceName}}
			if scope == scopeNamespaced {
				opts = append(opts, client.InNamespace(serviceNamespace))
			}
			if err := rgnClient.List(ctx, list, opts...); err != nil {
				// NoMatchError/NoKindMatchError from the actual List
				// remains as defense in depth — the RESTMapper's discovery
				// cache can be briefly stale during a cluster upgrade even
				// though it said "served".
				if isNoMatchError(err) {
					break
				}
				listErrs = errors.Join(listErrs, fmt.Errorf("list %s by %s: %w", gk, labelKey, err))
				continue
			}

			for i := range list.Items {
				obj := &list.Items[i]
				key := client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}

				verdict := resourceVerdict{
					GVK:       *listGVK,
					Name:      obj.GetName(),
					Namespace: obj.GetNamespace(),
					Healthy:   true,
				}
				verdict.GVK.Kind = gk.Kind // strip the "List" suffix

				// Evaluate every rule registered for this GroupKind. First
				// failure wins; we record its message + source.
				for _, rule := range gkRules {
					healthy, reason, evalErr := rule.evaluate(obj)
					if evalErr != nil {
						listErrs = errors.Join(listErrs, fmt.Errorf("evaluate %s %s/%s (rule %s): %w", gk, obj.GetNamespace(), obj.GetName(), rule.Source, evalErr))
						continue
					}
					if !healthy {
						verdict.Healthy = false
						verdict.Reason = reason
						verdict.RuleSrc = rule.Source
						break
					}
				}

				verdicts = append(verdicts, verdict)
			}
		}
	}

	if len(unserved) > 0 {
		sort.Strings(unserved)
		shown := unserved
		truncated := 0
		if len(shown) > maxUnservedVersionsLogged {
			shown = shown[:maxUnservedVersionsLogged]
			truncated = len(unserved) - maxUnservedVersionsLogged
		}
		msg := "verifier: API versions not served by target cluster: " + strings.Join(shown, ", ")
		if truncated > 0 {
			msg += fmt.Sprintf(", …and %d more", truncated)
		}
		ctrl.LoggerFrom(ctx).V(1).Info(msg)
	}

	return verdicts, listErrs
}

// distinctVersions returns the versions authored across gkRules,
// preserving first-encountered order and de-duplicating. First-seen
// order matters for reproducibility: two versions tied on served-ness
// would otherwise have a nondeterministic winner across process restarts.
func distinctVersions(gkRules []resourceRule) []string {
	out := make([]string, 0, len(gkRules))
	seen := make(map[string]struct{}, len(gkRules))
	for _, r := range gkRules {
		if _, ok := seen[r.GVK.Version]; ok {
			continue
		}
		seen[r.GVK.Version] = struct{}{}
		out = append(out, r.GVK.Version)
	}
	return out
}

// resolveServedVersion returns the (Group, Version, Kind+"List") to
// use for listing objects of gk on the target cluster. The version is
// chosen from the intersection of what any rule authored for gk and
// what the cluster's RESTMapper reports as served.
//
// Returns (nil, nil) — no error — when the caller should silently skip
// this GroupKind. That covers two cases:
//   - The cluster does not serve gk under any version.
//   - The cluster serves gk but under versions the rule set does not
//     author for; the caller aggregates those into an operator-visible
//     log line rather than evaluating against a version the author
//     didn't intend.
//
// Any other mapper error (permission denied, transport failure) is
// returned and the caller aggregates it into the reconcile-level error.
func resolveServedVersion(
	mapper meta.RESTMapper,
	gk schema.GroupKind,
	authoredVersions []string,
) (*schema.GroupVersionKind, error) {
	mappings, err := mapper.RESTMappings(gk)
	if err != nil {
		if meta.IsNoMatchError(err) {
			return nil, nil //nolint:nilnil // deliberate: caller treats nil GVK + nil error as "silent skip" (kind not served)
		}
		return nil, fmt.Errorf("discover versions for %s: %w", gk, err)
	}

	served := make(map[string]struct{}, len(mappings))
	for _, m := range mappings {
		served[m.GroupVersionKind.Version] = struct{}{}
	}

	for _, v := range authoredVersions {
		if _, ok := served[v]; ok {
			return &schema.GroupVersionKind{
				Group:   gk.Group,
				Version: v,
				Kind:    gk.Kind + "List",
			}, nil
		}
	}
	return nil, nil //nolint:nilnil // deliberate: caller treats nil GVK + nil error as "silent skip" (kind not served or version mismatch)
}

// formatGroupVersionKind renders "group/version/Kind" with the core
// group spelled out ("core/v1/Pod") so a log line reads naturally
// instead of "/v1/Pod".
func formatGroupVersionKind(gk schema.GroupKind, version string) string {
	g := gk.Group
	if g == "" {
		g = "core"
	}
	return fmt.Sprintf("%s/%s/%s", g, version, gk.Kind)
}

func isNoMatchError(err error) bool {
	var noKind *meta.NoKindMatchError
	var noResource *meta.NoResourceMatchError
	return errors.As(err, &noKind) || errors.As(err, &noResource)
}

// serviceSetHealthRulesCondition is the ServiceSet-level condition the
// verifier writes to surface health-rule load errors. Status=True when all
// rule ConfigMaps parsed and compiled successfully; False when at least
// one rule failed and the verifier had to skip it.
const serviceSetHealthRulesCondition = "ServiceSetHealthRules"

// applyRuleLoadErrorCondition writes (or refreshes) the
// ServiceSetHealthRules condition on the ServiceSet to reflect the result
// of the most recent rule load.
func applyRuleLoadErrorCondition(serviceSet *kcmv1.ServiceSet, loadErrs []ruleLoadError) {
	cond := metav1.Condition{
		Type:               serviceSetHealthRulesCondition,
		ObservedGeneration: serviceSet.Generation,
	}
	if len(loadErrs) == 0 {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "AllRulesValid"
		cond.Message = "All health rules loaded successfully"
	} else {
		msgs := make([]string, 0, len(loadErrs))
		for _, e := range loadErrs {
			msgs = append(msgs, e.Error())
		}
		cond.Status = metav1.ConditionFalse
		cond.Reason = "HealthRuleInvalid"
		cond.Message = strings.Join(msgs, "; ")
	}
	apimetaSetStatusCondition(&serviceSet.Status.Conditions, cond)
}

// apimetaSetStatusCondition is a function variable to keep the
// apimachinery/api/meta dependency confined to one call site, mirroring
// the import alias the rest of the controller uses.
var apimetaSetStatusCondition = meta.SetStatusCondition

// fetchHelmArtifactsForVerifier loads the sveltos artifacts the verifier
// needs to compute per-service hashes:
//
//   - ClusterSummary  — for HelmReleaseSummaries[i].{ValuesHash, PatchesHash}
//   - ClusterConfiguration — for Charts[i].{AppVersion, ChartVersion,
//     Namespace, ReleaseName, RepoURL}
//
// Both are namespaced; both live in the matching cluster's namespace
// (CAPI Cluster.Namespace for non-self-managed, "mgmt" for self-managed).
//
// The ClusterConfiguration is found via owner references rather than
// reproducing sveltos's unexported name-format helpers: sveltos stamps the
// Profile/ClusterProfile as an owner ref on the ClusterConfiguration when
// it first records resources for that (cluster, profile) pair, so the
// owner UID is a stable identity link.
//
// Returns (nil, nil, nil) — without error — when sveltos hasn't picked a
// matching cluster yet or hasn't created the artifacts. The verifier
// treats that case as "no fingerprint information available" and falls
// back to the downgrade-only path.
func fetchHelmArtifactsForVerifier(
	ctx context.Context,
	rgnClient client.Client,
	serviceSet *kcmv1.ServiceSet,
) (
	summary *addoncontrollerv1beta1.ClusterSummary,
	cc *addoncontrollerv1beta1.ClusterConfiguration,
	profileKind string,
	profileName string,
	err error,
) {
	var profileObj client.Object
	if serviceSet.Spec.Provider.SelfManagement {
		profileObj = new(addoncontrollerv1beta1.ClusterProfile)
	} else {
		profileObj = new(addoncontrollerv1beta1.Profile)
	}
	if err := rgnClient.Get(ctx, client.ObjectKeyFromObject(serviceSet), profileObj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, "", "", nil
		}
		return nil, nil, "", "", fmt.Errorf("get sveltos profile %s: %w", client.ObjectKeyFromObject(serviceSet), err)
	}

	var (
		matchingRefs []corev1.ObjectReference
		profileUID   types.UID
	)
	switch p := profileObj.(type) {
	case *addoncontrollerv1beta1.Profile:
		matchingRefs = p.Status.MatchingClusterRefs
		profileKind = addoncontrollerv1beta1.ProfileKind
		profileName = p.Name
		profileUID = p.UID
	case *addoncontrollerv1beta1.ClusterProfile:
		matchingRefs = p.Status.MatchingClusterRefs
		profileKind = addoncontrollerv1beta1.ClusterProfileKind
		profileName = p.Name
		profileUID = p.UID
	}
	if len(matchingRefs) == 0 {
		return nil, nil, "", "", nil
	}

	cluster := matchingRefs[0]
	isSveltos := cluster.APIVersion == libsveltosv1beta1.GroupVersion.WithKind(libsveltosv1beta1.SveltosClusterKind).GroupVersion().String()

	// ClusterSummary — sveltos exports a name helper for this one.
	summaryName := clusterops.GetClusterSummaryName(profileKind, profileName, cluster.Name, isSveltos)
	summary = new(addoncontrollerv1beta1.ClusterSummary)
	summaryKey := client.ObjectKey{Name: summaryName, Namespace: cluster.Namespace}
	if err := rgnClient.Get(ctx, summaryKey, summary); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, nil, "", "", fmt.Errorf("get ClusterSummary %s: %w", summaryKey, err)
		}
		summary = nil
	}

	cc, err = findOwnedClusterConfiguration(ctx, rgnClient, cluster.Namespace, profileUID)
	if err != nil {
		return nil, nil, "", "", err
	}
	return summary, cc, profileKind, profileName, nil
}

// findOwnedClusterConfiguration returns the ClusterConfiguration in
// namespace whose OwnerReferences include profileUID. Returns (nil, nil)
// when none exists yet (sveltos may not have recorded resources for the
// profile on this cluster yet). Callers already handle a nil result as
// "no fingerprint information available" so a sentinel error would only
// force pointless errors.Is checks at every call site.
func findOwnedClusterConfiguration(
	ctx context.Context,
	rgnClient client.Client,
	namespace string,
	profileUID types.UID,
) (*addoncontrollerv1beta1.ClusterConfiguration, error) {
	list := new(addoncontrollerv1beta1.ClusterConfigurationList)
	if err := rgnClient.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list ClusterConfigurations in %s: %w", namespace, err)
	}
	for i := range list.Items {
		for _, owner := range list.Items[i].OwnerReferences {
			if owner.UID == profileUID {
				return &list.Items[i], nil
			}
		}
	}
	return nil, nil //nolint:nilnil // deliberate: nil result means "not-found-yet", not an error condition
}

// serviceHashesFromArtifacts builds a (releaseNamespace, releaseName) →
// ServiceHash map for every Helm release sveltos has recorded for the
// supplied profile. Missing inputs are tolerated: if either summary or cc
// is nil the result is an empty map.
func serviceHashesFromArtifacts(
	summary *addoncontrollerv1beta1.ClusterSummary,
	cc *addoncontrollerv1beta1.ClusterConfiguration,
	profileKind, profileName string,
) map[client.ObjectKey]string {
	hashes := make(map[client.ObjectKey]string)
	if summary == nil || cc == nil {
		return hashes
	}

	// Locate this profile's section inside the ClusterConfiguration.
	idx, err := addoncontrollerv1beta1.GetClusterConfigurationSectionIndex(cc, profileKind, profileName)
	if err != nil {
		return hashes
	}

	var features []addoncontrollerv1beta1.Feature
	if profileKind == addoncontrollerv1beta1.ClusterProfileKind {
		features = cc.Status.ClusterProfileResources[idx].Features
	} else {
		features = cc.Status.ProfileResources[idx].Features
	}

	var charts []addoncontrollerv1beta1.Chart
	for i := range features {
		if features[i].FeatureID == libsveltosv1beta1.FeatureHelm {
			charts = features[i].Charts
			break
		}
	}

	// Index by (releaseNamespace, releaseName) for cross-source lookup.
	chartByKey := make(map[client.ObjectKey]*addoncontrollerv1beta1.Chart, len(charts))
	for i := range charts {
		chartByKey[client.ObjectKey{Namespace: charts[i].Namespace, Name: charts[i].ReleaseName}] = &charts[i]
	}

	for i := range summary.Status.HelmReleaseSummaries {
		rs := &summary.Status.HelmReleaseSummaries[i]
		key := client.ObjectKey{Namespace: rs.ReleaseNamespace, Name: rs.ReleaseName}
		chart, ok := chartByKey[key]
		if !ok {
			continue
		}
		if h := computeServiceHash(rs, chart); h != "" {
			hashes[key] = h
		}
	}

	return hashes
}

// computeServiceHash produces a stable hex-sha256 fingerprint covering
// chart identity, rendered values, and sveltos-applied patches for a
// single helm-deployed service.
//
//	ReleaseHash = sha256(appVersion|chartVersion|namespace|releaseName|repoURL)
//	ServiceHash = sha256(ReleaseHash || ValuesHash || PatchesHash)
//
// `lastAppliedTime` is INTENTIONALLY excluded — sveltos rewrites it on every
// re-touch even when nothing else changed, and including it would defeat
// the whole point of the fingerprint.
//
// Returns "" when either input is nil. Callers MUST treat empty as
// "no fingerprint available" — never short-circuit comparisons against
// other empties (e.g. a brand-new service whose ClusterConfiguration
// entry hasn't appeared yet would otherwise spuriously match a
// brand-new ServiceState whose LastDeployedHash is also unset).
func computeServiceHash(release *addoncontrollerv1beta1.HelmChartSummary, chart *addoncontrollerv1beta1.Chart) string {
	if release == nil || chart == nil {
		return ""
	}

	// Stable, explicit field ordering — no map iteration.
	releaseInput := strings.Join([]string{
		"appVersion=" + chart.AppVersion,
		"chartVersion=" + chart.ChartVersion,
		"namespace=" + chart.Namespace,
		"releaseName=" + chart.ReleaseName,
		"repoURL=" + chart.RepoURL,
	}, "|")
	releaseHash := sha256.Sum256([]byte(releaseInput))

	// sha256.Hash.Write is documented to never return an error; the
	// blank-assignments silence errcheck without adding real error handling.
	combined := sha256.New()
	_, _ = combined.Write(releaseHash[:])
	_, _ = combined.Write(release.ValuesHash)
	_, _ = combined.Write(release.PatchesHash)
	return hex.EncodeToString(combined.Sum(nil))
}

// applyVerifyConditions replaces any verifier-owned condition (Type
// starting with serviceHealthConditionPrefix) on the service state with
// the supplied new set, leaving non-verifier conditions untouched. Called
// even when the new set is empty so a recovering service has its old
// health conditions cleared.
func applyVerifyConditions(s *kcmv1.ServiceState, newConds []metav1.Condition) {
	kept := make([]metav1.Condition, 0, len(s.Conditions))
	for _, c := range s.Conditions {
		if !strings.HasPrefix(c.Type, serviceHealthConditionPrefix) {
			kept = append(kept, c)
		}
	}
	kept = append(kept, newConds...)
	s.Conditions = kept
}
