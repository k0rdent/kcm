# HMC installation for development

Below is the example on how to install HMC for development purposes and create
a managed cluster on AWS with k0s for testing. The kind cluster acts as management in this example.

## Prerequisites

### Clone HMC repository

```bash
git clone https://github.com/K0rdent/kcm.git && cd kcm
make help # Check available commands
```

### Install required CLIs

Run:

```bash
make cli-install
```

### AWS Provider Setup

Follow the instruction to configure AWS Provider: [AWS Provider Setup](https://k0rdent.github.io/docs/quick-start/aws/)

The following env variables must be set in order to deploy dev cluster on AWS:

- `AWS_ACCESS_KEY_ID`: The access key ID for authenticating with AWS.
- `AWS_SECRET_ACCESS_KEY`: The secret access key for authenticating with AWS.

The following environment variables are optional but can enhance functionality:

- `AWS_SESSION_TOKEN`: Required only if using temporary AWS credentials.
- `AWS_REGION`: Specifies the AWS region in which to deploy resources. If not provided, defaults to `us-east-2`.

### Azure Provider Setup

Follow the instruction on how to configure [Azure Provider](https://k0rdent.github.io/docs/quick-start/azure/).

Additionally to deploy dev cluster on Azure the following env variables should
be set before running deployment:

- `AZURE_SUBSCRIPTION_ID` - Subscription ID
- `AZURE_TENANT_ID` - Service principal tenant ID
- `AZURE_CLIENT_ID` - Service principal App ID
- `AZURE_CLIENT_SECRET` - Service principal password

More detailed description of these parameters can be found
[here](azure/cluster-parameters.md).

### vSphere Provider Setup

Follow the instruction on how to configure [vSphere Provider](https://k0rdent.github.io/docs/quick-start/vsphere/).

To properly deploy dev cluster you need to have the following variables set:

- `VSPHERE_USER`
- `VSPHERE_PASSWORD`
- `VSPHERE_SERVER`
- `VSPHERE_THUMBPRINT`
- `VSPHERE_DATACENTER`
- `VSPHERE_DATASTORE`
- `VSPHERE_RESOURCEPOOL`
- `VSPHERE_FOLDER`
- `VSPHERE_CONTROL_PLANE_ENDPOINT`
- `VSPHERE_VM_TEMPLATE`
- `VSPHERE_NETWORK`
- `VSPHERE_SSH_KEY`

Naming of the variables duplicates parameters in `ManagementCluster`. To get
full explanation for each parameter visit
[vSphere cluster parameters](cluster-parameters.md) and
[vSphere machine parameters](machine-parameters.md).

### EKS Provider Setup

To properly deploy dev cluster you need to have the following variable set:

- `DEV_PROVIDER` - should be "eks"

- The rest of deployment procedure is the same as for other providers.

### Adopted Cluster Setup

To "adopt" an existing cluster first obtain the kubeconfig file for the cluster. 
Then set the `DEV_PROVIDER` to "adopted". Export the kubeconfig file as a variable by running the following:

`export KUBECONFIG_DATA=$(cat kubeconfig | base64 -w 0)`

The rest of deployment procedure is the same as for other providers.

## Deploy HMC

Default provider which will be used to deploy cluster is AWS, if you want to use
another provider change `DEV_PROVIDER` variable with the name of provider before
running make (e.g. `export DEV_PROVIDER=azure`).

1. Configure your cluster parameters in provider specific file
   (for example `config/dev/aws-clusterdeployment.yaml` in case of AWS):

    * Configure the `name` of the ClusterDeployment
    * Change instance type or size for control plane and worker machines
    * Specify the number of control plane and worker machines, etc

2. Run `make dev-apply` to deploy and configure management cluster.

3. Wait a couple of minutes for management components to be up and running.

4. Apply credentials for your provider by executing `make dev-creds-apply`.

5. Run `make dev-mcluster-apply` to deploy managed cluster on provider of your
   choice with default configuration.

6. Wait for infrastructure to be provisioned and the cluster to be deployed. You
   may watch the process with the `./bin/clusterctl describe` command. Example:

```bash
export KUBECONFIG=~/.kube/config

./bin/clusterctl describe cluster <clusterdeployment-name> -n hmc-system --show-conditions all
```

> [!NOTE]
> If you encounter any errors in the output of `clusterctl describe cluster` inspect the logs of the
> `capa-controller-manager` with:
> ```bash
> kubectl logs -n hmc-system deploy/capa-controller-manager
> ```
> This may help identify any potential issues with deployment of the AWS infrastructure.

7. Retrieve the `kubeconfig` of your managed cluster:

```bash
kubectl --kubeconfig ~/.kube/config get secret -n hmc-system <clusterdeployment-name>-kubeconfig -o=jsonpath={.data.value} | base64 -d > kubeconfig
```

## Running E2E tests locally
E2E tests can be ran locally via the `make test-e2e` target.  In order to have
CI properly deploy a non-local registry will need to be used and the Helm charts
and hmc-controller image will need to exist on the registry, for example, using
GHCR:

```
IMG="ghcr.io/k0rdent/kcm/controller-ci:v0.0.1-179-ga5bdf29" \
    REGISTRY_REPO="oci://ghcr.io/k0rdent/kcm/charts-ci" \
    make test-e2e
```

Optionally, the `NO_CLEANUP=1` env var can be used to disable `After` nodes from
running within some specs, this will allow users to debug tests by re-running
them without the need to wait a while for an infrastructure deployment to occur.
For subsequent runs the `CLUSTER_DEPLOYMENT_NAME=<cluster name>` env var should be
passed to tell the test what cluster name to use so that it does not try to
generate a new name and deploy a new cluster.

Tests that run locally use autogenerated names like `12345678-e2e-test` while
tests that run in CI use names such as `ci-1234567890-e2e-test`.  You can always
pass `CLUSTER_DEPLOYMENT_NAME=` from the get-go to customize the name used by the
test.

### Filtering test runs
Provider tests are broken into two types, `onprem` and `cloud`.  For CI,
`provider:onprem` tests run on self-hosted runners provided by Mirantis.
`provider:cloud` tests run on GitHub actions runners and interact with cloud
infrastructure providers such as AWS or Azure.

Each specific provider test also has a label, for example, `provider:aws` can be
used to run only AWS tests.  To utilize these filters with the `make test-e2e`
target pass the `GINKGO_LABEL_FILTER` env var, for example:

```bash
GINKGO_LABEL_FILTER="provider:cloud" make test-e2e
```

would run all cloud provider tests.  To see a list of all available labels run:

```bash
ginkgo labels ./test/e2e
```

### Nuke created resources
In CI we run `make dev-aws-nuke` to cleanup test resources, you can do so
manually with:

```bash
CLUSTER_NAME=example-e2e-test make dev-aws-nuke
```

## Credential propagation

The following is the notes on provider specific CCM credentials delivery process

### Azure

Azure CCM/CSI controllers expect well-known `azure.json` to be provided though
Secret or by placing it on host file system.

The 2A controller will create Secret named `azure-cloud-provider` in the
`kube-system` namespace (where all controllers reside). The name is passed to
controllers via helm values.

The `azure.json` parameters are documented in detail in the
[official docs](https://cloud-provider-azure.sigs.k8s.io/install/configs)

Most parameters are obtained from CAPZ objects. Rest parameters are either
omitted or set to sane defaults.

### vSphere

#### CCM

cloud-provider-vsphere expects configuration to be passed in ConfigMap. The
credentials are located in the secret which is referenced in the configuration.

The config itself is a yaml file and it's not very well documented (the
[spec docs](https://github.com/kubernetes/cloud-provider-vsphere/blob/master/docs/book/cloud_config.md)
haven't been updated for years).

Most options however has similar names and could be inferred.

All optional parameters are omitted in the configuration created by 2A
controller.

Some options are hardcoded (since values are hard/impossible to get from CAPV
objects). For example:

- `insecureFlag` is set to `true` to omit certificate management parameters. This
  is also a default in the official charts since most vcenters are using
  self-signed or signed by internal authority certificates.
- `port` is set to `443` (HTTPS)
- [Multi-vcenter](https://cloud-provider-vsphere.sigs.k8s.io/tutorials/deploying_cpi_with_multi_dc_vc_aka_zones.html)
  labels are set to default values of region and zone (`k8s-region` and
  `k8s-zone`)

#### CSI

CSI expects single Secret with configuration in `ini` format
([documented here](https://docs.vmware.com/en/VMware-vSphere-Container-Storage-Plug-in/2.0/vmware-vsphere-csp-getting-started/GUID-BFF39F1D-F70A-4360-ABC9-85BDAFBE8864.html)).
Options are similar to CCM and same defaults/considerations are applicable.

## Generating the airgap bundle
Use the `make airgap-package` target to manually generate the airgap bundle,
to ensure the correctly tagged HMC controller image is present in the bundle
prefix the `IMG` env var with the desired image, for example:

```bash
IMG="ghcr.io/k0rdent/kcm:0.0.4" make airgap-package
```

Not setting an `IMG` var will use the default image name/tag generated by the
Makefile.
