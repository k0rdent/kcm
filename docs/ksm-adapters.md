# The built-in state management adapters

KCM ships three KSM adapters. Each turns a `ServiceSet` into whatever its
provider deploys from:

| adapter | `StateManagementProvider` | produces |
|---|---|---|
| sveltos | `ksm-projectsveltos` | ProjectSveltos `Profile` / `ClusterProfile` |
| flux | `ksm-flux` | Flux `HelmRelease`, one per service |
| argo | `ksm-argocd` | Argo CD `Application`, one per service |

Sveltos is the default and the only one that is on out of the box. All three
run inside `kcm-controller-manager`, so the adapter reference in the
`StateManagementProvider` cannot tell them apart; the
`ksm.k0rdent.mirantis.com/adapter-kind` label on the provider does. A provider
without that label is served by the sveltos adapter, which is what every
existing installation has.

A `ServiceSet` reaches one by naming it:

```yaml
spec:
  serviceSpec:
    provider:
      name: ksm-flux
```

or, for a whole cluster at once, through
`controller.defaultStateManagementProvider`, which decides where a `ServiceSet`
that names no provider goes.

## Flux

```yaml
controller:
  enableFluxCtrl: true
```

One `HelmRelease` per service, in the `ServiceSet`'s namespace on the
management cluster — next to the Flux sources KCM already maintains for the
`ServiceTemplate`s, which is why it cannot live anywhere else. A service that
targets a child cluster is reconciled remotely through
`.spec.kubeConfig.secretRef`, pointing at that cluster's CAPI kubeconfig
Secret. The release itself (chart, storage, namespace) lands on the target
cluster exactly where the sveltos adapter would have put it.

`spec.provider.config` takes four optional keys: `interval`,
`serviceAccountName`, `maxHistory` and `driftDetectionMode`
(`enabled`/`warn`).

The provisioners are the `helm-controller` and `source-controller` Deployments
KCM installs as part of its own chart, so nothing else has to be deployed.

## Argo CD

```yaml
controller:
  enableArgoCDCtrl: true
  # Where Argo CD runs; its Applications have to be created there.
  argoCDNamespace: argocd
```

One `Application` per service, in Argo CD's namespace — that is where Argo CD
looks for them. They therefore cannot carry an owner reference to the
`ServiceSet`, which lives in its own namespace, so the adapter labels them with
it and deletes them itself. Each one carries Argo CD's
`resources-finalizer.argocd.argoproj.io`, so tearing a `ServiceSet` down takes
the deployed resources with it rather than orphaning them.

Argo CD is **not** shipped by KCM. It has to be installed separately, and the
`ksm-argocd` provider stays not-ready until its `argocd-application-controller`
StatefulSet and `argocd-repo-server` Deployment are up in `argoCDNamespace`.

An OCI chart repository is registered with Argo CD on the fly, as a labelled
Secret in the same namespace: Argo CD reads an unregistered OCI URL as a plain
Helm repository and fails to pull. Plain HTTP(S) repositories need no entry.

`spec.provider.config` takes `project`, `syncOptions`, `selfHeal`, `prune` and
`retryLimit`.

Two differences from the other two adapters are worth knowing:

- Argo CD renders a chart and applies the result; there is no Helm release on
  the target cluster and no `helm list` to find it. Scenarios that assert on
  Helm releases see nothing.
- It has no `valuesFrom`, so the adapter resolves the referenced ConfigMaps and
  Secrets itself and merges them into one values document, in Helm's order:
  later wins.

## Limits of both new adapters

- Helm services only. A `ServiceTemplate` backed by `kustomize` or `resources`
  fails the `ServiceSet` with an explicit message.
- No regional clusters. Flux sources live on the management cluster, and
  Argo CD is registered against the clusters it knows; neither follows a
  `ServiceSet` to a regional cluster yet.
