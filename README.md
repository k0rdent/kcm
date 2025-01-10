# Mirantis Hybrid Multi Cluster (HMC), Codename: Project 0x2A

## Overview

Mirantis Hybrid Multi Cluster is part of Mirantis Project 0x2A which is focused
on delivering a open source approach to providing an enterprise grade
multi-cluster kubernetes management solution based entirely on standard open
source tooling that works across private or public clouds.

We like to say that Project 0x2A (42) is the answer to life, the universe, and
everything ...  Or, at least, the Kubernetes sprawl we find ourselves faced with
in real life!

## Documentation

Detailed documentation is available in [K0rdent Docs](https://k0rdent.github.io/docs)

## Installation

### TL;DR

```bash
kubectl apply -f https://github.com/K0rdent/kcm/releases/download/v0.0.6/install.yaml
```

or install using `helm`

```bash
helm install hmc oci://ghcr.io/k0rdent/kcm/charts/hmc --version 0.0.6 -n hmc-system --create-namespace
```

Then follow the [Deploy a cluster deployment](#create-a-clusterdeployment) guide to
create a cluster deployment.

> [!NOTE]
> The HMC installation using Kubernetes manifests does not allow
> customization of the deployment. To apply a custom HMC configuration, install
> HMC using the Helm chart.

### Development guide

See [Install HMC for development purposes](docs/dev.md#hmc-installation-for-development).

### Software Prerequisites

Mirantis Hybrid Container Cloud requires the following:

1. Existing management cluster (minimum required kubernetes version 1.28.0).
2. `kubectl` CLI installed locally.

Optionally, the following CLIs may be helpful:

1. `helm` (required only when installing HMC using `helm`).
2. `clusterctl` (to handle the lifecycle of the cluster deployments).

### Providers configuration

Full details on the provider configuration can be found in the Project 2A Docs,
see [Documentation](#documentation)

### Installation

```
export KUBECONFIG=<path-to-management-kubeconfig>

helm install hmc oci://ghcr.io/k0rdent/kcm/charts/hmc --version <hmc-version> -n hmc-system --create-namespace
```

#### Extended Management configuration

By default, the Hybrid Container Cloud is being deployed with the following
configuration:

```yaml
apiVersion: k0rdent.mirantis.com/v1alpha1
kind: Management
metadata:
  name: hmc
spec:
  providers:
  - name: k0smotron
  - name: cluster-api-provider-aws
  - name: cluster-api-provider-azure
  - name: cluster-api-provider-vsphere
  - name: projectsveltos
  release: hmc-0-0-5
```

There are two options to override the default management configuration of HMC:

1. Update the `Management` object after the HMC installation using `kubectl`:

    `kubectl --kubeconfig <path-to-management-kubeconfig> edit management`

2. Deploy HMC skipping the default `Management` object creation and provide your
own `Management` configuration:

   * Create `management.yaml` file and configure core components and providers.
   See [Management API](api/v1alpha1/management_types.go).

   * Specify `--create-management=false` controller argument and install HMC:

    If installing using `helm` add the following parameter to the `helm install`
    command:

    `--set="controller.createManagement=false"`

   * Create `hmc` `Management` object after HMC installation:

    `kubectl --kubeconfig <path-to-management-kubeconfig> create -f management.yaml`

## Create a ClusterDeployment

To create a ClusterDeployment:

1. Create `Credential` object with all credentials required.

   See [Credential system docs](https://k0rdent.github.io/docs/credential/main)
   for more information regarding this object.

2. Select the `ClusterTemplate` you want to use for the deployment. To list all
   available templates, run:

```bash
export KUBECONFIG=<path-to-management-kubeconfig>

kubectl get clustertemplate -n hmc-system
```

If you want to deploy hosted control plane template, make sure to check
additional notes on Hosted control plane in 2A Docs, see
[Documentation](#documentation).

2. Create the file with the `ClusterDeployment` configuration:

> [!NOTE]
> Substitute the parameters enclosed in angle brackets with the corresponding
> values. Enable the `dryRun` flag if required.
> For details, see [Dryrun](#dry-run).

```yaml
apiVersion: k0rdent.mirantis.com/v1alpha1
kind: ClusterDeployment
metadata:
  name: <cluster-name>
  namespace: <cluster-namespace>
spec:
  template: <template-name>
  credential: <credential-name>
  dryRun: <true/false>
  config:
    <cluster-configuration>
```

3. Create the `ClusterDeployment` object:

`kubectl create -f clusterdeployment.yaml`

4. Check the status of the newly created `ClusterDeployment` object:

`kubectl -n <clusterdeployment-namespace> get ClusterDeployment <clusterdeployment-name> -o=yaml`

5. Wait for infrastructure to be provisioned and the cluster to be deployed (the
provisioning starts only when `spec.dryRun` is disabled):

```bash
kubectl -n <clusterdeployment-namespace> get cluster <clusterdeployment-name> -o=yaml
```

> [!NOTE]
> You may also watch the process with the `clusterctl describe` command
> (requires the `clusterctl` CLI to be installed): ``` clusterctl describe
> cluster <clusterdeployment-name> -n <clusterdeployment-namespace> --show-conditions
> all ```

6. Retrieve the `kubeconfig` of your cluster deployment:

```
kubectl get secret -n hmc-system <clusterdeployment-name>-kubeconfig -o=jsonpath={.data.value} | base64 -d > kubeconfig
```

### Dry run

HMC `ClusterDeployment` supports two modes: with and without (default) `dryRun`.

If no configuration (`spec.config`) provided, the `ClusterDeployment` object will
be populated with defaults (default configuration can be found in the
corresponding `Template` status) and automatically marked as `dryRun`.

Here is an example of the `ClusterDeployment` object with default configuration:

```yaml
apiVersion: k0rdent.mirantis.com/v1alpha1
kind: ClusterDeployment
metadata:
  name: <cluster-name>
  namespace: <cluster-namespace>
spec:
  config:
    clusterNetwork:
      pods:
        cidrBlocks:
        - 10.244.0.0/16
      services:
        cidrBlocks:
        - 10.96.0.0/12
    controlPlane:
      iamInstanceProfile: control-plane.cluster-api-provider-aws.sigs.k8s.io
      instanceType: ""
    controlPlaneNumber: 3
    k0s:
      version: v1.27.2+k0s.0
    publicIP: false
    region: ""
    sshKeyName: ""
    worker:
      iamInstanceProfile: nodes.cluster-api-provider-aws.sigs.k8s.io
      instanceType: ""
    workersNumber: 2
  template: aws-standalone-cp-0-0-4
  credential: aws-credential
  dryRun: true
```

After you adjust your configuration and ensure that it passes validation
(`TemplateReady` condition from `status.conditions`), remove the `spec.dryRun`
flag to proceed with the deployment.

Here is an example of a `ClusterDeployment` object that passed the validation:

```yaml
apiVersion: k0rdent.mirantis.com/v1alpha1
kind: ClusterDeployment
metadata:
  name: aws-standalone
  namespace: hmc-system
spec:
  template: aws-standalone-cp-0-0-4
  credential: aws-credential
  config:
    region: us-east-2
    publicIP: true
    controlPlaneNumber: 1
    workersNumber: 1
    controlPlane:
      instanceType: t3.small
    worker:
      instanceType: t3.small
  status:
    conditions:
    - lastTransitionTime: "2024-07-22T09:25:49Z"
      message: Template is valid
      reason: Succeeded
      status: "True"
      type: TemplateReady
    - lastTransitionTime: "2024-07-22T09:25:49Z"
      message: Helm chart is valid
      reason: Succeeded
      status: "True"
      type: HelmChartReady
    - lastTransitionTime: "2024-07-22T09:25:49Z"
      message: ClusterDeployment is ready
      reason: Succeeded
      status: "True"
      type: Ready
    observedGeneration: 1
```

## Cleanup

1. Remove the Management object:

```bash
kubectl delete management.hmc hmc
```

> [!NOTE]
> Make sure you have no HMC ClusterDeployment objects left in the cluster prior to
> Management deletion

2. Remove the `hmc` Helm release:

```bash
helm uninstall hmc -n hmc-system
```

3. Remove the `hmc-system` namespace:

```bash
kubectl delete ns hmc-system
```
