# KCM installation for development

Below is the example on how to install KCM for development purposes and create
a managed cluster on AWS with k0s for testing. The kind cluster acts as management in this example.

## Prerequisites

### Clone KCM repository

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

Follow the instruction to configure AWS Provider: [AWS Provider Setup](https://docs.k0rdent.io/latest/quickstart-2-aws/)

The following env variables must be set in order to deploy dev cluster on AWS:

- `AWS_ACCESS_KEY_ID`: The access key ID for authenticating with AWS.
- `AWS_SECRET_ACCESS_KEY`: The secret access key for authenticating with AWS.

The following environment variables are optional but can enhance functionality:

- `AWS_SESSION_TOKEN`: Required only if using temporary AWS credentials.
- `AWS_REGION`: Specifies the AWS region in which to deploy resources. If not provided, defaults to `us-east-2`.

### Azure Provider Setup

Follow the instruction on how to configure [Azure Provider](https://docs.k0rdent.io/latest/quickstart-2-azure/).

Additionally to deploy dev cluster on Azure the following env variables should
be set before running deployment:

- `AZURE_SUBSCRIPTION_ID` - Subscription ID
- `AZURE_TENANT_ID` - Service principal tenant ID
- `AZURE_CLIENT_ID` - Service principal App ID
- `AZURE_CLIENT_SECRET` - Service principal password

More detailed description of these parameters can be found
[here](azure/cluster-parameters.md).

### vSphere Provider Setup

Follow the instruction on how to configure [vSphere Provider](https://docs.k0rdent.io/latest/admin-prepare/#vsphere).

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

### OpenStack Provider Setup

To deploy a development cluster on OpenStack, first set:

- `DEV_PROVIDER` - should be "openstack"

We recommend using OpenStack Application Credentials as it enhances security by allowing
applications to authenticate with limited, specific permissions without exposing the user's password.

- `OS_AUTH_URL`
- `OS_AUTH_TYPE`
- `OS_APPLICATION_CREDENTIAL_ID`
- `OS_APPLICATION_CREDENTIAL_SECRET`
- `OS_REGION_NAME`
- `OS_INTERFACE`
- `OS_IDENTITY_API_VERSION`

You will also need to specify additional parameters related to machine sizes and images:

- `OPENSTACK_CONTROL_PLANE_MACHINE_FLAVOR`
- `OPENSTACK_NODE_MACHINE_FLAVOR`
- `OPENSTACK_IMAGE_NAME`

> [!NOTE]
> The recommended minimum vCPU value for the control plane flavor is 2, while for the worker node flavor, it is 1. For detailed information, refer to the [machine-flavor CAPI docs](https://github.com/kubernetes-sigs/cluster-api-provider-openstack/blob/main/docs/book/src/clusteropenstack/configuration.md#machine-flavor).

### Adopted Cluster Setup

To "adopt" an existing cluster first obtain the kubeconfig file for the cluster.
Then set the `DEV_PROVIDER` to "adopted". Export the kubeconfig file as a variable by running the following:

`export KUBECONFIG_DATA=$(cat kubeconfig | base64 -w 0)`

The rest of the deployment procedure is same for all providers.

## Deploy KCM

Default provider which will be used to deploy cluster is AWS, if you want to use
another provider change `DEV_PROVIDER` variable with the name of provider before
running make (e.g. `export DEV_PROVIDER=azure`).

1. Configure your cluster parameters in provider specific file
   (for example `config/dev/aws-clusterdeployment.yaml` in case of AWS):
    - Configure the `name` of the ClusterDeployment
    - Change instance type or size for control plane and worker machines
    - Specify the number of control plane and worker machines, etc

2. Run `make dev-apply` to deploy and configure management cluster.

3. Wait a couple of minutes for management components to be up and running.

4. Apply credentials for your provider by executing `make dev-creds-apply`.

5. Run `make dev-mcluster-apply` to deploy managed cluster on provider of your
   choice with default configuration.

6. Wait for infrastructure to be provisioned and the cluster to be deployed. You
   may watch the process with the `./bin/clusterctl describe` command. Example:

   ```bash
   export KUBECONFIG=~/.kube/config

   ./bin/clusterctl describe cluster <clusterdeployment-name> -n kcm-system --show-conditions all
   ```

   > [!NOTE]
   > If you encounter any errors in the output of `clusterctl describe cluster` inspect the logs of the
   > `capa-controller-manager` with:
   >
   > ```bash
   > kubectl logs -n kcm-system deploy/capa-controller-manager
   > ```
   >
   > This may help identify any potential issues with deployment of the AWS infrastructure.

7. Retrieve the `kubeconfig` of your managed cluster:

   ```bash
   kubectl --kubeconfig ~/.kube/config get secret -n kcm-system <clusterdeployment-name>-kubeconfig -o=jsonpath={.data.value} | base64 -d > kubeconfig
   ```

## Running E2E tests locally

E2E tests can be ran locally via the `make test-e2e` target.

Before start, make according changes to the `test/e2e/config/config.yaml` that
holds the configuration of the e2e tests.

Ensure to set all of the required env variables before starting the tests,
there are checks to ensure all of them are set but this still consumes some time
to run the suite's `BeforeAll` node.

Optionally, the `NO_CLEANUP=1` env var can be used to disable `After` nodes from
running within some specs, this will allow users to debug tests by re-running
them without the need to wait a while for an infrastructure deployment to occur.

### Running non-hosted templates

If you plan to run only standalone/adopted/remote tests, then you can run everything
locally without any requirements to have images/helm charts/manifests pre-uploaded somewhere else.

Example command:

```bash
make KIND_CLUSTER_NAME="kcm-test" CLUSTER_DEPLOYMENT_PREFIX="<your-cluster-name-prefix>" GINKGO_LABEL_FILTER="<optional>" REGISTRY_REPO="oci://127.0.0.1:5001/charts" VERSION=1.0.0 IMG=localhost/kcm/controller:1.0.0 IMG_TELEMETRY=localhost/kcm/telemetry:1.0.0 kind-deploy registry-deploy set-kcm-version dev-push test-e2e
```

The `1.0.0` version here is given only for example, put any value you want.

`KIND_CLUSTER_NAME` with the `kcm-test` value is required since the `test-e2e` recipe hardcodes it.

`GINKGO_LABEL_FILTER` narrows the provided configuration to avoid running unnecessary `BeforeAll` and `AfterAll`
nodes. Read more about tests [filters](#filtering-test-runs).

`CLUSTER_DEPLOYMENT_PREFIX` is a required variable to ensure unique names of deployments.

### Running hosted templates

In order to run hosted templates, a **CI run must be done beforehand** to make sure
a cluster can fetch manifests, controller image and helm charts.

Example command:

```bash
make KIND_CLUSTER_NAME="kcm-test" CLUSTER_DEPLOYMENT_PREFIX="<your-cluster-name-prefix>" GINKGO_LABEL_FILTER="<optional>" IMG="ghcr.io/k0rdent/kcm/controller-ci:<version-from-ci>" IMG_TELEMETRY="ghcr.io/k0rdent/kcm/telemetry-ci:<version-from-ci>" VERSION=<version-from-ci> REGISTRY_REPO="oci://ghcr.io/k0rdent/kcm/charts-ci" set-kcm-version test-e2e
```

Substitute with a proper version from the CI run.

The rest of the variables have the same logic described [above](#running-non-hosted-templates).

### Filtering test runs

The top-level configuration is located in the `test/e2e/config/config.yaml` and it defines
the list of tests to be run.

In general, provider tests are broken into two types, `onprem` and `cloud`. For CI,
`provider:onprem` tests run on self-hosted runners provided by Mirantis.
`provider:cloud` tests run on GitHub actions runners and interact with cloud
infrastructure providers such as AWS or Azure.

Each specific provider test also has a label, for example, `provider:aws` can be
used to run only AWS tests. To utilize these filters with the `make test-e2e`
target pass the `GINKGO_LABEL_FILTER` env var, for example:

```bash
make GINKGO_LABEL_FILTER="provider:aws" REST_OF_THE=VARIABLES test-e2e
```

would run only AWS tests skipping all of the `Before` and `After` nodes for the rest
of the E2E tests. Without such a label and, for example, having only `aws` tests in the `config.yaml`,
the rest of the tests **will** run these in this case unnecessary nodes consuming more time.

To see a list of all available labels run:

```bash
ginkgo labels ./test/e2e
```

### Nuking created test resources

In CI we run `make dev-aws-nuke` and `make dev-azure-nuke` to cleanup test
resources within AWS and Azure CI environments.  These targets are not run
locally, but can be run manually if needed.  For example, to cleanup AWS
manually use:

```bash
CLUSTER_NAME=example-e2e-test make dev-aws-nuke
```

## CI/CD

### Release (`release.yml`)

The `release.yml` is triggered via a release, it builds and packages the
Helm charts images and uploads them.  Images and charts are
uploaded to `ghcr.io/k0rdent/kcm`.

### Build and Test (`build_test.yml`)

The `build_test.yml` workflow is broken into three phases:

- Lint and Unit Test
- Build and Push Artifacts When Required
- E2E Tests
- Cleanup

#### Lint and Unit Test

This job runs the controller unit tests, linters and other checks.

#### Build and Push Artifacts When Required

This job builds and packages the Helm charts and controller images uploading them
to `ghcr.io/k0rdent/kcm` so that the jobs within the E2E phase have access to
them. The job is conditional and runs only if `test-e2e` label is set on a PR and another job,
`authorize`, has been approved. The latter starts only if the former is set.

- CI charts are uploaded to `ghcr.io/k0rdent/kcm/charts-ci`
- KCM Controller image is uploaded to `ghcr.io/k0rdent/kcm/controller-ci`

All other tests within the workflow require this job to pass before they are
scheduled to run.

In order for the other jobs to be in sync the `Get outputs` step is used to
capture a shared `clustername` variable and `version` variable used across
tests.

The `authorize` job holds the E2E tests configuration.

#### E2E Tests

The E2E Tests phase is comprised of three jobs.  Each of the jobs other than the
`E2E Controller` job are conditional on the `test e2e` label being present on
the PR which triggers the workflow.

Once the `Build and Unit Test` job completes successfully all E2E jobs are
scheduled and run concurrently.

- `E2E Controller` - Runs the e2e tests for the controller via `GINKGO_LABEL_FILTER="controller"`
   The built controller is deployed in a kind cluster and the tests are run against it.
   These tests always run even if the `test e2e` label is not present.
- `E2E Cloud` - Runs the AWS and Azure provider tests via `GINKGO_LABEL_FILTER="provider:cloud"`
- `E2E Onprem` - Runs the VMWare VSphere provider tests via `GINKGO_LABEL_FILTER="provider:onprem"`
   this job runs on a self-hosted runner provided by Mirantis and utilizes Mirantis'
   internal vSphere infrastructure.

If any failures are encountered in the E2E tests the `Archive test results` step
will archive test logs and other artifacts for troubleshooting.

#### Cleanup

The Cleanup phase is comprised of one job, `Cleanup`.  This job is conditional
on the `test e2e` label being present and the `E2E Cloud` job running.  The job
will always run no matter the result of the `E2E Cloud` job and conducts cleanup
of AWS and Azure test resources.  It is intended to run no matter the result of
the `E2E Cloud` job so that resources are cleaned up even if the tests fail.

The `Cleanup` job runs the `make dev-aws-nuke` and `make dev-azure-nuke` targets.

At this time other providers do not have a mechanism for cleanup and if tests
fail to delete the resources they create they will need to be manually cleaned.

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

### OpenStack

CAPO relies on a clouds.yaml file in order to manage the OpenStack resources. This should be supplied as a Kubernetes Secret.

```yaml
clouds:
  my-openstack-cloud:
    auth:
      auth_url: <your_auth_url>
      application_credential_id: <your_credential_id>
      application_credential_secret: <your_credential_secret>
    region_name: <your_region>
    interface: <public|internal|admin>
    identity_api_version: 3
    auth_type: v3applicationcredential
```

One would typically create a Secret (for example, openstack-cloud-config) in the kcm-system namespace with the clouds.yaml. Credential object references the secret and the CAPO controllers references this Credential to provision resources.

When you deploy a new cluster, KCM automatically parses the previously created Kubernetes Secret’s data to build a cloud.conf. This cloud-config is mounted inside the CCM and/or CSI pods enabling them to manage load balancers, floating IPs, etc.
Refer to [configuring OpenStack CCM](https://github.com/kubernetes/cloud-provider-openstack/blob/master/docs/openstack-cloud-controller-manager/using-openstack-cloud-controller-manager.md#config-openstack-cloud-controller-manager) for more details.

Here's an example of the generated cloud.conf:

```ini
[Global]
auth-url=<your_auth_url>
application-credential-id=<your_credential_id>
application-credential-secret=<your_credential_secret>
region=<your_region>
domain-name=<your_domain_name>

[LoadBalancer]
floating-network-id=<your_floating_network_id>

[Networking]
public-network-name=<your_network_name>
```
