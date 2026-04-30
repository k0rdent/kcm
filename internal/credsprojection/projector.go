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

// Package credsprojection handles rendering and applying credential resources onto child clusters
package credsprojection

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigyaml "sigs.k8s.io/yaml"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// templateData is the top-level data passed into each resource template
type templateData struct {
	// InfrastructureProvider is the infrastructure cluster object (e.g. AzureCluster,
	// AWSCluster, VSphereCluster) referenced by the CAPI Cluster's InfrastructureRef
	InfrastructureProvider map[string]any
	// Identity is the raw data of the identity object referenced by the Credential
	Identity map[string]any
	// IdentitySecret is the raw data of the companion secret for non-Secret identities.
	// Nil when the identity itself is a Secret
	IdentitySecret map[string]any
}

// Projector renders credential resource templates and applies the resulting
// manifests to a target (child) cluster
type Projector struct {
	mgmtClient  client.Client
	childClient client.Client
}

// NewProjector creates a Projector that reads source objects from mgmtClient
// and applies rendered manifests via childClient
func NewProjector(mgmtClient, childClient client.Client) *Projector {
	return &Projector{
		mgmtClient:  mgmtClient,
		childClient: childClient,
	}
}

// Project reads the credential's identity objects and the projection template ConfigMap,
// renders the templates, and applies (creates or updates) the resulting resources on the
// child cluster. The optional infraProvider is the infrastructure cluster object (e.g.
// AzureCluster) resolved from the CAPI Cluster's InfrastructureRef
func (p *Projector) Project(ctx context.Context, cred *kcmv1.Credential, infraProvider *unstructured.Unstructured) error {
	if cred.Spec.ProjectionConfig == nil {
		return nil
	}
	if cred.Spec.IdentityRef == nil {
		return fmt.Errorf("credential %s has no identityRef", client.ObjectKeyFromObject(cred))
	}

	cfg := cred.Spec.ProjectionConfig

	data, err := p.buildTemplateData(ctx, cred, infraProvider)
	if err != nil {
		return fmt.Errorf("building template data: %w", err)
	}

	var objects []unstructured.Unstructured
	if cfg.ResourceTemplateRef != nil {
		cm := new(corev1.ConfigMap)
		cmKey := client.ObjectKey{Namespace: cred.Namespace, Name: cfg.ResourceTemplateRef.Name}
		if err := p.mgmtClient.Get(ctx, cmKey, cm); err != nil {
			return fmt.Errorf("getting resource template ConfigMap %s: %w", cmKey, err)
		}

		objects, err = renderConfigMapTemplates(cm, data)
		if err != nil {
			return fmt.Errorf("rendering templates from ConfigMap %s: %w", cmKey, err)
		}
	} else {
		objects, err = renderInlineTemplate(cfg.ResourceTemplate, data)
		if err != nil {
			return fmt.Errorf("rendering inline resource template: %w", err)
		}
	}

	for i := range objects {
		if err := serverSideApply(ctx, p.childClient, &objects[i]); err != nil {
			return fmt.Errorf("applying object %s/%s %s: %w",
				objects[i].GetNamespace(), objects[i].GetName(), objects[i].GroupVersionKind(), err)
		}
	}

	return nil
}

// buildTemplateData fetches the identity objects and assembles the template data context
func (p *Projector) buildTemplateData(ctx context.Context, cred *kcmv1.Credential, infraProvider *unstructured.Unstructured) (*templateData, error) {
	ref := cred.Spec.IdentityRef

	identity := &unstructured.Unstructured{}
	identity.SetAPIVersion(ref.APIVersion)
	identity.SetKind(ref.Kind)
	identityKey := client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}
	if err := p.mgmtClient.Get(ctx, identityKey, identity); err != nil {
		return nil, fmt.Errorf("getting identity object %s %s: %w", identity.GroupVersionKind(), identityKey, err)
	}

	data := &templateData{
		Identity: identity.Object,
	}

	if infraProvider != nil {
		data.InfrastructureProvider = infraProvider.Object
	}

	// Populate companion secret only when explicitly referenced via projectionConfig.
	if cred.Spec.ProjectionConfig != nil && cred.Spec.ProjectionConfig.SecretDataRef != nil {
		secret := new(corev1.Secret)
		secretKey := client.ObjectKey{Namespace: cred.Namespace, Name: cred.Spec.ProjectionConfig.SecretDataRef.Name}
		if err := p.mgmtClient.Get(ctx, secretKey, secret); err != nil {
			return nil, fmt.Errorf("getting companion secret %s: %w", secretKey, err)
		}

		data.IdentitySecret = secretToTemplateMap(secret)
	}

	return data, nil
}

// secretToTemplateMap converts a Secret into a map[string]any suitable for
// template rendering, matching the shape of a Secret from the Kubernetes API
func secretToTemplateMap(s *corev1.Secret) map[string]any {
	metadata := map[string]any{
		"name":      s.Name,
		"namespace": s.Namespace,
	}

	if len(s.Labels) > 0 {
		labels := make(map[string]any, len(s.Labels))
		for k, v := range s.Labels {
			labels[k] = v
		}
		metadata["labels"] = labels
	}

	if len(s.Annotations) > 0 {
		annotations := make(map[string]any, len(s.Annotations))
		for k, v := range s.Annotations {
			annotations[k] = v
		}
		metadata["annotations"] = annotations
	}

	m := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   metadata,
	}

	if s.Type != "" {
		m["type"] = s.Type
	}

	data := make(map[string]any, len(s.Data))
	for k, v := range s.Data {
		data[k] = base64.StdEncoding.EncodeToString(v)
	}
	m["data"] = data

	return m
}

// templateFuncMap returns template helpers that mirror the Sveltos template
// functions so that existing resource-template ConfigMaps remain compatible
func templateFuncMap(data *templateData) template.FuncMap {
	return template.FuncMap{
		"b64dec": func(s string) (string, error) {
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return "", fmt.Errorf("b64dec: %w", err)
			}

			return string(b), nil
		},
		"b64enc": func(s string) string {
			return base64.StdEncoding.EncodeToString([]byte(s))
		},
		"hasKey": func(m map[string]any, key string) bool {
			_, ok := m[key]
			return ok
		},
		"default": func(def, val any) any {
			if val == nil {
				return def
			}

			if s, ok := val.(string); ok && s == "" {
				return def
			}

			return val
		},
		// getResource provides backward compatibility with Sveltos templates
		// that use (getResource "InfrastructureProviderIdentity").
		// The new canonical names are "Identity" and "IdentitySecret".
		"getResource": func(identifier string) (map[string]any, error) {
			switch identifier {
			case "Identity", "InfrastructureProviderIdentity":
				if data.Identity == nil {
					return nil, errors.New("identity is not available")
				}

				return data.Identity, nil
			case "IdentitySecret", "InfrastructureProviderIdentitySecret":
				if data.IdentitySecret == nil {
					return nil, errors.New("identity secret is not available")
				}

				return data.IdentitySecret, nil
			default:
				return nil, fmt.Errorf("unknown resource identifier %q; supported: Identity, IdentitySecret (aliases: InfrastructureProviderIdentity, InfrastructureProviderIdentitySecret)", identifier)
			}
		},
		"fromYaml": func(s string) (map[string]any, error) {
			var m map[string]any
			if err := sigyaml.Unmarshal([]byte(s), &m); err != nil {
				return nil, fmt.Errorf("fromYaml: %w", err)
			}

			return m, nil
		},
		"toYaml": func(v any) (string, error) {
			b, err := sigyaml.Marshal(v)
			if err != nil {
				return "", fmt.Errorf("toYaml: %w", err)
			}

			return strings.TrimSuffix(string(b), "\n"), nil
		},
		"toJson": func(v any) (string, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", fmt.Errorf("toJson: %w", err)
			}

			return string(b), nil
		},
		"fail": func(msg string) (string, error) {
			return "", fmt.Errorf("%s", msg)
		},
		"dict": func(args ...any) (map[string]any, error) {
			if len(args)&1 != 0 {
				return nil, errors.New("dict requires even number of arguments (key-value pairs)")
			}

			m := make(map[string]any, len(args)/2)
			for i := 0; i < len(args); i += 2 {
				key, ok := args[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict key at position %d is not a string", i)
				}

				m[key] = args[i+1]
			}

			return m, nil
		},
	}
}

// renderConfigMapTemplates renders every data entry in the given ConfigMap as a
// Go text/template with the provided data, parses the rendered YAML into
// unstructured objects, and returns the collected results
func renderConfigMapTemplates(cm *corev1.ConfigMap, data *templateData) ([]unstructured.Unstructured, error) {
	var result []unstructured.Unstructured

	keys := slices.Collect(maps.Keys(cm.Data))
	slices.Sort(keys)

	for _, key := range keys {
		tplText := cm.Data[key]
		tpl, err := template.New(key).
			Option("missingkey=zero").
			Funcs(templateFuncMap(data)).
			Parse(tplText)
		if err != nil {
			return nil, fmt.Errorf("parsing template %q: %w", key, err)
		}

		var buf bytes.Buffer
		if err := tpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("executing template %q: %w", key, err)
		}

		objs, err := parseMultiDocYAML(buf.Bytes())
		if err != nil {
			return nil, fmt.Errorf("parsing rendered YAML from template %q: %w", key, err)
		}
		result = append(result, objs...)
	}

	return result, nil
}

// renderInlineTemplate renders a single inline Go text/template string with the
// provided data and returns the parsed unstructured objects
func renderInlineTemplate(tplText string, data *templateData) ([]unstructured.Unstructured, error) {
	tpl, err := template.New("inline").
		Option("missingkey=zero").
		Funcs(templateFuncMap(data)).
		Parse(tplText)
	if err != nil {
		return nil, fmt.Errorf("parsing inline template: %w", err)
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing inline template: %w", err)
	}

	return parseMultiDocYAML(buf.Bytes())
}

// parseMultiDocYAML splits a multi-document YAML byte slice into individual unstructured objects
func parseMultiDocYAML(data []byte) ([]unstructured.Unstructured, error) {
	var result []unstructured.Unstructured
	reader := yaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))

	for {
		raw, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading YAML document: %w", err)
		}

		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}

		obj := unstructured.Unstructured{}
		if err := yaml.Unmarshal(raw, &obj.Object); err != nil {
			return nil, fmt.Errorf("unmarshaling YAML document: %w", err)
		}

		// skip empty documents (e.g. "---\n")
		if len(obj.Object) == 0 {
			continue
		}

		result = append(result, obj)
	}

	return result, nil
}

// serverSideApply applies the given unstructured object to the cluster using server-side apply semantics
func serverSideApply(ctx context.Context, cl client.Client, obj *unstructured.Unstructured) error {
	const fieldManager = "kcm-credential-projector"

	if obj.GetAPIVersion() == "" || obj.GetKind() == "" {
		return errors.New("object is missing apiVersion or kind")
	}

	return cl.Apply(ctx, client.ApplyConfigurationFromUnstructured(obj), client.FieldOwner(fieldManager), client.ForceOwnership)
}
