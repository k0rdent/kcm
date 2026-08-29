# Proposal for showing services status in MultiClusterService

## Current MCS Status

Currently (v1.9.0) the MCS object shows the following in its status:
1. The clusters it matches.
2. A boolean value to indicate whether the services defined in its spec have been deployed on each matching cluster.
2. The service upgrade paths for each of the services defined in its spec.

Consider the example below showing the information mentioned above. This MulticlusterService `mcs0` has 2 services defined in its spec and it matches 3 clusters (2 ClusterDeployments and 1 SveltosCluster).

```yaml
apiVersion: k0rdent.mirantis.com/v1beta1
kind: MultiClusterService
metadata:
  . . .
  name: mcs0
spec:
  clusterSelector:
    matchLabels:
      owner: dev-team
  serviceSpec:
    continueOnError: false
    priority: 100
    provider:
      config:
        continueOnError: true
        priority: 100
      name: ksm-projectsveltos
      selfManagement: true
    services:
    - name: nginx
      namespace: nginx
      template: ingress-nginx-4-13-0
    - name: hello
      namespace: hello
      template: hello-world
    stopOnConflict: false
    syncMode: Continuous
status:
  conditions:
    . . .
  # 1. The list of matching clusters.
  matchingClusters:
  - apiVersion: k0rdent.mirantis.com/v1beta1
    # 2. The deployed field indicating that all services were deployed on this cluster.
    deployed: true
    kind: ClusterDeployment
    lastTransitionTime: "2026-05-20T21:36:35Z"
    name: wali-dev-0
    namespace: kcm-system
    regional: false
  - apiVersion: k0rdent.mirantis.com/v1beta1
    deployed: true
    kind: ClusterDeployment
    lastTransitionTime: "2026-05-20T20:31:14Z"
    name: wali-dev-1
    namespace: kcm-system
    regional: false
  - apiVersion: lib.projectsveltos.io/v1beta1
    deployed: true
    kind: SveltosCluster
    lastTransitionTime: "2026-05-20T21:34:55Z"
    name: mgmt
    namespace: mgmt
    regional: false
  observedGeneration: 4
  # 3. The list of service update paths for each service.
  servicesUpgradePaths:
  - availableUpgrades:
    - versions:
      - name: ingress-nginx-4-13-0
        version: ingress-nginx-4-13-0
    name: nginx
    namespace: nginx
    template: ingress-nginx-4-13-0
  - availableUpgrades:
    - versions:
      - name: hello-world
        version: hello-world
    name: hello
    namespace: hello
    template: hello-world
```

So currently the MCS status is not capable of showing the status for each of the services on the clusters it matches. For that the user will have to either query the ClusterDeployment or the relevant ServiceSet object. 

For example, the `wali-dev-0` ClusterDeployment's status shows the status of both the services deployed via MCS (i.e., `ingres-nginx` and `hello-world`) which is taken from the relevant ServiceSet as well as the status of the service it itself deploys (i.e., `postgres-operator`).

```yaml
apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterDeployment
metadata:
  . . .
  labels:
    k0rdent.mirantis.com/component: kcm
    owner: dev-team
  name: wali-dev-0
  namespace: kcm-system
spec:
  . . .
  serviceSpec:
    continueOnError: false
    priority: 100
    provider:
      name: ksm-projectsveltos
    services:
    - name: postrges-operator
      namespace: postrges-operator
      template: postgres-operator-1-15-1
    stopOnConflict: false
    syncMode: Continuous
  template: aws-standalone-cp-1-0-29
status:
  . . .
  services:
  - lastStateTransitionTime: "2026-05-20T20:31:04Z"
    name: nginx
    namespace: nginx
    state: Deployed
    template: ingress-nginx-4-13-0
    type: Helm
    version: 4.13.0
  - lastStateTransitionTime: "2026-05-20T21:43:35Z"
    name: postrges-operator
    namespace: postrges-operator
    state: Deployed
    template: postgres-operator-1-15-1
    type: Helm
    version: 1.15.1
  - lastStateTransitionTime: "2026-05-20T21:36:35Z"
    name: hello
    namespace: hello
    state: Deployed
    template: hello-world
    type: Kustomize
    version: hello-world
  servicesUpgradePaths:
  - availableUpgrades:
    - versions:
      - name: postgres-operator-1-15-1
        version: postgres-operator-1-15-1
    name: postrges-operator
    namespace: postrges-operator
    template: postgres-operator-1-15-1
```

Similarly for SveltosCluster, we can query the `management-*` ServiceSet to check the detailed status of the services deployed on the management cluster.

```yaml
apiVersion: k0rdent.mirantis.com/v1beta1
kind: ServiceSet
metadata:
  . . .
  name: management-6ec16280
  namespace: kcm-system
spec:
  cluster: ""
  multiClusterService: mcs0
  provider:
    config:
      continueOnError: true
      priority: 100
    name: ksm-projectsveltos
    selfManagement: true
  services:
  - name: hello
    namespace: hello
    template: hello-world
    version: hello-world
  - name: nginx
    namespace: nginx
    template: ingress-nginx-4-13-0
    version: 4.13.0
status:
  cluster:
    apiVersion: lib.projectsveltos.io/v1beta1
    kind: SveltosCluster
    name: mgmt
    namespace: mgmt
  conditions:
    . . .
  deployed: true
  provider:
    ready: true
  services:
  - lastStateTransitionTime: "2026-05-20T21:36:19Z"
    name: hello
    namespace: hello
    state: Deployed
    template: hello-world
    type: Kustomize
    version: hello-world
  - lastStateTransitionTime: "2026-05-20T21:34:55Z"
    name: nginx
    namespace: nginx
    state: Deployed
    template: ingress-nginx-4-13-0
    type: Helm
    version: 4.13.0
```

## Include Service Status in MCS Status

One way to include the status of each service per matched cluster in the MCS would be to add each service's status to `.status.matchingClusters.services[]`. For example the status of `mcs0` in this scenario would become the following.

```yaml
apiVersion: k0rdent.mirantis.com/v1beta1
kind: MultiClusterService
metadata:
  . . .
  name: mcs0
spec:
  clusterSelector:
    matchLabels:
      owner: dev-team
  serviceSpec:
    . . .
status:
  conditions:
    . . .
  matchingClusters:
  - apiVersion: k0rdent.mirantis.com/v1beta1
    deployed: true
    kind: ClusterDeployment
    lastTransitionTime: "2026-05-20T21:36:35Z"
    name: wali-dev-0
    namespace: kcm-system
    regional: false
    services:
    - lastStateTransitionTime: "2026-05-20T20:31:04Z"
      name: nginx
      namespace: nginx
      state: Deployed
      template: ingress-nginx-4-13-0
      type: Helm
      version: 4.13.0
    - lastStateTransitionTime: "2026-05-20T21:36:35Z"
      name: hello
      namespace: hello
      state: Deployed
      template: hello-world
      type: Kustomize
      version: hello-world
  - apiVersion: k0rdent.mirantis.com/v1beta1
    deployed: true
    kind: ClusterDeployment
    lastTransitionTime: "2026-05-20T20:31:14Z"
    name: wali-dev-1
    namespace: kcm-system
    regional: false
    services:
    - lastStateTransitionTime: "2026-05-20T20:31:14Z"
      name: nginx
      namespace: nginx
      state: Deployed
      template: ingress-nginx-4-13-0
      type: Helm
      version: 4.13.0
    - lastStateTransitionTime: "2026-05-20T21:36:19Z"
      name: hello
      namespace: hello
      state: Deployed
      template: hello-world
      type: Kustomize
      version: hello-world
  - apiVersion: lib.projectsveltos.io/v1beta1
    deployed: true
    kind: SveltosCluster
    lastTransitionTime: "2026-05-20T21:34:55Z"
    name: mgmt
    namespace: mgmt
    regional: false
    services:
    - lastStateTransitionTime: "2026-05-20T21:36:19Z"
      name: hello
      namespace: hello
      state: Deployed
      template: hello-world
      type: Kustomize
      version: hello-world
    - lastStateTransitionTime: "2026-05-20T21:34:55Z"
      name: nginx
      namespace: nginx
      state: Deployed
      template: ingress-nginx-4-13-0
      type: Helm
      version: 4.13.0
  observedGeneration: 4
  servicesUpgradePaths:
  - availableUpgrades:
    - versions:
      - name: ingress-nginx-4-13-0
        version: ingress-nginx-4-13-0
    name: nginx
    namespace: nginx
    template: ingress-nginx-4-13-0
  - availableUpgrades:
    - versions:
      - name: hello-world
        version: hello-world
    name: hello
    namespace: hello
    template: hello-world
```

## Optimization

To reduce the size of the MCS object, we could optimize how the status of each service is shown in the MCS status. The purpose in the MCS object is to give a summary of the service status so we can omit certain fields which can be read by querying relevant ServiceSets if desired.

## Proposal 1

We can remove the `lastStateTransitionTime`, `template` and `type` and concatenate `name` and `namespace` into a single field like the following.

```yaml
. . .
  - apiVersion: k0rdent.mirantis.com/v1beta1
    deployed: true
    kind: ClusterDeployment
    lastTransitionTime: "2026-05-20T20:31:14Z"
    name: wali-dev-1
    namespace: kcm-system
    regional: false
    services:
    - namespacedName: nginx/nginx
      state: Deployed
      version: 4.13.0
    - namespacedName: hello/hello
      state: Deployed
      version: hello-world
. . .
```

## Proposal 2

If we want to further reduce size, we can even concatenate `version` with `namespacedName` like the following.

```yaml
. . .
  - apiVersion: k0rdent.mirantis.com/v1beta1
    deployed: true
    kind: ClusterDeployment
    lastTransitionTime: "2026-05-20T20:31:14Z"
    name: wali-dev-1
    namespace: kcm-system
    regional: false
    services:
    # Maybe a better name than `namespacedNameVersion` can be used?
    - namespacedNameVersion: nginx/nginx@4.13.0
      state: Deployed
    - namespacedNameVersion: hello/hello@hello-world
      state: Deployed
. . .
```

## Proposal 3

```yaml
. . .
  - apiVersion: k0rdent.mirantis.com/v1beta1
    deployed: true
    kind: ClusterDeployment
    lastTransitionTime: "2026-05-20T20:31:14Z"
    name: wali-dev-1
    namespace: kcm-system
    regional: false
    readyServices: 11/20
. . .
```


## Pros & Cons
Pros:
* Able to see status of individual services (Provisioning, Deployed, Failed, etc) without querying underlying ServiceSet.

Cons:
* With lots of matching cluster and services, the object could become very large and exceed size limit. 

## Estimates

Since the MCS size can vary due to different length of values such as names and the number of services and matching clusters, therefore this is only a rough estimate to see how each of the proposals compare in size relative to each other. Consider the following.

MCS with 0 services & 0 matching clusters:
```yaml
apiVersion: k0rdent.mirantis.com/v1beta1
kind: MultiClusterService
metadata:
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"k0rdent.mirantis.com/v1beta1","kind":"MultiClusterService","metadata":{"annotations":{},"name":"mcs1"},"spec":{"clusterSelector":{"matchLabels":{"owner":"dev-team"}},"serviceSpec":{"provider":{"name":"ksm-projectsveltos"},"services":[{"name":"nginx","namespace":"nginx","template":"ingress-nginx-4-13-0"}]}}}
  creationTimestamp: "2026-06-10T16:18:28Z"
  finalizers:
  - k0rdent.mirantis.com/multicluster-service
  generation: 4
  labels:
    k0rdent.mirantis.com/component: kcm
  name: mcs1
  resourceVersion: "46542"
  uid: ea51c362-0419-41d2-afc3-8638de44fd96
spec:
  clusterSelector:
    matchLabels:
      owner: dev-team
  keepServicesOnSelectorMismatch: false
  serviceSpec:
    continueOnError: false
    priority: 100
    provider:
      name: ksm-projectsveltos
    services:

    stopOnConflict: false
    syncMode: Continuous
status:
  conditions:
  - lastTransitionTime: "2026-06-10T16:18:48Z"
    message: ""
    observedGeneration: 4
    reason: Succeeded
    status: "True"
    type: ServicesReferencesValidation
  - lastTransitionTime: "2026-06-10T16:18:48Z"
    message: ""
    observedGeneration: 4
    reason: Succeeded
    status: "True"
    type: ServicesDependencyValidation
  - lastTransitionTime: "2026-06-10T16:18:48Z"
    message: ""
    observedGeneration: 4
    reason: Succeeded
    status: "True"
    type: MultiClusterServiceDependencyValidation
  - lastTransitionTime: "2026-06-10T21:46:38Z"
    message: 1/1
    reason: Succeeded
    status: "True"
    type: ClusterInReadyState
  - lastTransitionTime: "2026-06-10T21:46:38Z"
    message: Object is ready
    observedGeneration: 4
    reason: Succeeded
    status: "True"
    type: Ready
  matchingClusters:

  observedGeneration: 4
  servicesUpgradePaths:

```
Each service in spec. We'll make the assumption that services will not be defined with their values because that can vary and of course their names and namespaces as well:
```yaml
    - name: nginx
      namespace: nginx
      template: ingress-nginx-4-13-0
```

Service upgrade path in status assuming it only has itself as an upgrade:
```yaml
  - availableUpgrades:
    - versions:
      - name: ingress-nginx-4-13-0
        version: ingress-nginx-4-13-0
    name: nginx
    namespace: nginx
    template: ingress-nginx-4-13-0
```

Each matching cluster in status:
```yaml
  - apiVersion: k0rdent.mirantis.com/v1beta1
    deployed: true
    kind: ClusterDeployment
    lastTransitionTime: "2026-06-10T21:46:38Z"
    name: wali-dev-1
    namespace: kcm-system
    regional: false
    services:

```

Proposal 1 per matching cluster in status:
```yaml
    - namespacedName: nginx/nginx
      state: Deployed
      version: 4.13.0
```

Proposal 2 per matching cluster in status:
```yaml
    - namespacedNameVersion: nginx/nginx@4.13.0
      state: Deployed
```

Proposal 3 in status:
```yaml
    readyServices: 9999/9999
```

So we have the following values:
```
Max size of oject allowed by Kubernetes API (1 MB)  = 1048576 bytes
Size of MCS with 0 services & 0 matching clusters   = 1910
Size of each service in spec                        = 78
Size of Service upgrade path in status              = 184
Size of each matching cluster is status             = 220
Size of Proposal 1 per matching cluster in status   = 77
Size of Proposal 2 per matching cluster in status   = 69
Size of Proposal 3 in status                        = 28
```

Relatively speaking everything else being equal Proposal 2 will be ~10% more compact in size than Proposal 1, But let's roughly estimate how how many services with matching clusters we can define with each Proposal.

From the sizes above we get the following equations where `s = no of services` and `c = no of clusters`:
```
Proposal 1: 1910 + 78s + 184s + 220c + 77sc = 1048576
Proposal 2: 1910 + 78s + 184s + 220c + 69sc = 1048576
Proposal 3: 1910 + 78s + 184s + 220c + 28   = 1048576
```

If we assume the number of services and matching clusters to be equal for estimation purposes then the equations can be simplified to:
```
Proposal 1:
1910 + 78x + 184x + 220x + 77x^2 = 1048576
=> 77x^2 + 482x - 1046483 = 0
=> x = (-482 +- sqrt(482^2 - 4*77*(-1046483)))/2*77
=> x = 113.50 OR -119.76

Proposal 2:
1910 + 78x + 184x + 220x + (69x^2) = 1048576
=> 69x^2 + 482x - 1046483 = 0
=> x = 119.72 OR -126.71

Proposal 3:
1910 + 78x + 184x + 220x + 28 = 1048576
=> 482x = 1048576 - 1910 - 28
=> x = 2171.45
```

With these assumptions for Proposal 1 we would be able to define ~113 services to ~113 matching clusters and similarly ~119 for Proposal 2 and ~2171 for proposal 3 before hitting the 1MB size limit.
