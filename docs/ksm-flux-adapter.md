# The Flux state management adapter

KCM ships two built-in KSM adapters. The default one translates a `ServiceSet`
into ProjectSveltos `Profile` objects; this one translates it into Flux
`HelmRelease` objects, one per Helm service.

Both run inside `kcm-controller-manager`, so the adapter reference in the
`StateManagementProvider` cannot tell them apart. The
`ksm.k0rdent.mirantis.com/adapter-kind` label on the provider does; a provider
without it is served by the sveltos adapter, which is what every existing
installation has.

## Turning it on

```yaml
controller:
  enableFluxCtrl: true
  # Optional: route ServiceSets that name no provider here as well.
  defaultStateManagementProvider: ksm-flux
```

`enableFluxCtrl` starts the controller and makes the Management controller
create a `ksm-flux` StateManagementProvider whose provisioners are the
`helm-controller` and `source-controller` Deployments. Name it per
`MultiClusterService`:

```yaml
spec:
  serviceSpec:
    provider:
      name: ksm-flux
```

## What it produces

One `HelmRelease` per service, in the `ServiceSet`'s namespace on the
management cluster — next to the Flux sources KCM already maintains for the
`ServiceTemplate`s, which is why it cannot live anywhere else. A service that
targets a child cluster is reconciled remotely through
`.spec.kubeConfig.secretRef`, pointing at that cluster's CAPI kubeconfig
Secret. The release itself (chart, storage, namespace) lands on the target
cluster exactly where the sveltos adapter would have put it.

`spec.provider.config` takes four optional keys: `interval`,
`serviceAccountName`, `maxHistory` and `driftDetectionMode`
(`enabled`/`warn`). Anything else in it is ignored, which is deliberate — KCM
fills the config with sveltos-shaped keys when the user sets none.

## Limits

- Helm services only. A `ServiceTemplate` backed by `kustomize` or `resources`
  fails the `ServiceSet` with an explicit message: KCM installs Flux without
  kustomize-controller.
- No regional clusters. The sources live on the management cluster, and a
  `HelmRelease` can only reference a source next to it.
