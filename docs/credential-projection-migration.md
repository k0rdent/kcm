# Credential Projection Migration Guide

## Background

KCM previously relied on the Sveltos addon-controller to propagate cloud-provider
credentials to child clusters. This was implemented via Sveltos `Profile`
`TemplateResourceRefs` and `PolicyRefs`, which injected identity objects and
applied ConfigMap templates on the remote cluster through the Sveltos templating
engine.

This approach had several drawbacks:

1. **Race condition** ([#2550](https://github.com/k0rdent/kcm/issues/2550)):
   Services that depend on cloud-provider secrets (CCM, CSI) could be deployed
   before the credential resources were ready on the child cluster.
2. **Cross-namespace bugs** ([#1586](https://github.com/k0rdent/kcm/issues/1586)):
   The `TemplateResourceRefs`/`PolicyRefs` convention made it difficult to
   cleanly scope credential propagation to the correct namespaces.
3. **Tight coupling**: Credential lifecycle was interleaved with Sveltos Profile
   reconciliation, making it hard to reason about ordering guarantees.

The new **direct credential projection** approach addresses these by having the
`ClusterDeployment` controller project credentials onto the child cluster
*before* any services are deployed, using a dedicated `credsprojection.Projector`.

## Feature Flag

The migration is gated on the `projectionConfig` field in the `Credential` spec.

| `Credential.spec.projectionConfig` | Behavior |
|-------------------------------------|----------|
| **Not set** (default) | Legacy Sveltos-based propagation via `TemplateResourceRefs`/`PolicyRefs`. No behavioral change. |
| **Set** | Direct projection via `credsprojection.Projector`. The `ClusterDeployment` controller resolves the CAPI infrastructure provider, renders the template ConfigMap, and applies the results to the child cluster *before* deploying services. |

This means existing deployments continue to work unchanged. Migration is
opt-in per `Credential`.

## Why the New Approach Is Better

| Aspect | Legacy (Sveltos) | Direct Projection |
|--------|-------------------|-------------------|
| Ordering | No guarantee — credentials and services race. | Credentials are projected and verified before services start. |
| Template engine | Sveltos-specific (`getResource`, Lua, `getField`). | Standard Go `text/template` with a focused function library; the `getResource` function is also supported for backward compatibility. |
| Observability | Credential failures surface as Sveltos Profile errors. | Dedicated `CredentialsProjected` condition on `ClusterDeployment`. |
| Scope | Runs inside Sveltos Profile reconciliation. | Runs directly in `ClusterDeployment` controller with clear lifecycle. |

## Migration Steps

### 1. Create a Projection Template ConfigMap

The new template ConfigMap uses the same Go template syntax as the old Sveltos
ConfigMap, with one key difference: identity objects are available directly
as template data fields rather than only through `getResource`.

**Available template data:**

| Field | Description |
|-------|-------------|
| `.InfrastructureProvider` | Infrastructure cluster object (e.g. `AzureCluster`, `VSphereCluster`) from the CAPI `Cluster.spec.infrastructureRef`. |
| `.Identity` | Identity object referenced by `Credential.spec.identityRef`. |
| `.IdentitySecret` | Companion secret referenced by `projectionConfig.secretDataRef`. Nil when not set. |

**Available template functions:**

| Function | Description |
|----------|-------------|
| `getResource "Identifier"` | Returns identity or secret data (backward-compatible with Sveltos templates). Supported identifiers: `Identity`, `IdentitySecret` (aliases: `InfrastructureProviderIdentity`, `InfrastructureProviderIdentitySecret`). |
| `b64dec` / `b64enc` | Base64 decode/encode. |
| `hasKey` | Check if a map contains a key. |
| `default` | Return a default value if the input is nil or empty. |
| `fromYaml` | Parse a YAML string into a map. |
| `toYaml` | Serialize a value to YAML. |
| `toJson` | Serialize a value to JSON. |
| `dict` | Create a map from key-value pairs: `dict "k1" "v1" "k2" "v2"`. |
| `fail` | Abort template rendering with an error message. |
| `index` | Go built-in for nested map/slice access. |
| `printf` | Go built-in for formatted strings. |

### 2. Convert Existing Templates

The old Sveltos-format templates used `getResource` to fetch identity objects.
With direct projection, these objects are available as top-level template fields.
However, `getResource` is also supported for backward compatibility.

**Minimal changes required:**

The old `projectsveltos.io/template: "true"` annotation is no longer needed (it
is ignored by the direct projector). Templates that use `getResource` with the
old Sveltos identifiers continue to work via backward-compatible aliases inside
the `getResource` function:

| Old `getResource` identifier | Alias for |
|------------------------------|-----------|
| `InfrastructureProviderIdentity` | `.Identity` |
| `InfrastructureProviderIdentitySecret` | `.IdentitySecret` |

Note: these aliases apply **only** to `getResource` calls. The old names are
**not** available as direct top-level template fields (`.InfrastructureProviderIdentity`
would evaluate to an empty/zero value at render time). If your template uses
direct field access (e.g. `{{- $identity := .InfrastructureProviderIdentity -}}`),
rename to the new canonical field names shown in the table above.

**Example — vSphere (old Sveltos format works as-is):**

```yaml
{{- $cluster := .InfrastructureProvider -}}
{{- $identity := (getResource "InfrastructureProviderIdentity") -}}
{{- $secret := (getResource "InfrastructureProviderIdentitySecret") -}}
apiVersion: v1
kind: Secret
metadata:
  name: vsphere-cloud-secret
  namespace: kube-system
type: Opaque
data:
  {{ printf "%s.username" $cluster.spec.server }}: {{ index $secret.data "username" }}
  {{ printf "%s.password" $cluster.spec.server }}: {{ index $secret.data "password" }}
```

> **Note:** The `$$` you see in `config/dev/*.yaml` files is Makefile escaping —
> the actual templates use single `$` for Go template variables.

**Recommended new style (using direct field access):**

```yaml
{{- $cluster := .InfrastructureProvider -}}
{{- $identity := .Identity -}}
{{- $secret := .IdentitySecret -}}
apiVersion: v1
kind: Secret
metadata:
  name: vsphere-cloud-secret
  namespace: kube-system
type: Opaque
data:
  {{ printf "%s.username" $cluster.spec.server }}: {{ index $secret.data "username" }}
  {{ printf "%s.password" $cluster.spec.server }}: {{ index $secret.data "password" }}
```

### 3. Update the Credential Object

Add `projectionConfig` to the Credential spec. You can reference an external
ConfigMap via `resourceTemplateRef`, or embed the template inline via
`resourceTemplate` (the two are mutually exclusive). If the identity has a
companion Secret, reference it explicitly with `secretDataRef`.

**Using a ConfigMap reference:**

```yaml
apiVersion: k0rdent.mirantis.com/v1beta1
kind: Credential
metadata:
  name: vsphere-cluster-identity-cred
  namespace: kcm-system
spec:
  identityRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
    kind: VSphereClusterIdentity
    name: vsphere-cluster-identity
    namespace: kcm-system
  projectionConfig:
    secretDataRef:
      name: vsphere-cluster-identity-secret
    resourceTemplateRef:
      name: vsphere-cluster-identity-resource-template
```

**Using an inline template:**

```yaml
apiVersion: k0rdent.mirantis.com/v1beta1
kind: Credential
metadata:
  name: aws-cluster-identity-cred
  namespace: kcm-system
spec:
  identityRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
    kind: AWSClusterStaticIdentity
    name: aws-cluster-identity
  projectionConfig:
    secretDataRef:
      name: aws-cluster-identity-secret
    resourceTemplate: |
      apiVersion: v1
      kind: Secret
      metadata:
        name: aws-cloud-credentials
        namespace: kube-system
      type: Opaque
      stringData:
        credentials: |
          [default]
          aws_access_key_id = {{ index .IdentitySecret "data" "AccessKeyID" }}
          aws_secret_access_key = {{ index .IdentitySecret "data" "SecretAccessKey" }}
```

Once `projectionConfig` is set, the `ClusterDeployment` controller will:

1. Wait for the CAPI Cluster and infrastructure provider to be ready.
2. Render the referenced ConfigMap templates with the identity data.
3. Apply the rendered manifests to the child cluster using server-side apply.
4. Set `CredentialsProjected=True` condition before deploying services.

### 4. Verify

Check the `ClusterDeployment` status conditions:

```bash
kubectl get clusterdeployment <name> -o jsonpath='{.status.conditions}' | jq '.[] | select(.type=="CredentialsProjected")'
```

Expected output after successful projection:

```json
{
  "type": "CredentialsProjected",
  "status": "True",
  "reason": "Succeeded"
}
```

## Rollback

To revert to the Sveltos-based approach, remove the `projectionConfig` field
from the Credential spec. The next reconciliation will use the legacy path
automatically.

## Deprecation Timeline

The legacy Sveltos-based credential propagation path is deprecated and will be
removed in a future release.  Plan to migrate all Credentials to use
`projectionConfig` within the next two minor releases.

## Template Function Compatibility Notes

The following Sveltos-specific functions are **not** available in the direct
projection engine:

| Sveltos Function | Alternative |
|-------------------|-------------|
| `getField` | Use Go template `index` for nested access: `index .Object "spec" "field"` |
| `setField` / `chainSetField` | Not available — templates should produce final-form YAML. |
| `removeField` / `chainRemoveField` | Not available — omit unwanted fields from the template. |
| `copy` | Not needed — template data is read-only. |
| Lua scripting | Not available — use Go template control flow (`if`, `range`, `with`). |

Functions that **are** available and compatible with Sveltos templates:
`getResource`, `b64dec`, `b64enc`, `hasKey`, `default`, `fromYaml`, `toYaml`,
`toJson`, `dict`, `fail`, `index`, `printf`, `range`, `if`, `with`.
