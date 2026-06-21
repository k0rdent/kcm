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
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	GVK     schema.GroupVersionKind
	Scope   resourceScope
	Source  string // for diagnostics; mirrors sourcedRuleDoc.Source
	healthy cel.Program
	message cel.Program
}

// ruleSet groups compiled rules by (group, kind). Version is intentionally
// ignored: a helm-deployed Deployment may be served by any registered
// version of apps/Deployment and the rule applies regardless.
//
// Every rule in the slice for a given GroupKind must evaluate as healthy
// for a resource to be considered healthy — the rules layer additively,
// they do NOT override one another.
type ruleSet map[schema.GroupKind][]resourceRule

// ruleLoadError describes a single failure during rule loading or
// compilation. Used to surface bad user-provided rules as a condition on
// the ServiceSet so the cluster admin sees which CM is misconfigured.
type ruleLoadError struct {
	Source string // "<namespace>/<configmap-name>[#<index>]"
	Err    error
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
	Healthy   bool
	Reason    string // empty when Healthy
	RuleSrc   string // populated when !Healthy; identifies the failing rule
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
type ruleCacheEntry struct {
	resourceVersion string
	docs            []sourcedRuleDoc // raw + source-tagged, ready for rulesFromDocs
	loadErrs        []ruleLoadError  // parse errors (not compile errors — those re-run with a single env)
}

// ruleParseCache memoises ConfigMap parsing. The CEL compiler is fast but
// JSON-into-list-into-doc parsing repeats unnecessarily across reconciles
// when no CM has changed.
var ruleParseCache = struct {
	sync.RWMutex
	entries map[client.ObjectKey]ruleCacheEntry
}{entries: make(map[client.ObjectKey]ruleCacheEntry)}

// docsFromConfigMap reads a single ConfigMap's `rules` key, parses it as a
// YAML list of rule documents, and returns the source-tagged slice plus
// any parse errors. Results are cached on ResourceVersion.
func docsFromConfigMap(cm *corev1.ConfigMap) ([]sourcedRuleDoc, []ruleLoadError) {
	key := client.ObjectKey{Namespace: cm.Namespace, Name: cm.Name}

	ruleParseCache.RLock()
	cached, ok := ruleParseCache.entries[key]
	ruleParseCache.RUnlock()
	if ok && cached.resourceVersion == cm.ResourceVersion {
		return cached.docs, cached.loadErrs
	}

	rawRules, ok := cm.Data[healthRuleConfigMapDataKey]
	if !ok || strings.TrimSpace(rawRules) == "" {
		entry := ruleCacheEntry{resourceVersion: cm.ResourceVersion}
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
		entry := ruleCacheEntry{resourceVersion: cm.ResourceVersion, loadErrs: errs}
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

	entry := ruleCacheEntry{resourceVersion: cm.ResourceVersion, docs: sourced}
	ruleParseCache.Lock()
	ruleParseCache.entries[key] = entry
	ruleParseCache.Unlock()
	return sourced, nil
}

// rulesFromConfigMaps discovers health-rule ConfigMaps for a ServiceSet
// across the three tiers, parses them, compiles every rule, and returns
// the merged ruleSet plus per-rule load errors.
//
// Tiers (lowest precedence to highest — but because rules layer additively,
// "precedence" really means "added on top of"):
//
//  1. ConfigMaps in systemNamespace labelled health-rule-target=global
//  2. ConfigMaps in serviceSetNamespace labelled health-rule-target=global
//  3. ConfigMaps in serviceSetNamespace labelled health-rule-target=<serviceSetName>
//
// All discovered rules are loaded; for a given (group, kind) the verifier
// requires every rule to pass for the resource to be healthy.
func rulesFromConfigMaps(
	ctx context.Context,
	mgmtClient client.Client,
	systemNamespace, serviceSetNamespace, serviceSetName string,
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
	if serviceSetNamespace != systemNamespace {
		t2, errs2, err := listAndParse(ctx, mgmtClient, serviceSetNamespace, healthRuleTargetGlobal, seen)
		if err != nil {
			return nil, nil, fmt.Errorf("list tier 2 rules: %w", err)
		}
		sourced = append(sourced, t2...)
		loadErrs = append(loadErrs, errs2...)
	}

	// Tier 3 — ServiceSet namespace, target == this ServiceSet's name.
	t3, errs3, err := listAndParse(ctx, mgmtClient, serviceSetNamespace, serviceSetName, seen)
	if err != nil {
		return nil, nil, fmt.Errorf("list tier 3 rules: %w", err)
	}
	sourced = append(sourced, t3...)
	loadErrs = append(loadErrs, errs3...)

	rs, compileErrs := rulesFromDocs(sourced)
	loadErrs = append(loadErrs, compileErrs...)
	return rs, loadErrs, nil
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

	mOut, _, err := r.message.Eval(input)
	if err != nil {
		// Don't fail the whole verdict if the message expression breaks; we
		// already know the resource is unhealthy.
		return false, "", nil
	}
	mVal, _ := mOut.Value().(string)
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
//                                       resources exist on the target cluster
//   - kcmv1.ServiceStateProvisioning -> at least one labeled resource is
//                                       unhealthy per its rule
//   - kcmv1.ServiceStateDeployed     -> every labeled resource is healthy
//
// The returned conditions slice contains one entry per unhealthy resource.
// The slice is empty when state is Deployed.
func verifyHelmServiceOnCluster(
	ctx context.Context,
	remote client.Client,
	rules ruleSet,
	serviceName, serviceNamespace string,
) (string, []metav1.Condition, error) {
	verdicts, err := collectVerdicts(ctx, remote, rules, serviceName, serviceNamespace)
	if err != nil {
		return "", nil, err
	}

	if len(verdicts) == 0 {
		return kcmv1.ServiceStateFailed, []metav1.Condition{{
			Type:    serviceHealthConditionPrefix,
			Status:  metav1.ConditionFalse,
			Reason:  "NoResourcesOnCluster",
			Message: fmt.Sprintf("no resources matching any of %v=%s found on target cluster", discoveryLabels, serviceName),
		}}, nil
	}

	conds := make([]metav1.Condition, 0)
	for _, v := range verdicts {
		if v.Healthy {
			continue
		}
		ref := v.Name
		if v.Namespace != "" {
			ref = v.Namespace + "/" + v.Name
		}
		conds = append(conds, metav1.Condition{
			Type:    serviceHealthConditionPrefix + v.GVK.Kind,
			Status:  metav1.ConditionFalse,
			Reason:  v.GVK.Kind + "Unhealthy",
			Message: fmt.Sprintf("%s %s: %s (rule %s)", v.GVK.Kind, ref, v.Reason, v.RuleSrc),
		})
	}

	if len(conds) == 0 {
		return kcmv1.ServiceStateDeployed, nil, nil
	}
	return kcmv1.ServiceStateProvisioning, conds, nil
}

// collectVerdicts iterates every (group, kind) in the rule set, lists
// matching objects on the target cluster across every discovery label, and
// evaluates each against every rule registered for that (group, kind).
// A resource is unhealthy if any rule says so; the first failing rule wins.
//
// "Scope" disambiguates a namespaced vs. cluster-scoped list. When multiple
// rules for the same (group, kind) disagree on scope (which shouldn't
// happen in practice — kubernetes scope is intrinsic to the kind), the
// first rule's scope is used.
func collectVerdicts(
	ctx context.Context,
	remote client.Client,
	rules ruleSet,
	serviceName, serviceNamespace string,
) ([]resourceVerdict, error) {
	var verdicts []resourceVerdict
	var listErrs error

	for gk, gkRules := range rules {
		if len(gkRules) == 0 {
			continue
		}
		// Take the first rule's GVK + scope for listing — every rule in this
		// slice targets the same GroupKind so the list will return the same
		// items regardless of which rule's GVK we use.
		scope := gkRules[0].Scope
		listGVK := gkRules[0].GVK
		listGVK.Kind = gk.Kind + "List"

		// Dedup objects discovered via multiple labels.
		seen := make(map[client.ObjectKey]struct{})

		for _, labelKey := range discoveryLabels {
			list := &unstructured.UnstructuredList{}
			list.SetGroupVersionKind(listGVK)

			opts := []client.ListOption{client.MatchingLabels{labelKey: serviceName}}
			if scope == scopeNamespaced {
				opts = append(opts, client.InNamespace(serviceNamespace))
			}
			if err := remote.List(ctx, list, opts...); err != nil {
				// NoMatchError/NoKindMatchError means the API server on the
				// target cluster does not serve this GVK — treat as zero
				// matches, not a hard failure.
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
					GVK:       listGVK,
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
	return verdicts, listErrs
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
	s.Conditions = append(kept, newConds...)
}
